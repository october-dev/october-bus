package bus

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionCloseCancelsStateWriteAndHonorsCallerDeadline(t *testing.T) {
	a := setupAgents(t, ":memory:")
	defer a.runtime.Close()
	base := NewServer(a.runtime, ServerOptions{})
	working := make(chan struct{})
	retiring := make(chan struct{})
	finishRetire := make(chan struct{})
	var retireOnce, releaseOnce sync.Once
	var beats atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/me/heartbeat" && beats.Add(1) == 2 {
			_, _ = io.Copy(io.Discard, r.Body)
			close(working)
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/v1/me/retire" {
			retireOnce.Do(func() { close(retiring) })
			<-finishRetire
		}
		base.ServeHTTP(w, r)
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(finishRetire) })
	session, err := StartAgentSession(context.Background(), AgentSessionOptions{Address: server.URL, ScopeToken: a.scope.ScopeToken, Registration: RegisterAgentInput{ID: "closing", DisplayName: "Closing"}})
	requireNoError(t, err)
	stateDone := make(chan error, 1)
	go func() { _, err := session.SetState(context.Background(), LifecycleWorking, true); stateDone <- err }()
	<-working
	closed := make(chan error, 1)
	closeContext, cancelClose := context.WithCancel(context.Background())
	go func() { closed <- session.Close(closeContext) }()
	<-retiring
	if err := <-stateDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight state write not canceled: %v", err)
	}
	cancelClose()
	if err := <-closed; !errors.Is(err, context.Canceled) {
		t.Fatalf("close ignored cancellation: %v", err)
	}
	_, err = session.SetState(context.Background(), LifecycleReady, true)
	requireCode(t, err, CodeConflict)
	releaseOnce.Do(func() { close(finishRetire) })
	requireNoError(t, session.Close(context.Background()))
	if _, err := session.Client.Heartbeat(context.Background(), HeartbeatInput{Lifecycle: LifecycleReady, LeaseMS: 300000}); err == nil {
		t.Fatal("closed execution revived")
	}
}

func TestSessionContextCancellationRetiresAuthority(t *testing.T) {
	a := setupAgents(t, ":memory:")
	defer a.runtime.Close()
	server := httptest.NewServer(NewServer(a.runtime, ServerOptions{}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	session, err := StartAgentSession(ctx, AgentSessionOptions{Address: server.URL, ScopeToken: a.scope.ScopeToken, Registration: RegisterAgentInput{ID: "canceling", DisplayName: "Canceling"}})
	requireNoError(t, err)
	cancel()
	select {
	case <-session.Done():
	case <-time.After(6 * time.Second):
		t.Fatal("retirement did not finish")
	}
	requireNoError(t, session.Err())
	_, err = session.Client.Heartbeat(context.Background(), HeartbeatInput{Lifecycle: LifecycleReady, LeaseMS: 300000})
	requireCode(t, err, CodeUnauthenticated)
}
