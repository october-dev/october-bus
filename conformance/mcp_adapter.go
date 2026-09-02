package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/october-dev/october-bus/bus"
)

const ProfileMCPAdapter = "mcp-adapter"

// MCPAdapterOptions describes one executable MCP adapter verification run.
type MCPAdapterOptions struct {
	Address     string
	AdminToken  string
	Command     string
	Args        []string
	Environment []string
}

type adapterConnection struct {
	session *mcp.ClientSession
	stderr  *bytes.Buffer
}

func (connection *adapterConnection) close() error {
	if connection == nil || connection.session == nil {
		return nil
	}
	return connection.session.Close()
}

func removeEnvironment(base []string, names ...string) []string {
	removed := make(map[string]bool, len(names))
	for _, name := range names {
		removed[strings.ToUpper(name)] = true
	}
	result := make([]string, 0, len(base))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if !removed[strings.ToUpper(name)] {
			result = append(result, entry)
		}
	}
	return result
}

func setEnvironment(base []string, values ...string) []string {
	replaced := make([]string, 0, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		replaced = append(replaced, values[index])
	}
	result := removeEnvironment(base, replaced...)
	for index := 0; index < len(values); index += 2 {
		result = append(result, values[index]+"="+values[index+1])
	}
	return result
}

func removeEnvironmentValues(base []string, values ...string) []string {
	protected := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			protected[value] = true
		}
	}
	result := make([]string, 0, len(base))
	for _, entry := range base {
		_, value, _ := strings.Cut(entry, "=")
		if !protected[value] {
			result = append(result, entry)
		}
	}
	return result
}

func connectAdapter(ctx context.Context, options MCPAdapterOptions, scopeToken string, registration bus.RegisterAgentResult) (*adapterConnection, error) {
	command := exec.CommandContext(ctx, options.Command, options.Args...)
	baseEnvironment := options.Environment
	if baseEnvironment == nil {
		baseEnvironment = os.Environ()
	}
	command.Env = setEnvironment(
		removeEnvironmentValues(
			removeEnvironment(baseEnvironment, "OCTOBER_BUS_ADMIN_TOKEN", "OCTOBER_BUS_SCOPE_TOKEN"),
			options.AdminToken, scopeToken,
		),
		"OCTOBER_BUS_ADDRESS", options.Address,
		"OCTOBER_BUS_AGENT_ID", registration.AgentID,
		"OCTOBER_BUS_EXECUTION_ID", registration.ExecutionID,
		"OCTOBER_BUS_AGENT_TOKEN", registration.AgentToken,
	)
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "october-bus-conformance", Version: bus.Version}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		return nil, fmt.Errorf("could not start MCP adapter: %w", err)
	}
	return &adapterConnection{session: session, stderr: stderr}, nil
}

func decodeToolResult[T any](result *mcp.CallToolResult, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	if result == nil {
		return zero, errors.New("tool returned no result")
	}
	if result.IsError {
		return zero, fmt.Errorf("tool returned an error: %s", toolResultText(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return zero, err
	}
	var decoded T
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return zero, fmt.Errorf("could not decode tool result: %w", err)
	}
	return decoded, nil
}

func toolResultText(result *mcp.CallToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, textContent.Text)
		}
	}
	if len(parts) == 0 {
		return "unspecified error"
	}
	return strings.Join(parts, "; ")
}

func callTool[T any](ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) (T, error) {
	return decodeToolResult[T](session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments}))
}

func requireToolError(ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) error {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil
	}
	if result != nil && result.IsError {
		return nil
	}
	return fmt.Errorf("expected %s to fail", name)
}

func findAgent(agents []bus.Agent, id string) (bus.Agent, error) {
	for _, agent := range agents {
		if agent.ID == id {
			return agent, nil
		}
	}
	return bus.Agent{}, fmt.Errorf("agent %s was not found", id)
}

func checkNoCredentials(logs []*bytes.Buffer, credentials ...string) error {
	for _, log := range logs {
		text := log.String()
		for _, credential := range credentials {
			if credential != "" && strings.Contains(text, credential) {
				return errors.New("adapter diagnostics contained a protected credential")
			}
		}
	}
	return nil
}

