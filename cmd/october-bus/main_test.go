package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/october-dev/october-bus/bus"
)

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("OCTOBER_BUS_MCP_STDIO_TEST_HELPER") != "1" {
		return
	}
	if err := runMCPStdio(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func mcpBridgeCommand(address, token string, stderr io.Writer) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestMCPStdioHelper$")
	command.Env = setEnvironment(removeEnvironment(os.Environ(),
		"OCTOBER_BUS_ADDRESS",
		"OCTOBER_BUS_AGENT_TOKEN",
		"OCTOBER_BUS_SCOPE_TOKEN",
		"OCTOBER_BUS_ADMIN_TOKEN",
	),
		"OCTOBER_BUS_MCP_STDIO_TEST_HELPER", "1",
		"OCTOBER_BUS_ADDRESS", address,
		"OCTOBER_BUS_AGENT_TOKEN", token,
	)
	command.Stderr = stderr
	return command
}

func connectMCPBridge(t *testing.T, ctx context.Context, address, token string) (*mcp.ClientSession, *bytes.Buffer) {
	t.Helper()
	stderr := new(bytes.Buffer)
	client := mcp.NewClient(&mcp.Implementation{Name: "bridge-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: mcpBridgeCommand(address, token, stderr)}, nil)
	if err != nil {
		t.Fatalf("connect to stdio bridge: %v; stderr: %s", err, stderr.String())
	}
	return session, stderr
}

func callMCPBridgeTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil || result.IsError {
		t.Fatalf("call %s: %#v, %v", name, result, err)
	}
	return result
}

func TestMCPStdioBridgeForwardsDaemonTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address, _, senderToken, receiverToken, cleanup := startTestServer(t, ctx, "stdio-forwarding")
	defer cleanup()
	session, stderr := connectMCPBridge(t, ctx, address, senderToken)
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 14 {
		t.Fatalf("unexpected forwarded tools: %d, %v; stderr: %s", len(tools.Tools), err, stderr.String())
	}
	directClient := mcp.NewClient(&mcp.Implementation{Name: "direct-test", Version: "1"}, nil)
	directSession, err := directClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: address + "/mcp",
		HTTPClient: &http.Client{Transport: agentTokenTransport{
			token: senderToken,
			base:  http.DefaultTransport,
		}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer directSession.Close()
	directTools, err := directSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	forwardedJSON, err := json.Marshal(tools.Tools)
	if err != nil {
		t.Fatal(err)
	}
	directJSON, err := json.Marshal(directTools.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forwardedJSON, directJSON) {
		t.Fatalf("stdio bridge changed the daemon tool definitions\nforwarded: %s\ndirect: %s", forwardedJSON, directJSON)
	}
	callMCPBridgeTool(t, ctx, session, "list_peers", map[string]any{})
	callMCPBridgeTool(t, ctx, session, "message_peer", map[string]any{"peer": "receiver", "message": "Forwarded over stdio"})
	messages, err := (bus.Client{Address: address, Token: receiverToken}).PullInbox(ctx, 10, 0)
	if err != nil || len(messages) != 1 || messages[0].Body != "Forwarded over stdio" {
		t.Fatalf("unexpected forwarded message: %#v, %v", messages, err)
	}
	callMCPBridgeTool(t, ctx, session, "add_task", map[string]any{"title": "Forward a task"})
	callMCPBridgeTool(t, ctx, session, "ask_user", map[string]any{"question": "Continue?"})
}

func TestMCPStdioBridgeStartsWithoutRuntimeIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, stderr := connectMCPBridge(t, ctx, "http://127.0.0.1:1", "")
	initial := session.InitializeResult()
	if initial == nil || !strings.Contains(initial.Instructions, "not running inside a managed agent execution") {
		t.Fatalf("unexpected bridge instructions: %#v", initial)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 0 {
		t.Fatalf("unexpected tools without identity: %#v, %v; stderr: %s", tools, err, stderr.String())
	}
	if err := session.Close(); err != nil {
		t.Fatalf("stdio bridge did not stop cleanly when input closed: %v", err)
	}
}

func TestMCPStdioBridgeRejectsUnavailableDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stderr := new(bytes.Buffer)
	client := mcp.NewClient(&mcp.Implementation{Name: "bridge-test", Version: "1"}, nil)
	_, err := client.Connect(ctx, &mcp.CommandTransport{Command: mcpBridgeCommand("http://127.0.0.1:1", "agent-token", stderr)}, nil)
	if err == nil || !strings.Contains(stderr.String(), "could not connect to October Bus") {
		t.Fatalf("expected unavailable daemon error, got %v; stderr: %s", err, stderr.String())
	}
}

