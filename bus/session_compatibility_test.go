package bus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type sessionTransport func(*http.Request) (*http.Response, error)

func (f sessionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSessionRejectsIncompatibleRuntimeBeforeRegistration(t *testing.T) {
	for _, health := range []Health{
		{Name: "october-bus", ProtocolVersion: ProtocolVersion, Status: "ready"},
		{Name: "october-bus", ProtocolVersion: "future", Status: "ready", Features: []string{FeatureSessionRetirement}},
	} {
		calls := 0
		client := &http.Client{Transport: sessionTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path != "/health" || r.Header.Get("Authorization") != "" {
				t.Fatal("compatibility check registered or sent credentials")
			}
			body, _ := json.Marshal(health)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		})}
		_, err := StartAgentSession(context.Background(), AgentSessionOptions{Address: "http://session.invalid", ScopeToken: "synthetic", HTTP: client})
		if AsBusError(err).Code != CodeConflict || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	}
}

func TestSessionFailedStateWritePreservesConfirmedState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &AgentSession{context: ctx, lifecycle: LifecycleReady, ready: true, leaseMS: 300000}
	session.Client = Client{Address: "http://session.invalid", HTTP: &http.Client{Transport: sessionTransport(func(*http.Request) (*http.Response, error) {
		return nil, Errorf(CodeInternal, "rejected heartbeat")
	})}}
	_, err := session.SetState(ctx, LifecycleWorking, false)
	if err == nil || session.lifecycle != LifecycleReady || !session.ready {
		t.Fatalf("failed heartbeat changed confirmed state: %v", err)
	}
}
