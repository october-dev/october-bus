package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/october-dev/october-bus/bus"
)

func runCapture(t *testing.T, fn func() error) string {
	t.Helper()
	var out bytes.Buffer
	if err := captureStdout(&out, fn); err != nil {
		t.Fatalf("command failed: %v", err)
	}
	return out.String()
}

func TestWatchOncePrintsScopedEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address, scopeToken, _, _, cleanup := startTestServer(t, ctx, "watch-once")
	defer cleanup()
	t.Setenv("OCTOBER_BUS_SCOPE_TOKEN", scopeToken)

	out := runCapture(t, func() error {
		return runWatch([]string{"--address", address, "--once", "--from", "0", "--limit", "50"})
	})
	if !strings.Contains(out, "agent.registered") || !strings.Contains(out, "link.created") {
		t.Fatalf("expected registration/link events in stream, got: %s", out)
	}
}

func TestWatchFiltersByAgentAndType(t *testing.T) {
	event := bus.BusEvent{
		Type: "message.delivered", SubjectID: "msg_1",
		Attributes: map[string]string{"from": "sender", "to": "receiver", "mode": "notify"},
	}
	if !watchInclude(event, "receiver", nil) {
		t.Fatal("to-agent must match --agent")
	}
	if watchInclude(event, "bystander", nil) {
		t.Fatal("unrelated agent must not match")
	}
	if !watchInclude(event, "", []string{"message."}) {
		t.Fatal("type prefix must match")
	}
	if watchInclude(event, "", []string{"task."}) {
		t.Fatal("non-matching type prefix must exclude")
	}
	if !watchInclude(event, "receiver", []string{"message."}) {
		t.Fatal("agent + matching type prefix must include")
	}
	if watchInclude(event, "receiver", []string{"task."}) {
		t.Fatal("agent match cannot override a failing type filter")
	}
}

func TestWatchRejectsBadArgs(t *testing.T) {
	if err := runWatch([]string{"--wait", "99999"}); err == nil {
		t.Fatal("expected --wait bound error")
	}
	if err := runWatch([]string{"extra"}); err == nil {
		t.Fatal("expected positional-arg error")
	}
}

func TestMessageSendAndInboxInjectRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	address, _, senderToken, receiverToken, cleanup := startTestServer(t, ctx, "send-inject")
	defer cleanup()

	t.Setenv("OCTOBER_BUS_ADDRESS", address)
	t.Setenv("OCTOBER_BUS_AGENT_ID", "sender")
	t.Setenv("OCTOBER_BUS_AGENT_TOKEN", senderToken)
	sent := runCapture(t, func() error {
		return runMessageSend([]string{"--to", "receiver", "--body", "ping", "--mode", "notify"})
	})
	if !strings.Contains(sent, "receiver") {
		t.Fatalf("expected receipt line naming receiver, got: %s", sent)
	}

	t.Setenv("OCTOBER_BUS_AGENT_ID", "receiver")
	t.Setenv("OCTOBER_BUS_AGENT_TOKEN", receiverToken)
	inbox := runCapture(t, func() error {
		return runInboxInject([]string{"--ack"})
	})
	if !strings.Contains(inbox, "sender") || !strings.Contains(inbox, "ping") {
		t.Fatalf("expected injected message from sender, got: %s", inbox)
	}
	if !strings.Contains(inbox, "receiver") {
		t.Fatalf("expected header to fall back to OCTOBER_BUS_AGENT_ID, got: %s", inbox)
	}
	// --ack consumed it: a second drain is empty.
	again := runCapture(t, func() error {
		return runInboxInject(nil)
	})
	if again != "" {
		t.Fatalf("expected empty second drain after --ack, got: %s", again)
	}
}

func TestMessageSendRequiresBodyAndPeer(t *testing.T) {
	if err := runMessageSend([]string{"--to", "x"}); err == nil {
		t.Fatal("expected missing-body error")
	}
	if err := runMessageSend([]string{"--body", "hi"}); err == nil {
		t.Fatal("expected missing-peer error")
	}
	if err := runMessageSend([]string{"--to", "x", "--body", "hi", "--mode", "bogus"}); err == nil {
		t.Fatal("expected unknown-mode error")
	}
}
