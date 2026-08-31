package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/october-dev/october-bus/bus"
)

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
	inbox, err := receiver.PullInbox(ctx, 10)
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