func TestAgentRunHelper(t *testing.T) {
	if os.Getenv("OCTOBER_BUS_TEST_HELPER") != "1" {
		return
	}
	for _, name := range []string{
		"OCTOBER_BUS_ADDRESS",
		"OCTOBER_BUS_MCP_URL",
		"OCTOBER_BUS_AGENT_ID",
		"OCTOBER_BUS_EXECUTION_ID",
		"OCTOBER_BUS_AGENT_TOKEN",
	} {
		if os.Getenv(name) == "" {
			os.Exit(2)
		}
	}
	if os.Getenv("OCTOBER_BUS_SCOPE_TOKEN") != "" {
		os.Exit(3)
	}
}

func TestStopCommandUsesProtectedLocalEndpoint(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OCTOBER_BUS_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("OCTOBER_BUS_RUNTIME_DIR", filepath.Join(root, "run"))
	daemon, err := bus.StartDaemon(context.Background(), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() {
		select {
		case <-daemon.Server.ShutdownRequested():
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stopped <- daemon.Stop(ctx)
		case <-time.After(time.Second):
			stopped <- context.DeadlineExceeded
		}
	}()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

func TestRunAgentOwnsHeartbeatAndEnvironment(t *testing.T) {
	ctx := context.Background()
	runtimeValue, err := bus.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := bus.NewServer(runtimeValue, bus.ServerOptions{})
	address, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop(context.Background())
	scope, err := runtimeValue.CreateScope(ctx, bus.CreateScopeInput{ID: "agent-run"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", scope.ScopeToken)
	t.Setenv("OCTOBER_BUS_TEST_HELPER", "1")
	if err := runAgent([]string{
		"--id", "worker",
		"--name", "Worker",
		"--address", address,
		"--lease", "30s",
		"--heartbeat", "10ms",
		"--", os.Args[0], "-test.run=TestAgentRunHelper",
	}); err != nil {
		t.Fatal(err)
	}
	agents, err := (bus.Client{Address: address, Token: scope.ScopeToken}).ListAgents(ctx)
	if err != nil || len(agents) != 1 {
		t.Fatalf("unexpected agents: %#v, %v", agents, err)
	}
	if agents[0].Lifecycle != bus.LifecycleOffline || agents[0].Ready || agents[0].Reachable {
		t.Fatalf("agent process did not leave an offline execution: %#v", agents[0])
	}
}

func TestSetEnvironmentReplacesExistingValues(t *testing.T) {
	result := setEnvironment([]string{"PATH=/bin", "OCTOBER_BUS_AGENT_TOKEN=old"},
		"OCTOBER_BUS_AGENT_TOKEN", "new",
		"OCTOBER_BUS_AGENT_ID", "worker",
	)
	if len(result) != 3 || result[0] != "PATH=/bin" || result[1] != "OCTOBER_BUS_AGENT_TOKEN=new" || result[2] != "OCTOBER_BUS_AGENT_ID=worker" {
		t.Fatalf("unexpected environment: %#v", result)
	}
}

func TestRemoveEnvironmentRemovesCredentials(t *testing.T) {
	result := removeEnvironment([]string{"PATH=/bin", "OCTOBER_BUS_SCOPE_TOKEN=secret", "OTHER=value"}, "october_bus_scope_token")
	if len(result) != 2 || result[0] != "PATH=/bin" || result[1] != "OTHER=value" {
		t.Fatalf("unexpected environment: %#v", result)
	}
}

// startTestServer spins up an in-memory bus, registers two linked agents
// (sender and receiver), and returns the address, scope token, both agent
// tokens, and a cleanup function. It mirrors the protocol exercised by
// bus.RunDemo but keeps the scope and ids local to the test.
func startTestServer(t *testing.T, ctx context.Context, scopeID string) (address, scopeToken, senderToken, receiverToken string, cleanup func()) {
	t.Helper()
	runtimeValue, err := bus.Open(":memory:")
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	server := bus.NewServer(runtimeValue, bus.ServerOptions{})
	listenAddr, err := server.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	scope, err := runtimeValue.CreateScope(ctx, bus.CreateScopeInput{ID: scopeID})
	if err != nil {
		server.Stop(context.Background())
		t.Fatalf("create scope: %v", err)
	}
	scopeClient := bus.Client{Address: listenAddr, Token: scope.ScopeToken}
	sender, err := scopeClient.RegisterAgent(ctx, bus.RegisterAgentInput{ID: "sender", DisplayName: "Sender"})
	if err != nil {
		server.Stop(context.Background())
		t.Fatalf("register sender: %v", err)
	}
	receiver, err := scopeClient.RegisterAgent(ctx, bus.RegisterAgentInput{ID: "receiver", DisplayName: "Receiver", ConnectTo: []string{"sender"}})
	if err != nil {
		server.Stop(context.Background())
		t.Fatalf("register receiver: %v", err)
	}
	if err := scopeClient.LinkAgents(ctx, "sender", "receiver"); err != nil {
		server.Stop(context.Background())
		t.Fatalf("link agents: %v", err)
	}
	cleanup = func() {
		server.Stop(context.Background())
		runtimeValue.Close()
	}
	return listenAddr, scope.ScopeToken, sender.AgentToken, receiver.AgentToken, cleanup
}

func TestInspectReceiptRequiresArgs(t *testing.T) {
	if err := inspectReceipt(nil); err == nil || !strings.Contains(err.Error(), "<message-id>") {
		t.Fatalf("expected missing-arg error, got %v", err)
	}
	if err := inspectReceipt([]string{"   "}); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected empty-id error, got %v", err)
	}
}

func TestInspectReceiptRequiresAgentToken(t *testing.T) {
	t.Setenv("OCTOBER_BUS_AGENT_TOKEN", "")
	err := inspectReceipt([]string{"--address", "http://127.0.0.1:1", "msg-1"})
	if err == nil || !strings.Contains(err.Error(), "OCTOBER_BUS_AGENT_TOKEN is required") {
		t.Fatalf("expected token-required error, got %v", err)
	}
}

func TestPrintReceiptHumanOmitsEmptyTimestamps(t *testing.T) {
	// Capture stdout while rendering, restore on exit.
	originalStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe: %v", pipeErr)
	}
	os.Stdout = w
	defer func() { os.Stdout = originalStdout }()

	receipt := bus.DeliveryReceipt{
		MessageID:   "msg-1",
		State:       bus.DeliveryDelivered,
		AcceptedAt:  "2026-08-31T10:00:00Z",
		DeliveredAt: "2026-08-31T10:00:01Z",
		// AcknowledgedAt and RepliedAt intentionally empty.
	}
	if err := printReceiptHuman(receipt); err != nil {
		t.Fatalf("printReceiptHuman: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Message msg-1",
		"State: delivered",
		"AcceptedAt: 2026-08-31T10:00:00Z",
		"DeliveredAt: 2026-08-31T10:00:01Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q\n--- output ---\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"AcknowledgedAt", "RepliedAt", "ResponseMessageID"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("human output unexpectedly contains %q\n--- output ---\n%s", unwanted, out)
		}
	}
}

func TestInspectReceiptEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	address, _, senderToken, receiverToken, cleanup := startTestServer(t, ctx, "receipt-e2e")
	defer cleanup()

	sender := bus.Client{Address: address, Token: senderToken}
	receiver := bus.Client{Address: address, Token: receiverToken}

	// Send a message, pull it on the receiver side, and acknowledge it so the
	// receipt progresses through delivered and acknowledged states.
	initial, err := sender.SendMessage(ctx, bus.SendMessageInput{To: "receiver", Body: "ping"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if initial.State != bus.DeliveryQueued {
		t.Fatalf("expected initial state queued, got %q", initial.State)
	}
	inbox, err := receiver.PullInbox(ctx, 10, 0)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("expected one inbox message, got %d", len(inbox))
	}
	if _, err := receiver.AcknowledgeMessages(ctx, []string{initial.MessageID}); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// Now drive inspectReceipt through the CLI layer with the sender token and
	// the explicit --address, capturing stdout to verify both rendering modes.
	t.Setenv("OCTOBER_BUS_AGENT_TOKEN", senderToken)

	var jsonOut, humanOut bytes.Buffer
	if err := runInspectCapturing(&jsonOut, initial.MessageID, "--json", "--address", address); err != nil {
		t.Fatalf("inspect json: %v", err)
	}
	if err := runInspectCapturing(&humanOut, "--address", address, initial.MessageID); err != nil {
		t.Fatalf("inspect human: %v", err)
	}

	var decoded bus.DeliveryReceipt
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatalf("json output did not parse as DeliveryReceipt: %v\n--- output ---\n%s", err, jsonOut.String())
	}
	if decoded.MessageID != initial.MessageID {
		t.Errorf("json messageId = %q, want %q", decoded.MessageID, initial.MessageID)
	}
	if decoded.State != bus.DeliveryAcknowledged {
		t.Errorf("json state = %q, want %q", decoded.State, bus.DeliveryAcknowledged)
	}
	if decoded.AcceptedAt == "" || decoded.DeliveredAt == "" || decoded.AcknowledgedAt == "" {
		t.Errorf("expected accepted/delivered/acknowledged timestamps, got %+v", decoded)
	}
	// Body and context must never appear in the receipt-shaped output.
	if strings.Contains(jsonOut.String(), "ping") {
		t.Errorf("json output leaked message body")
	}
	if !strings.Contains(humanOut.String(), "State: acknowledged") {
		t.Errorf("human output missing state line\n--- output ---\n%s", humanOut.String())
	}
	if !strings.Contains(humanOut.String(), "AcknowledgedAt:") {
		t.Errorf("human output missing AcknowledgedAt line\n--- output ---\n%s", humanOut.String())
	}
}

// runInspectCapturing routes inspectReceipt's stdout through a buffer by
// temporarily swapping os.Stdout. It restores the original handle on exit.
func runInspectCapturing(buf *bytes.Buffer, args ...string) error {
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(buf, r)
		close(done)
	}()
	invokeErr := inspectReceipt(args)
	if err := w.Close(); err != nil {
		os.Stdout = original
		return err
	}
	os.Stdout = original
	<-done
	_ = r.Close()
	return invokeErr
}

func TestInspectReceiptUnknownMessageReturnsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	address, _, senderToken, _, cleanup := startTestServer(t, ctx, "receipt-missing")
	defer cleanup()
	t.Setenv("OCTOBER_BUS_AGENT_TOKEN", senderToken)

	err := runInspectCapturing(&bytes.Buffer{}, "--address", address, "no-such-message")
	if err == nil {
		t.Fatal("expected error for unknown message, got nil")
	}
	var busErr *bus.BusError
	if !errors.As(err, &busErr) {
		t.Fatalf("expected *bus.BusError, got %T: %v", err, err)
	}
	if busErr.Code != bus.CodeNotFound {
		t.Errorf("expected CodeNotFound, got %q", busErr.Code)
	}
}

func TestInspectReceiptRejectsUnauthorizedCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	address, scopeToken, senderToken, _, cleanup := startTestServer(t, ctx, "receipt-authz")
	defer cleanup()

	// Register a third agent in the same scope. Its valid credential proves
	// receipt authorization is enforced independently of authentication.
	outsider, err := (bus.Client{Address: address, Token: scopeToken}).RegisterAgent(ctx, bus.RegisterAgentInput{
		ID: "outsider", DisplayName: "Outsider",
	})
	if err != nil {
		t.Fatalf("register outsider: %v", err)
	}

	sender := bus.Client{Address: address, Token: senderToken}
	initial, err := sender.SendMessage(ctx, bus.SendMessageInput{To: "receiver", Body: "secret"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	t.Setenv("OCTOBER_BUS_AGENT_TOKEN", outsider.AgentToken)
	var output bytes.Buffer
	err = runInspectCapturing(&output, "--address", address, initial.MessageID)
	if err == nil {
		t.Fatal("expected error when an outsider inspects a message they did not send or receive")
	}
	var busErr *bus.BusError
	if !errors.As(err, &busErr) {
		t.Fatalf("expected *bus.BusError, got %T: %v", err, err)
	}
	if busErr.Code != bus.CodeNotFound {
		t.Errorf("expected CodeNotFound, got %q", busErr.Code)
	}
	if strings.Contains(output.String(), "secret") {
		t.Error("receipt inspection leaked the message body")
	}
}

func TestListAgentsRequiresScopeToken(t *testing.T) {
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", "")
	err := listAgents([]string{"--address", "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "OCTOBER_BUS_SCOPE_TOKEN is required") {
		t.Fatalf("expected token-required error, got %v", err)
	}
}

func TestListAgentsRejectsPositionalArguments(t *testing.T) {
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", "unused")
	err := listAgents([]string{"extra"})
	if err == nil || !strings.Contains(err.Error(), "does not accept positional arguments") {
		t.Fatalf("expected positional-argument error, got %v", err)
	}
}

func TestPrintAgentsHumanQuotesUntrustedDisplayNames(t *testing.T) {
	agents := []bus.Agent{{
		ID:          "reviewer",
		DisplayName: "Reviewer\n\x1b[31mForged",
		Lifecycle:   bus.LifecycleReady,
		Ready:       true,
		Reachable:   true,
		Capabilities: []bus.AgentCapability{
			{Name: "review"},
		},
		UpdatedAt: "2026-08-31T10:00:00Z",
	}}
	var output bytes.Buffer
	if err := captureStdout(&output, func() error { return printAgentsHuman(agents) }); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		`reviewer ("Reviewer\n\x1b[31mForged")`,
		"lifecycle:  ready",
		"ready:      yes",
		"reachable:  yes",
		"capabilities: review",
		"updatedAt:  2026-08-31T10:00:00Z",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("human output missing %q\n--- output ---\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") {
		t.Error("human output contains a terminal escape character")
	}
}

func TestPrintAgentsHumanEmptyScope(t *testing.T) {
	var output bytes.Buffer
	if err := captureStdout(&output, func() error { return printAgentsHuman(nil) }); err != nil {
		t.Fatal(err)
	}
	if output.String() != "No agents in scope.\n" {
		t.Fatalf("unexpected empty-scope output: %q", output.String())
	}
}

func TestListAgentsEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address, scopeToken, _, _, cleanup := startTestServer(t, ctx, "agent-list-e2e")
	defer cleanup()
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", scopeToken)

	var jsonOutput, humanOutput bytes.Buffer
	if err := runListAgentsCapturing(&jsonOutput, "--json", "--address", address); err != nil {
		t.Fatalf("list JSON: %v", err)
	}
	if err := runListAgentsCapturing(&humanOutput, "--address", address); err != nil {
		t.Fatalf("list human: %v", err)
	}

	var agents []bus.Agent
	if err := json.Unmarshal(jsonOutput.Bytes(), &agents); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if len(agents) != 2 || agents[0].ID != "receiver" || agents[1].ID != "sender" {
		t.Fatalf("agents are not sorted by id: %+v", agents)
	}
	receiverIndex := strings.Index(humanOutput.String(), "receiver (")
	senderIndex := strings.Index(humanOutput.String(), "sender (")
	if receiverIndex < 0 || senderIndex < 0 || receiverIndex > senderIndex {
		t.Fatalf("human output is not sorted by id:\n%s", humanOutput.String())
	}
	if strings.Contains(jsonOutput.String(), scopeToken) || strings.Contains(humanOutput.String(), scopeToken) {
		t.Fatal("agent list output leaked the scope token")
	}
}

func TestListAgentsRejectsInvalidOrAgentCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address, _, senderToken, _, cleanup := startTestServer(t, ctx, "agent-list-auth")
	defer cleanup()

	for name, testCase := range map[string]struct {
		token string
		code  bus.ErrorCode
	}{
		"invalid":     {token: "not-a-real-token", code: bus.CodeUnauthenticated},
		"agent-token": {token: senderToken, code: bus.CodePermissionDenied},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", testCase.token)
			err := runListAgentsCapturing(&bytes.Buffer{}, "--address", address)
			var busErr *bus.BusError
			if !errors.As(err, &busErr) || busErr.Code != testCase.code {
				t.Fatalf("expected %s, got %v", testCase.code, err)
			}
		})
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/v1/agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+senderToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("agent credential returned HTTP %d, want 403", response.StatusCode)
	}
}