// RunMCPAdapter verifies an executable adapter over stdio MCP. It verifies the
// adapter contract, not the behavior of a particular AI model or harness.
func RunMCPAdapter(ctx context.Context, options MCPAdapterOptions) (result Result, runErr error) {
	result = Result{
		Profile: ProfileMCPAdapter, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Passed: []string{}, Failed: []Failure{},
	}
	defer func() { result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano) }()
	record := recorder{result: &result}
	if strings.TrimSpace(options.Command) == "" {
		return result, errors.New("adapter command is required")
	}

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

	scopeID := fmt.Sprintf("mcp-adapter-%d", time.Now().UnixNano())
	scope, err := admin.CreateScope(ctx, bus.CreateScopeInput{ID: scopeID})
	if err != nil {
		return result, err
	}
	owner := bus.Client{Address: options.Address, Token: scope.ScopeToken}
	controllerSession, err := bus.StartAgentSession(ctx, bus.AgentSessionOptions{
		Address: options.Address, ScopeToken: scope.ScopeToken,
		Registration:      bus.RegisterAgentInput{ID: "controller", DisplayName: "Controller", LeaseMS: 30000},
		HeartbeatInterval: 100 * time.Millisecond, InitialLifecycle: bus.LifecycleReady, InitialReady: true,
	})
	if err != nil {
		return result, err
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = controllerSession.Close(closeContext)
	}()
	controller := controllerSession.Client

	workerContext, stopWorker := context.WithCancel(ctx)
	workerSession, err := bus.StartAgentSession(workerContext, bus.AgentSessionOptions{
		Address: options.Address, ScopeToken: scope.ScopeToken,
		Registration: bus.RegisterAgentInput{
			ID: "worker", DisplayName: "Worker", ConnectTo: []string{"controller"}, LeaseMS: 30000,
			Capabilities: []bus.AgentCapability{{Name: "verification"}},
		},
		HeartbeatInterval: 100 * time.Millisecond, InitialLifecycle: bus.LifecycleReady, InitialReady: true,
	})
	if err != nil {
		stopWorker()
		return result, err
	}
	workerClosed := false
	defer func() {
		if !workerClosed {
			stopWorker()
		}
	}()
	var adapter *adapterConnection
	if err := record.check("adapter-start-and-execution-identity", func() error {
		var err error
		adapter, err = connectAdapter(ctx, options, scope.ScopeToken, workerSession.Registration)
		if err != nil {
			return err
		}
		status, err := callTool[bus.NodeStatus](ctx, adapter.session, "get_node_status", map[string]any{})
		if err != nil {
			return err
		}
		if status.Identity.AgentID != "worker" || status.Identity.ExecutionID != workerSession.Registration.ExecutionID {
			return fmt.Errorf("unexpected adapter identity: %#v", status.Identity)
		}
		return nil
	}); err != nil {
		return result, err
	}
	adapterClosed := false
	logs := []*bytes.Buffer{adapter.stderr}
	defer func() {
		if !adapterClosed {
			_ = adapter.close()
		}
	}()

	if err := record.check("external-heartbeat", func() error {
		agents, err := owner.ListAgents(ctx)
		if err != nil {
			return err
		}
		before, err := findAgent(agents, "worker")
		if err != nil {
			return err
		}
		time.Sleep(250 * time.Millisecond)
		agents, err = owner.ListAgents(ctx)
		if err != nil {
			return err
		}
		after, err := findAgent(agents, "worker")
		if err != nil {
			return err
		}
		if !after.Ready || !after.Reachable || after.UpdatedAt == before.UpdatedAt {
			return fmt.Errorf("heartbeat did not renew the ready execution: %#v", after)
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := record.check("exact-peer-discovery", func() error {
		peers, err := callTool[struct {
			Peers []bus.Agent `json:"peers"`
		}](ctx, adapter.session, "list_peers", map[string]any{})
		if err != nil {
			return err
		}
		if len(peers.Peers) != 1 || peers.Peers[0].ID != "controller" {
			return fmt.Errorf("unexpected peers: %#v", peers.Peers)
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := record.check("durable-messaging-and-acknowledgement", func() error {
		inbound, err := controller.SendMessage(ctx, bus.SendMessageInput{To: "worker", Body: "controller notification"})
		if err != nil {
			return err
		}
		inbox, err := callTool[struct {
			Messages []bus.Message `json:"messages"`
		}](ctx, adapter.session, "check_inbox", map[string]any{"limit": 10})
		if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].ID != inbound.MessageID {
			return fmt.Errorf("unexpected adapter inbox: %#v, %v", inbox.Messages, err)
		}
		acknowledged, err := callTool[map[string]int64](ctx, adapter.session, "acknowledge_messages", map[string]any{"messageIds": []string{inbound.MessageID}})
		if err != nil || acknowledged["acknowledged"] != 1 {
			return fmt.Errorf("unexpected acknowledgement: %#v, %v", acknowledged, err)
		}
		outbound, err := callTool[bus.DeliveryReceipt](ctx, adapter.session, "message_peer", map[string]any{
			"peer": "controller", "message": "worker notification", "mode": "notify",
		})
		if err != nil {
			return err
		}
		messages, err := controller.PullInbox(ctx, 10, 0)
		if err != nil || len(messages) != 1 || messages[0].ID != outbound.MessageID {
			return fmt.Errorf("unexpected controller inbox: %#v, %v", messages, err)
		}
		_, err = controller.AcknowledgeMessages(ctx, []string{outbound.MessageID})
		return err
	}); err != nil {
		return result, err
	}

	if err := record.check("correlated-request-and-response", func() error {
		request, err := controller.SendMessage(ctx, bus.SendMessageInput{To: "worker", Body: "review", Mode: bus.MessageRequest})
		if err != nil {
			return err
		}
		inbox, err := callTool[struct {
			Messages []bus.Message `json:"messages"`
		}](ctx, adapter.session, "check_inbox", map[string]any{"limit": 10})
		if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].ID != request.MessageID {
			return fmt.Errorf("unexpected request inbox: %#v, %v", inbox.Messages, err)
		}
		if _, err := callTool[map[string]int64](ctx, adapter.session, "acknowledge_messages", map[string]any{"messageIds": []string{request.MessageID}}); err != nil {
			return err
		}
		response, err := callTool[bus.DeliveryReceipt](ctx, adapter.session, "message_peer", map[string]any{
			"peer": "controller", "message": "reviewed", "mode": "response", "responseTo": request.MessageID,
		})
		if err != nil {
			return err
		}
		messages, err := controller.PullInbox(ctx, 10, 0)
		if err != nil || len(messages) != 1 || messages[0].ID != response.MessageID {
			return fmt.Errorf("unexpected response inbox: %#v, %v", messages, err)
		}
		if _, err := controller.AcknowledgeMessages(ctx, []string{response.MessageID}); err != nil {
			return err
		}
		receipt, err := controller.Receipt(ctx, request.MessageID)
		if err != nil || receipt.ResponseMessageID != response.MessageID {
			return fmt.Errorf("request did not link response: %#v, %v", receipt, err)
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := record.check("idempotent-send", func() error {
		arguments := map[string]any{
			"peer": "controller", "message": "retry once", "mode": "request", "idempotencyKey": "adapter-conformance-retry",
		}
		first, err := callTool[bus.DeliveryReceipt](ctx, adapter.session, "message_peer", arguments)
		if err != nil {
			return err
		}
		second, err := callTool[bus.DeliveryReceipt](ctx, adapter.session, "message_peer", arguments)
		if err != nil || first.MessageID != second.MessageID {
			return fmt.Errorf("retry created a duplicate: %#v, %#v, %v", first, second, err)
		}
		messages, err := controller.PullInbox(ctx, 10, 0)
		if err != nil || len(messages) != 1 || messages[0].ID != first.MessageID {
			return fmt.Errorf("unexpected retry delivery: %#v, %v", messages, err)
		}
		if _, err := controller.AcknowledgeMessages(ctx, []string{first.MessageID}); err != nil {
			return err
		}
		_, err = controller.SendMessage(ctx, bus.SendMessageInput{
			To: "worker", Body: "accepted", Mode: bus.MessageResponse, ResponseTo: first.MessageID,
		})
		if err != nil {
			return err
		}
		inbox, err := callTool[struct {
			Messages []bus.Message `json:"messages"`
		}](ctx, adapter.session, "check_inbox", map[string]any{"limit": 10})
		if err != nil || len(inbox.Messages) != 1 {
			return fmt.Errorf("unexpected retry response: %#v, %v", inbox.Messages, err)
		}
		_, err = callTool[map[string]int64](ctx, adapter.session, "acknowledge_messages", map[string]any{"messageIds": []string{inbox.Messages[0].ID}})
		return err
	}); err != nil {
		return result, err
	}

	if err := record.check("bounded-context", func() error {
		receipt, err := callTool[bus.DeliveryReceipt](ctx, adapter.session, "message_peer", map[string]any{
			"peer": "controller", "message": "bounded context", "context": []map[string]any{{
				"kind": "text", "title": "Relevant finding", "text": "Only this finding is shared.",
			}},
		})
		if err != nil {
			return err
		}
		messages, err := controller.PullInbox(ctx, 10, 0)
		if err != nil || len(messages) != 1 || messages[0].ID != receipt.MessageID || len(messages[0].Context) != 1 || messages[0].Context[0].Title != "Relevant finding" {
			return fmt.Errorf("unexpected bounded context: %#v, %v", messages, err)
		}
		_, err = controller.AcknowledgeMessages(ctx, []string{receipt.MessageID})
		return err
	}); err != nil {
		return result, err
	}

	if err := record.check("shared-task-lifecycle", func() error {
		first, err := callTool[bus.Task](ctx, adapter.session, "add_task", map[string]any{"title": "First task"})
		if err != nil {
			return err
		}
		blocked, err := callTool[bus.Task](ctx, adapter.session, "add_task", map[string]any{
			"title": "Dependent task", "dependencies": []string{first.ID},
		})
		if err != nil {
			return err
		}
		if err := requireToolError(ctx, adapter.session, "claim_task", map[string]any{"taskId": blocked.ID}); err != nil {
			return err
		}
		if _, err := callTool[bus.Task](ctx, adapter.session, "claim_task", map[string]any{"taskId": first.ID}); err != nil {
			return err
		}
		entry, err := callTool[bus.TaskProgress](ctx, adapter.session, "add_task_progress", map[string]any{
			"taskId": first.ID, "kind": "progress", "text": "Adapter review started",
		})
		if err != nil || entry.Sequence != 1 {
			return fmt.Errorf("unexpected task progress: %#v, %v", entry, err)
		}
		history, err := callTool[struct {
			Progress []bus.TaskProgress `json:"progress"`
		}](ctx, adapter.session, "list_task_progress", map[string]any{"taskId": first.ID})
		if err != nil || len(history.Progress) != 1 || history.Progress[0].Text != "Adapter review started" {
			return fmt.Errorf("unexpected task progress history: %#v, %v", history.Progress, err)
		}
		for _, call := range []struct {
			name string
			args map[string]any
		}{
			{"release_task", map[string]any{"taskId": first.ID}},
			{"claim_task", map[string]any{"taskId": first.ID}},
			{"complete_task", map[string]any{"taskId": first.ID, "note": "Complete"}},
		} {
			if _, err := callTool[bus.Task](ctx, adapter.session, call.name, call.args); err != nil {
				return err
			}
		}
		if _, err := controller.ClaimTask(ctx, blocked.ID); err != nil {
			return err
		}
		_, err = controller.CompleteTask(ctx, blocked.ID, "Complete")
		return err
	}); err != nil {
		return result, err
	}

	if err := record.check("human-escalation-boundary", func() error {
		escalation, err := callTool[bus.HumanEscalation](ctx, adapter.session, "ask_user", map[string]any{
			"question": "Continue?", "options": []string{"yes", "no"},
		})
		if err != nil {
			return err
		}
		if _, err := workerSession.Client.ResolveEscalation(ctx, escalation.ID, "yes"); requireCode(err, bus.CodeUnauthenticated) != nil {
			return fmt.Errorf("agent resolved its own escalation: %v", err)
		}
		resolved, err := owner.ResolveEscalation(ctx, escalation.ID, "yes")
		if err != nil || resolved.Status != "resolved" {
			return fmt.Errorf("owner could not resolve escalation: %#v, %v", resolved, err)
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := record.check("execution-replacement", func() error {
		replacement, err := owner.RegisterAgent(ctx, bus.RegisterAgentInput{ID: "worker", DisplayName: "Worker", LeaseMS: 30000})
		if err != nil {
			return err
		}
		if replacement.ExecutionID == workerSession.Registration.ExecutionID {
			return errors.New("replacement reused the previous execution id")
		}
		if err := requireToolError(ctx, adapter.session, "list_tasks", map[string]any{}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return result, err
	}

	if err := adapter.close(); err != nil {
		return result, err
	}
	adapterClosed = true
	stopWorker()
	<-workerSession.Done()
	workerClosed = true

	if err := record.check("clean-and-expired-lifecycle", func() error {
		cleanContext, stopClean := context.WithCancel(ctx)
		cleanSession, err := bus.StartAgentSession(cleanContext, bus.AgentSessionOptions{
			Address: options.Address, ScopeToken: scope.ScopeToken,
			Registration:      bus.RegisterAgentInput{ID: "clean-worker", DisplayName: "Clean Worker", LeaseMS: 30000},
			HeartbeatInterval: 100 * time.Millisecond, InitialLifecycle: bus.LifecycleReady, InitialReady: true,
		})
		if err != nil {
			stopClean()
			return err
		}
		cleanAdapter, err := connectAdapter(ctx, options, scope.ScopeToken, cleanSession.Registration)
		if err != nil {
			stopClean()
			return err
		}
		logs = append(logs, cleanAdapter.stderr)
		if err := cleanAdapter.close(); err != nil {
			stopClean()
			return err
		}
		closeContext, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelClose()
		if err := cleanSession.Close(closeContext); err != nil {
			stopClean()
			return err
		}
		stopClean()
		agents, err := owner.ListAgents(ctx)
		if err != nil {
			return err
		}
		cleanAgent, err := findAgent(agents, "clean-worker")
		if err != nil || cleanAgent.Lifecycle != bus.LifecycleOffline || cleanAgent.Reachable {
			return fmt.Errorf("clean worker did not go offline: %#v, %v", cleanAgent, err)
		}

		crashContext, stopCrash := context.WithCancel(ctx)
		crashSession, err := bus.StartAgentSession(crashContext, bus.AgentSessionOptions{
			Address: options.Address, ScopeToken: scope.ScopeToken,
			Registration:      bus.RegisterAgentInput{ID: "crash-worker", DisplayName: "Crash Worker", LeaseMS: 30000},
			HeartbeatInterval: 100 * time.Millisecond, InitialLifecycle: bus.LifecycleReady, InitialReady: true,
		})
		if err != nil {
			stopCrash()
			return err
		}
		crashAdapter, err := connectAdapter(ctx, options, scope.ScopeToken, crashSession.Registration)
		if err != nil {
			stopCrash()
			return err
		}
		logs = append(logs, crashAdapter.stderr)
		task, err := callTool[bus.Task](ctx, crashAdapter.session, "add_task", map[string]any{"title": "Recover after expiry"})
		if err != nil {
			_ = crashAdapter.close()
			stopCrash()
			return err
		}
		if _, err := callTool[bus.Task](ctx, crashAdapter.session, "claim_task", map[string]any{"taskId": task.ID}); err != nil {
			_ = crashAdapter.close()
			stopCrash()
			return err
		}
		stopCrash()
		<-crashSession.Done()
		if err := crashAdapter.close(); err != nil {
			return err
		}
		expiryDeadline := time.Now().Add(33 * time.Second)
		for {
			agents, err = owner.ListAgents(ctx)
			if err != nil {
				return err
			}
			crashed, findErr := findAgent(agents, "crash-worker")
			if findErr != nil {
				return findErr
			}
			if !crashed.Reachable {
				break
			}
			if time.Now().After(expiryDeadline) {
				return fmt.Errorf("expired worker remained reachable: %#v", crashed)
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if _, err := controller.ClaimTask(ctx, task.ID); err != nil {
			return fmt.Errorf("expired execution claim was not recovered: %w", err)
		}
		_, err = controller.CompleteTask(ctx, task.ID, "Recovered")
		return err
	}); err != nil {
		return result, err
	}

	if err := record.check("credential-isolation", func() error {
		return checkNoCredentials(logs, options.AdminToken, scope.ScopeToken)
	}); err != nil {
		return result, err
	}

	return result, nil
}
