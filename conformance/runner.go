package conformance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/october-dev/october-bus/bus"
)

const ProfileLocalRuntime = "local-runtime"

type Options struct {
	Address    string
	AdminToken string
}

type Result struct {
	Profile         string    `json:"profile"`
	ProtocolVersion string    `json:"protocolVersion"`
	RuntimeVersion  string    `json:"runtimeVersion"`
	StartedAt       string    `json:"startedAt"`
	CompletedAt     string    `json:"completedAt"`
	Passed          []string  `json:"passed"`
	Failed          []Failure `json:"failed"`
}

type Failure struct {
	Check string `json:"check"`
	Error string `json:"error"`
}

type recorder struct {
	result *Result
}

func (r recorder) check(name string, fn func() error) error {
	if err := fn(); err != nil {
		r.result.Failed = append(r.result.Failed, Failure{Check: name, Error: err.Error()})
		return fmt.Errorf("%s: %w", name, err)
	}
	r.result.Passed = append(r.result.Passed, name)
	return nil
}

func requireCode(err error, code bus.ErrorCode) error {
	if err == nil {
		return fmt.Errorf("expected %s", code)
	}
	var failure *bus.BusError
	if !errors.As(err, &failure) || failure.Code != code {
		return fmt.Errorf("expected %s, got %v", code, err)
	}
	return nil
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

func requireStructuredArray(result *mcp.CallToolResult, field string) error {
	if result == nil || result.IsError {
		return fmt.Errorf("tool returned an error: %#v", result)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return fmt.Errorf("structured content must be an object: %#v", result.StructuredContent)
	}
	if _, ok := structured[field].([]any); !ok {
		return fmt.Errorf("structured content must contain %s array: %#v", field, result.StructuredContent)
	}
	return nil
}

// Run exercises the local runtime profile through the public HTTP and MCP interfaces.
func Run(ctx context.Context, options Options) (result Result, runErr error) {
	result = Result{
		Profile: ProfileLocalRuntime, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Passed: []string{}, Failed: []Failure{},
	}
	defer func() { result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano) }()
	record := recorder{result: &result}
	admin := bus.Client{Address: options.Address, Token: options.AdminToken}
	var health bus.Health
	if err := record.check("health-and-version", func() error {
		var err error
		health, err = admin.Health(ctx)
		if err != nil {
			return err
		}
		if health.ProtocolVersion != bus.ProtocolVersion || health.Status != "ready" {
			return fmt.Errorf("unexpected health response: %#v", health)
		}
		return nil
	}); err != nil {
		return result, err
	}
	result.ProtocolVersion = health.ProtocolVersion
	result.RuntimeVersion = health.RuntimeVersion

	scopeID := fmt.Sprintf("conformance-%d", time.Now().UnixNano())
	var scope bus.CreateScopeResult
	if err := record.check("scope-authority", func() error {
		var err error
		scope, err = admin.CreateScope(ctx, bus.CreateScopeInput{ID: scopeID})
		if err != nil {
			return err
		}
		_, err = (bus.Client{Address: options.Address}).CreateScope(ctx, bus.CreateScopeInput{})
		return requireCode(err, bus.CodeUnauthenticated)
	}); err != nil {
		return result, err
	}
	owner := bus.Client{Address: options.Address, Token: scope.ScopeToken}

	var plannerRegistration, reviewerRegistration bus.RegisterAgentResult
	if err := record.check("registration-and-peer-link", func() error {
		var err error
		plannerRegistration, err = owner.RegisterAgent(ctx, bus.RegisterAgentInput{
			ID: "planner", DisplayName: "Planner", LeaseMS: 30000,
			Capabilities: []bus.AgentCapability{{Name: "planning"}},
		})
		if err != nil {
			return err
		}
		reviewerRegistration, err = owner.RegisterAgent(ctx, bus.RegisterAgentInput{
			ID: "reviewer", DisplayName: "Reviewer", LeaseMS: 30000, ConnectTo: []string{"planner"},
			Capabilities: []bus.AgentCapability{{Name: "review"}},
		})
		return err
	}); err != nil {
		return result, err
	}
	planner := bus.Client{Address: options.Address, Token: plannerRegistration.AgentToken}
	reviewer := bus.Client{Address: options.Address, Token: reviewerRegistration.AgentToken}

	if err := record.check("presence-and-discovery", func() error {
		if _, err := planner.Heartbeat(ctx, bus.HeartbeatInput{Lifecycle: bus.LifecycleReady, Ready: true, LeaseMS: 30000}); err != nil {
			return err
		}
		if _, err := reviewer.Heartbeat(ctx, bus.HeartbeatInput{Lifecycle: bus.LifecycleReady, Ready: true, LeaseMS: 30000}); err != nil {
			return err
		}
		plannerPeers, err := planner.ListPeers(ctx)
		if err != nil || len(plannerPeers) != 1 || plannerPeers[0].ID != "reviewer" || !plannerPeers[0].Ready {
			return fmt.Errorf("unexpected planner peers: %#v, %v", plannerPeers, err)
		}
		reviewerPeers, err := reviewer.ListPeers(ctx)
		if err != nil || len(reviewerPeers) != 1 || reviewerPeers[0].ID != "planner" {
			return fmt.Errorf("unexpected reviewer peers: %#v, %v", reviewerPeers, err)
		}
		return nil
	}); err != nil {
		return result, err
	}

	var request bus.DeliveryReceipt
	if err := record.check("durable-request-and-idempotency", func() error {
		input := bus.SendMessageInput{
			To: "reviewer", Mode: bus.MessageRequest, Body: "Review the retry path",
			IdempotencyKey: "conformance-request", Context: []bus.ContextItem{{Kind: "text", Title: "Scope", Text: "Retry logic only"}},
		}
		var err error
		request, err = planner.SendMessage(ctx, input)
		if err != nil {
			return err
		}
		retry, err := planner.SendMessage(ctx, input)
		if err != nil || retry.MessageID != request.MessageID {
			return fmt.Errorf("retry did not return original receipt: %#v, %v", retry, err)
		}
		input.Body = "Different content"
		_, err = planner.SendMessage(ctx, input)
		return requireCode(err, bus.CodeConflict)
	}); err != nil {
		return result, err
	}

	if err := record.check("reservation-delivery-and-acknowledgement", func() error {
		reservation, err := reviewer.ReserveInbox(ctx, 10, 0)
		if err != nil || reservation == nil || len(reservation.Messages) != 1 || reservation.Messages[0].ID != request.MessageID {
			return fmt.Errorf("unexpected reservation: %#v, %v", reservation, err)
		}
		messages, err := reviewer.CommitInbox(ctx, reservation.ID)
		if err != nil || len(messages) != 1 || messages[0].State != bus.DeliveryDelivered {
			return fmt.Errorf("unexpected committed messages: %#v, %v", messages, err)
		}
		count, err := reviewer.AcknowledgeMessages(ctx, []string{request.MessageID})
		if err != nil || count != 1 {
			return fmt.Errorf("unexpected acknowledgement: %d, %v", count, err)
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := record.check("correlated-response", func() error {
		response, err := reviewer.SendMessage(ctx, bus.SendMessageInput{
			To: "planner", Mode: bus.MessageResponse, ResponseTo: request.MessageID,
			Body: "The retry path is correct", IdempotencyKey: "conformance-response",
		})
		if err != nil {
			return err
		}
		messages, err := planner.PullInbox(ctx, 10, 0)
		if err != nil || len(messages) != 1 || messages[0].ID != response.MessageID {
			return fmt.Errorf("unexpected response inbox: %#v, %v", messages, err)
		}
		if _, err := planner.AcknowledgeMessages(ctx, []string{response.MessageID}); err != nil {
			return err
		}
		receipt, err := planner.Receipt(ctx, request.MessageID)
		if err != nil || receipt.ResponseMessageID != response.MessageID || receipt.RepliedAt == "" {
			return fmt.Errorf("request did not link response: %#v, %v", receipt, err)
		}
		_, err = reviewer.SendMessage(ctx, bus.SendMessageInput{
			To: "planner", Mode: bus.MessageResponse, ResponseTo: request.MessageID, Body: "Second response",
		})
		return requireCode(err, bus.CodeConflict)
	}); err != nil {
		return result, err
	}

	if err := record.check("message-expiry", func() error {
		receipt, err := planner.SendMessage(ctx, bus.SendMessageInput{To: "reviewer", Body: "Expire", ExpiresInMS: 30})
		if err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		messages, err := reviewer.PullInbox(ctx, 10, 0)
		if err != nil || len(messages) != 0 {
			return fmt.Errorf("expired message was delivered: %#v, %v", messages, err)
		}
		state, err := planner.Receipt(ctx, receipt.MessageID)
		if err != nil || state.State != bus.DeliveryExpired {
			return fmt.Errorf("unexpected expiry receipt: %#v, %v", state, err)
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := record.check("bounded-inbox-wait", func() error {
		type waitResult struct {
			messages []bus.Message
			err      error
		}
		waiting := make(chan waitResult, 1)
		go func() {
			messages, err := reviewer.PullInbox(ctx, 10, 2*time.Second)
			waiting <- waitResult{messages: messages, err: err}
		}()
		time.Sleep(50 * time.Millisecond)
		receipt, err := planner.SendMessage(ctx, bus.SendMessageInput{To: "reviewer", Body: "Wake waiting inbox"})
		if err != nil {
			return err
		}
		select {
		case waited := <-waiting:
			if waited.err != nil || len(waited.messages) != 1 || waited.messages[0].ID != receipt.MessageID {
				return fmt.Errorf("unexpected waited inbox: %#v, %v", waited.messages, waited.err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		count, err := reviewer.AcknowledgeMessages(ctx, []string{receipt.MessageID})
		if err != nil || count != 1 {
			return fmt.Errorf("unexpected waited acknowledgement: %d, %v", count, err)
		}
		return nil
	}); err != nil {
		return result, err
	}

	var firstTask, blockedTask bus.Task
	if err := record.check("task-dependencies-release-and-completion", func() error {
		var err error
		firstTask, err = planner.AddTask(ctx, bus.AddTaskInput{Description: "Review"})
		if err != nil {
			return err
		}
		blockedTask, err = planner.AddTask(ctx, bus.AddTaskInput{Description: "Apply", Dependencies: []string{firstTask.ID}})
		if err != nil {
			return err
		}
		_, err = reviewer.ClaimTask(ctx, blockedTask.ID)
		if err := requireCode(err, bus.CodeConflict); err != nil {
			return err
		}
		if _, err := reviewer.ClaimTask(ctx, firstTask.ID); err != nil {
			return err
		}
		if _, err := reviewer.ReleaseTask(ctx, firstTask.ID); err != nil {
			return err
		}
		if _, err := reviewer.ClaimTask(ctx, firstTask.ID); err != nil {
			return err
		}
		if _, err := reviewer.CompleteTask(ctx, firstTask.ID, "Reviewed"); err != nil {
			return err
		}
		if _, err := planner.ClaimTask(ctx, blockedTask.ID); err != nil {
			return err
		}
		completed, err := planner.CompleteTask(ctx, blockedTask.ID, "Applied")
		if err != nil || completed.Status != "done" {
			return fmt.Errorf("unexpected completed task: %#v, %v", completed, err)
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := record.check("execution-replacement-and-stale-claim-recovery", func() error {
		task, err := planner.AddTask(ctx, bus.AddTaskInput{Description: "Recover claim"})
		if err != nil {
			return err
		}
		if _, err := reviewer.ClaimTask(ctx, task.ID); err != nil {
			return err
		}
		replacement, err := owner.RegisterAgent(ctx, bus.RegisterAgentInput{ID: "reviewer", DisplayName: "Reviewer", LeaseMS: 30000})
		if err != nil {
			return err
		}
		_, err = reviewer.ListTasks(ctx)
		if err := requireCode(err, bus.CodeUnauthenticated); err != nil {
			return err
		}
		reviewer = bus.Client{Address: options.Address, Token: replacement.AgentToken}
		if _, err := reviewer.Heartbeat(ctx, bus.HeartbeatInput{Lifecycle: bus.LifecycleReady, Ready: true, LeaseMS: 30000}); err != nil {
			return err
		}
		if _, err := reviewer.ClaimTask(ctx, task.ID); err != nil {
			return err
		}
		_, err = reviewer.CompleteTask(ctx, task.ID, "Recovered")
		return err
	}); err != nil {
		return result, err
	}

	if err := record.check("human-escalation-boundary", func() error {
		escalation, err := planner.AskHuman(ctx, bus.AskHumanInput{Question: "Proceed?", Options: []string{"yes", "no"}})
		if err != nil {
			return err
		}
		_, err = planner.ResolveEscalation(ctx, escalation.ID, "yes")
		if err := requireCode(err, bus.CodeUnauthenticated); err != nil {
			return err
		}
		escalations, err := owner.ListEscalations(ctx)
		if err != nil || len(escalations) != 1 || escalations[0].ID != escalation.ID {
			return fmt.Errorf("unexpected owner escalations: %#v, %v", escalations, err)
		}
		resolved, err := owner.ResolveEscalation(ctx, escalation.ID, "yes")
		if err != nil || resolved.Status != "resolved" || resolved.Answer != "yes" {
			return fmt.Errorf("unexpected resolution: %#v, %v", resolved, err)
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := record.check("scope-isolation", func() error {
		otherScope, err := admin.CreateScope(ctx, bus.CreateScopeInput{ID: scopeID + "-other"})
		if err != nil {
			return err
		}
		intruderRegistration, err := (bus.Client{Address: options.Address, Token: otherScope.ScopeToken}).RegisterAgent(ctx, bus.RegisterAgentInput{
			ID: "intruder", DisplayName: "Intruder", LeaseMS: 30000,
		})
		if err != nil {
			return err
		}
		intruder := bus.Client{Address: options.Address, Token: intruderRegistration.AgentToken}
		_, err = intruder.SendMessage(ctx, bus.SendMessageInput{To: "planner", Body: "Cross scope"})
		return requireCode(err, bus.CodePermissionDenied)
	}); err != nil {
		return result, err
	}

	if err := record.check("mcp-tool-surface", func() error {
		httpClient := &http.Client{
			Transport: bearerTransport{token: reviewer.Token, base: http.DefaultTransport},
			Timeout:   10 * time.Second,
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "october-bus-conformance", Version: bus.Version}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint: options.Address + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true,
		}, nil)
		if err != nil {
			return err
		}
		defer session.Close()
		tools, err := session.ListTools(ctx, nil)
		if err != nil {
			return err
		}
		if len(tools.Tools) != 11 {
			return fmt.Errorf("unexpected MCP tool count: %d", len(tools.Tools))
		}
		peers, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_peers", Arguments: map[string]any{}})
		if err != nil {
			return err
		}
		if err := requireStructuredArray(peers, "peers"); err != nil {
			return err
		}
		tasks, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_tasks", Arguments: map[string]any{}})
		if err != nil {
			return err
		}
		return requireStructuredArray(tasks, "tasks")
	}); err != nil {
		return result, err
	}

	if err := record.check("clean-offline-lifecycle", func() error {
		if _, err := planner.Heartbeat(ctx, bus.HeartbeatInput{Lifecycle: bus.LifecycleOffline, Ready: false, LeaseMS: 30000}); err != nil {
			return err
		}
		_, err := reviewer.Heartbeat(ctx, bus.HeartbeatInput{Lifecycle: bus.LifecycleOffline, Ready: false, LeaseMS: 30000})
		return err
	}); err != nil {
		return result, err
	}

	return result, nil
}