func runListAgentsCapturing(output *bytes.Buffer, args ...string) error {
	return captureStdout(output, func() error { return listAgents(args) })
}

func TestTaskCommandsManageScopeBoard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address, scopeToken, senderToken, _, cleanup := startTestServer(t, ctx, "task-board-e2e")
	defer cleanup()
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", scopeToken)

	var firstOutput bytes.Buffer
	if err := captureStdout(&firstOutput, func() error {
		return addTask([]string{"--title", "Implement retries", "--description", "Preserve idempotency.", "--json", "--address", address})
	}); err != nil {
		t.Fatal(err)
	}
	var first bus.Task
	if err := json.Unmarshal(firstOutput.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.CreatedBy != nil || !first.Ready || first.Title != "Implement retries" {
		t.Fatalf("unexpected first task: %#v", first)
	}

	var secondOutput bytes.Buffer
	if err := captureStdout(&secondOutput, func() error {
		return addTask([]string{"--title", "Review retries", "--depends-on", first.ID, "--json", "--address", address})
	}); err != nil {
		t.Fatal(err)
	}
	var second bus.Task
	if err := json.Unmarshal(secondOutput.Bytes(), &second); err != nil {
		t.Fatal(err)
	}

	var readyOutput bytes.Buffer
	if err := captureStdout(&readyOutput, func() error {
		return listTasks([]string{"--ready", "--json", "--address", address})
	}); err != nil {
		t.Fatal(err)
	}
	var ready []bus.Task
	if err := json.Unmarshal(readyOutput.Bytes(), &ready); err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].ID != first.ID {
		t.Fatalf("unexpected ready board: %#v", ready)
	}

	agent := bus.Client{Address: address, Token: senderToken}
	if _, err := agent.ClaimTask(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.CompleteTask(ctx, first.ID, "done"); err != nil {
		t.Fatal(err)
	}
	ready, err := (bus.Client{Address: address, Token: scopeToken}).ListTasks(ctx, true)
	if err != nil || len(ready) != 1 || ready[0].ID != second.ID {
		t.Fatalf("dependent task did not become ready: %#v, %v", ready, err)
	}
}

