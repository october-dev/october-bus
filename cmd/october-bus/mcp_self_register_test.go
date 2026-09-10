package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/october-dev/october-bus/bus"
)

func TestStructuredArgumentCoercion(t *testing.T) {
	types := structuredArgumentTypes(json.RawMessage(`{"properties":{"ids":{"type":["null","array"]},"object":{"type":"object"},"text":{"type":"string"},"either":{"type":["string","array"]}}}`))
	require(t, len(types) == 2, "expected structured schema fields: %#v", types)
	for _, pair := range [][2]string{
		{`{"ids":"[\"one\"]","object":"{\"n\":9007199254740993}"}`, `{"ids":["one"],"object":{"n":9007199254740993}}`},
		{`{"ids":["one"],"text":"[]","either":"[]"}`, `{"ids":["one"],"text":"[]","either":"[]"}`},
		{`{"ids":"{}","object":"[]"}`, `{"ids":"{}","object":"[]"}`},
		{`{"ids":"[broken","object":"null"}`, `{"ids":"[broken","object":"null"}`},
		{`{"ids":null}`, `{"ids":null}`},
	} {
		got := coerceStructuredArguments(json.RawMessage(pair[0]), types)
		var actual, expected any
		decoder := json.NewDecoder(bytes.NewReader(got))
		decoder.UseNumber()
		requireNoError(t, decoder.Decode(&actual))
		decoder = json.NewDecoder(strings.NewReader(pair[1]))
		decoder.UseNumber()
		requireNoError(t, decoder.Decode(&expected))
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(expected)
		require(t, bytes.Equal(a, b), "coercion: got %s, want %s", a, b)
	}
}

func TestMCPStdioSelfRegisteredLifecycle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OCTOBER_BUS_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("OCTOBER_BUS_RUNTIME_DIR", filepath.Join(root, "run"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	daemon, err := bus.StartDaemon(ctx, 0, nil)
	requireNoError(t, err)
	defer daemon.Stop(context.Background())
	var output bytes.Buffer
	requireNoError(t, captureStdout(&output, func() error { return createScope("self-register") }))
	owner, err := localScopeClient("self-register")
	requireNoError(t, err)
	peer, err := owner.RegisterAgent(ctx, bus.RegisterAgentInput{ID: "peer", DisplayName: "Peer"})
	requireNoError(t, err)
	connect := func() *mcp.ClientSession {
		stderr := new(bytes.Buffer)
		command := mcpBridgeCommand("", "", stderr)
		args, _ := json.Marshal([]string{"--scope", "self-register", "--agent", "editor"})
		command.Env = setEnvironment(command.Env, "OCTOBER_BUS_MCP_STDIO_TEST_ARGS", string(args))
		client := mcp.NewClient(&mcp.Implementation{Name: "self-register-test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
		require(t, err == nil, "self-register: %v", err)
		return session
	}
	bridge := connect()
	defer bridge.Close()
	requireNoError(t, captureStdout(&output, func() error { return linkAgents([]string{"--scope", "self-register", "editor", "peer"}) }))
	peerClient := bus.Client{Address: owner.Address, Token: peer.AgentToken}
	receipt, err := peerClient.SendMessage(ctx, bus.SendMessageInput{To: "editor", Body: "Review", Mode: bus.MessageRequest})
	requireNoError(t, err)
	callMCPBridgeTool(t, ctx, bridge, "check_inbox", map[string]any{})
	ids, _ := json.Marshal([]string{receipt.MessageID})
	callMCPBridgeTool(t, ctx, bridge, "acknowledge_messages", map[string]any{"messageIds": string(ids)})
	callMCPBridgeTool(t, ctx, bridge, "message_peer", map[string]any{"peer": "peer", "message": "Reviewed", "mode": "response", "responseTo": receipt.MessageID})
	callMCPBridgeTool(t, ctx, bridge, "message_receipt", map[string]any{"messageId": receipt.MessageID})
	state, err := peerClient.Receipt(ctx, receipt.MessageID)
	require(t, err == nil && state.ResponseMessageID != "", "receipt did not link reply: %#v %v", state, err)
	// A cancelled tool must not poison the upstream HTTP connection.
	waitCtx, cancelWait := context.WithCancel(ctx)
	waitDone := make(chan error, 1)
	go func() {
		_, err := bridge.CallTool(waitCtx, &mcp.CallToolParams{Name: "check_inbox", Arguments: map[string]any{"waitMs": 25_000}})
		waitDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancelWait()
	require(t, <-waitDone != nil, "cancelled tool unexpectedly succeeded")
	callMCPBridgeTool(t, ctx, bridge, "get_node_status", map[string]any{})
	// Reconnecting the same identity fences the old execution, not the peer.
	replacement := connect()
	defer replacement.Close()
	stale, err := bridge.CallTool(ctx, &mcp.CallToolParams{Name: "list_peers", Arguments: map[string]any{}})
	require(t, err != nil || stale.IsError, "replaced bridge retained authority")
	requireNoError(t, replacement.Close())
	agents, err := owner.ListAgents(ctx)
	requireNoError(t, err)
	for _, agent := range agents {
		if agent.ID == "editor" {
			require(t, agent.Lifecycle == bus.LifecycleOffline && !agent.Reachable, "EOF did not retire: %#v", agent)
		}
		if agent.ID == "peer" {
			require(t, agent.Reachable, "unrelated peer was retired")
		}
	}
	// Rotation refreshes local discovery; deletion removes the saved credential.
	t.Setenv("OCTOBER_BUS_ADMIN_TOKEN", "")
	requireNoError(t, captureStdout(&output, func() error { return scopeAdmin("rotate-token", []string{"--id", "self-register"}) }))
	newOwner, err := localScopeClient("self-register")
	requireNoError(t, err)
	require(t, newOwner.Token != owner.Token, "rotation left stale local token")
	_, err = newOwner.ListAgents(ctx)
	requireNoError(t, err)
	requireNoError(t, captureStdout(&output, func() error {
		return scopeAdmin("delete", []string{"--id", "self-register", "--confirm", "self-register"})
	}))
	_, err = bus.ReadScopeToken(filepath.Join(root, "data"), "self-register")
	require(t, err != nil, "scope deletion retained credential")
}