func TestTaskListQuotesUntrustedText(t *testing.T) {
	tasks := []bus.Task{{ID: "task_1", Title: "Review\n\x1b[31mForged", Description: "Check\nlogs", Status: "open", Ready: true}}
	var output bytes.Buffer
	if err := captureStdout(&output, func() error { return printTasksHuman(tasks) }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b") || !strings.Contains(output.String(), `"Review\n\x1b[31mForged"`) {
		t.Fatalf("task output did not quote untrusted text: %q", output.String())
	}
}

func TestScopeStorageCommandsDefaultToDryRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address, scopeToken, senderToken, _, cleanup := startTestServer(t, ctx, "storage-cli-e2e")
	defer cleanup()
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", scopeToken)
	owner := bus.Client{Address: address, Token: scopeToken}
	task, err := owner.AddTask(ctx, bus.AddTaskInput{Title: "Old completed work"})
	if err != nil {
		t.Fatal(err)
	}
	agent := bus.Client{Address: address, Token: senderToken}
	if _, err := agent.ClaimTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.CompleteTask(ctx, task.ID, "done"); err != nil {
		t.Fatal(err)
	}

	var storageOutput bytes.Buffer
	if err := captureStdout(&storageOutput, func() error {
		return scopeStorage([]string{"--json", "--address", address})
	}); err != nil {
		t.Fatal(err)
	}
	var summary bus.StorageSummary
	if err := json.Unmarshal(storageOutput.Bytes(), &summary); err != nil || summary.ScopeID != "storage-cli-e2e" {
		t.Fatalf("unexpected storage summary: %#v, %v", summary, err)
	}

	cutoff := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	var dryOutput bytes.Buffer
	if err := captureStdout(&dryOutput, func() error {
		return scopePrune([]string{"--before", cutoff, "--json", "--address", address})
	}); err != nil {
		t.Fatal(err)
	}
	var dryRun bus.PruneScopeResult
	if err := json.Unmarshal(dryOutput.Bytes(), &dryRun); err != nil || !dryRun.DryRun || dryRun.Records.Tasks != 1 {
		t.Fatalf("unexpected dry run: %#v, %v", dryRun, err)
	}
	if tasks, err := owner.ListTasks(ctx, false); err != nil || len(tasks) != 1 {
		t.Fatalf("dry run removed task: %#v, %v", tasks, err)
	}

	if err := captureStdout(&bytes.Buffer{}, func() error {
		return scopePrune([]string{"--before", cutoff, "--yes", "--address", address})
	}); err != nil {
		t.Fatal(err)
	}
	if tasks, err := owner.ListTasks(ctx, false); err != nil || len(tasks) != 0 {
		t.Fatalf("confirmed prune kept task: %#v, %v", tasks, err)
	}
}

func TestScopePruneRequiresCutoff(t *testing.T) {
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", "unused")
	if err := scopePrune(nil); err == nil || !strings.Contains(err.Error(), "requires --before") {
		t.Fatalf("expected cutoff error, got %v", err)
	}
}

func captureStdout(output *bytes.Buffer, run func() error) error {
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	os.Stdout = writer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, reader)
		close(done)
	}()
	runErr := run()
	closeErr := writer.Close()
	os.Stdout = original
	<-done
	_ = reader.Close()
	if runErr != nil {
		return runErr
	}
	return closeErr
}
