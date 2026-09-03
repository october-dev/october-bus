package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInboxSignalsWakeCurrentWaitersWithoutRetainingIdleAgents(t *testing.T) {
	signals := newRuntimeSignals()
	key := signalKey{scopeID: "scope", consumerID: "agent"}
	signals.notify(key)
	if len(signals.channels) != 0 {
		t.Fatal("notification without waiters retained an idle agent")
	}
	current, unsubscribe := signals.subscribe(key)
	defer unsubscribe()
	signals.notify(key)
	select {
	case <-current:
	default:
		t.Fatal("notification did not wake the current waiter")
	}
	if len(signals.channels) != 0 {
		t.Fatal("completed notification retained an idle agent")
	}
}

func TestRuntimeSignalsEnforceWaiterLimit(t *testing.T) {
	signals := newRuntimeSignals()
	key := signalKey{scopeID: "scope"}
	unsubscribes := make([]func(), 0, 2)
	for range 2 {
		_, unsubscribe, ok := signals.subscribeLimited(key, 2)
		if !ok {
			t.Fatal("waiter was rejected before the limit")
		}
		unsubscribes = append(unsubscribes, unsubscribe)
	}
	if _, _, ok := signals.subscribeLimited(key, 2); ok {
		t.Fatal("waiter above the limit was accepted")
	}
	for _, unsubscribe := range unsubscribes {
		unsubscribe()
	}
	if len(signals.channels) != 0 {
		t.Fatal("released waiters retained a signal")
	}
}

func TestReserveInboxWakesForDurableMessage(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	requireAgentReady(t, agents.runtime, agents.reviewerToken)

	result := make(chan *InboxReservation, 1)
	failure := make(chan error, 1)
	go func() {
		reservation, err := agents.runtime.ReserveInbox(ctx, agents.reviewerToken, 10, 1000)
		if err != nil {
			failure <- err
			return
		}
		result <- reservation
	}()

	time.Sleep(50 * time.Millisecond)
	receipt, err := agents.runtime.SendMessage(ctx, agents.plannerToken, SendMessageInput{To: "reviewer", Body: "Wake up"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failure:
		t.Fatal(err)
	case reservation := <-result:
		if reservation == nil || len(reservation.Messages) != 1 || reservation.Messages[0].ID != receipt.MessageID {
			t.Fatalf("unexpected reservation: %#v", reservation)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestInboxWaitDoesNotMissConcurrentSend(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	requireAgentReady(t, agents.runtime, agents.reviewerToken)
	for index := 0; index < 25; index++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result := make(chan *InboxReservation, 1)
		failure := make(chan error, 1)
		go func() {
			reservation, err := agents.runtime.ReserveInbox(ctx, agents.reviewerToken, 1, 500)
			if err != nil {
				failure <- err
				return
			}
			result <- reservation
		}()
		receipt, err := agents.runtime.SendMessage(ctx, agents.plannerToken, SendMessageInput{To: "reviewer", Body: "Concurrent wake"})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		select {
		case err := <-failure:
			cancel()
			t.Fatal(err)
		case reservation := <-result:
			if reservation == nil || len(reservation.Messages) != 1 || reservation.Messages[0].ID != receipt.MessageID {
				cancel()
				t.Fatalf("unexpected concurrent reservation: %#v", reservation)
			}
			messages, err := agents.runtime.CommitInbox(ctx, agents.reviewerToken, reservation.ID)
			if err != nil || len(messages) != 1 {
				cancel()
				t.Fatalf("unexpected concurrent commit: %#v, %v", messages, err)
			}
			if _, err := agents.runtime.AcknowledgeMessages(ctx, agents.reviewerToken, []string{receipt.MessageID}); err != nil {
				cancel()
				t.Fatal(err)
			}
		case <-ctx.Done():
			cancel()
			t.Fatal(ctx.Err())
		}
		cancel()
	}
}

func TestCommitInboxDoesNotWakeConcurrentWaiter(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	requireAgentReady(t, agents.runtime, agents.reviewerToken)
	if _, err := agents.runtime.SendMessage(context.Background(), agents.plannerToken, SendMessageInput{To: "reviewer", Body: "Process once"}); err != nil {
		t.Fatal(err)
	}
	reservation, err := agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, 0)
	if err != nil || reservation == nil {
		t.Fatalf("unexpected initial reservation: %#v, %v", reservation, err)
	}

	waitContext, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	waitDone := make(chan error, 1)
	go func() {
		_, err := agents.runtime.ReserveInbox(waitContext, agents.reviewerToken, 10, 2000)
		waitDone <- err
	}()
	time.Sleep(40 * time.Millisecond)
	if _, err := agents.runtime.CommitInbox(context.Background(), agents.reviewerToken, reservation.ID); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-waitDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("commit woke concurrent waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent waiter did not honor cancellation")
	}
}

func TestReserveInboxTimesOutWithoutReservation(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	started := time.Now()
	reservation, err := agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, 40)
	if err != nil || reservation != nil {
		t.Fatalf("unexpected wait result: %#v, %v", reservation, err)
	}
	if elapsed := time.Since(started); elapsed < 25*time.Millisecond || elapsed > time.Second {
		t.Fatalf("unexpected wait duration: %s", elapsed)
	}
	if len(agents.runtime.signals.channels) != 0 {
		t.Fatal("timed-out wait retained an inbox subscription")
	}
}

func TestCanceledInboxWaitDoesNotConsumeLaterMessage(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	requireAgentReady(t, agents.runtime, agents.reviewerToken)
	ctx, cancel := context.WithCancel(context.Background())
	failure := make(chan error, 1)
	go func() {
		_, err := agents.runtime.ReserveInbox(ctx, agents.reviewerToken, 10, 2000)
		failure <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-failure:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("inbox wait did not stop after cancellation")
	}
	if len(agents.runtime.signals.channels) != 0 {
		t.Fatal("canceled wait retained an inbox subscription")
	}

	receipt, err := agents.runtime.SendMessage(context.Background(), agents.plannerToken, SendMessageInput{To: "reviewer", Body: "Still available"})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, 0)
	if err != nil || reservation == nil || len(reservation.Messages) != 1 || reservation.Messages[0].ID != receipt.MessageID {
		t.Fatalf("canceled wait consumed work: %#v, %v", reservation, err)
	}
}

func TestInboxWaitStopsWhenExecutionIsReplaced(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	failure := make(chan error, 1)
	go func() {
		_, err := agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, 2000)
		failure <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := agents.runtime.RegisterAgent(context.Background(), agents.scope.ScopeToken, RegisterAgentInput{
		ID: "reviewer", DisplayName: "Replacement", LeaseMS: 30000,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failure:
		requireCode(t, err, CodeUnauthenticated)
	case <-time.After(time.Second):
		t.Fatal("inbox wait did not stop after execution replacement")
	}
}

func TestInboxWaitRechecksAtLeaseExpiry(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	expiresAt := nowMillis() + 60
	if _, err := sqliteStore(t, agents.runtime).db.Exec(`UPDATE agents SET lease_expires_at=? WHERE scope_id=? AND agent_id=?`, expiresAt, agents.scope.ScopeID, agents.reviewer.AgentID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, 2000)
	requireCode(t, err, CodeUnauthenticated)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lease expiry took too long to stop wait: %s", elapsed)
	}
}

func TestInboxWaitRechecksWhenReservationExpires(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	requireAgentReady(t, agents.runtime, agents.reviewerToken)
	receipt, err := agents.runtime.SendMessage(context.Background(), agents.plannerToken, SendMessageInput{To: "reviewer", Body: "Redeliver"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, 0)
	if err != nil || first == nil {
		t.Fatalf("unexpected initial reservation: %#v, %v", first, err)
	}
	if _, err := sqliteStore(t, agents.runtime).db.Exec(`UPDATE reservations SET expires_at=? WHERE reservation_id=?`, nowMillis()+60, first.ID); err != nil {
		t.Fatal(err)
	}
	redelivery, err := agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, 2000)
	if err != nil || redelivery == nil || len(redelivery.Messages) != 1 || redelivery.Messages[0].ID != receipt.MessageID {
		t.Fatalf("expired reservation did not wake delivery: %#v, %v", redelivery, err)
	}
}

func TestInboxWaitBoundsAreValidated(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	_, err := agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, -1)
	requireCode(t, err, CodeInvalidArgument)
	_, err = agents.runtime.ReserveInbox(context.Background(), agents.reviewerToken, 10, maxInboxWaitMS+1)
	requireCode(t, err, CodeInvalidArgument)
}

func TestServerStopCancelsInboxWait(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	server := NewServer(agents.runtime, ServerOptions{AdminToken: "inbox-wait-admin"})
	address, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan struct {
		reservation *InboxReservation
		err         error
	}, 1)
	go func() {
		reservation, err := (Client{Address: address, Token: agents.reviewerToken}).ReserveInbox(context.Background(), 10, 25*time.Second)
		waitDone <- struct {
			reservation *InboxReservation
			err         error
		}{reservation: reservation, err: err}
	}()
	time.Sleep(50 * time.Millisecond)
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waitDone:
		if result.err != nil || result.reservation != nil {
			t.Fatalf("stopped server returned an unexpected inbox result: %#v, %v", result.reservation, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("server stop did not cancel inbox wait")
	}
}

// waitForSignalWaiter polls runtime.signals until the given per-agent key has
// exactly one active waiter, or the deadline elapses.
func waitForSignalWaiter(t *testing.T, runtime *Runtime, key signalKey, deadline time.Time) {
	t.Helper()
	for {
		runtime.signals.mu.Lock()
		signal := runtime.signals.channels[key]
		waiting := signal != nil && signal.waiters == 1
		runtime.signals.mu.Unlock()
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiter for %+v did not subscribe within deadline", key)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestReserveInboxGatesOnReadiness verifies the readiness invariant: a
// reservation is only admitted once the principal is ready=true, a false->true
// heartbeat wakes the blocked waiter immediately (not after its waitMs
// budget), and no reservation is created while the host is not ready. This
// fails ungated code at the second barrier (it delivers before ready) and fails
// gated-but-no-ready-wake code at the post-heartbeat 200ms bound.
func TestReserveInboxGatesOnReadiness(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()

	gated, err := agents.runtime.RegisterAgent(ctx, agents.scope.ScopeToken, RegisterAgentInput{
		ID: "gated-reviewer", DisplayName: "Gated Reviewer", ConnectTo: []string{"planner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// gated-reviewer is intentionally never heartbeated: its persisted ready
	// stays false, so no reservation may be admitted on its behalf.

	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome := make(chan struct {
		reservation *InboxReservation
		err         error
	}, 1)
	go func() {
		reservation, err := agents.runtime.ReserveInbox(waitContext, gated.AgentToken, 10, 2000)
		outcome <- struct {
			reservation *InboxReservation
			err         error
		}{reservation: reservation, err: err}
	}()

	key := signalKey{scopeID: agents.scope.ScopeID, consumerID: "gated-reviewer"}
	barrier := time.Now().Add(500 * time.Millisecond)
	waitForSignalWaiter(t, agents.runtime, key, barrier)
	select {
	case result := <-outcome:
		t.Fatalf("not-ready reservation returned before any message: %#v, %v", result.reservation, result.err)
	default:
	}

	// Enqueue while still not ready. Ungated code would be resumed by this
	// enqueue and deliver before readiness; gated code must re-subscribe and
	// keep waiting.
	receipt, err := agents.runtime.SendMessage(ctx, agents.plannerToken, SendMessageInput{To: "gated-reviewer", Body: "Queued before ready"})
	if err != nil {
		t.Fatal(err)
	}
	barrier = time.Now().Add(500 * time.Millisecond)
	waitForSignalWaiter(t, agents.runtime, key, barrier)
	select {
	case result := <-outcome:
		t.Fatalf("pre-ready delivery by ungated code: %#v, %v", result.reservation, result.err)
	default:
	}

	// Now signal readiness. The blocked waiter must wake and reserve promptly,
	// well below its 2000ms wait budget.
	if _, err := agents.runtime.Heartbeat(ctx, gated.AgentToken, HeartbeatInput{
		Lifecycle: LifecycleReady, Ready: true, LeaseMS: 30000,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-outcome:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.reservation == nil || len(result.reservation.Messages) != 1 || result.reservation.Messages[0].ID != receipt.MessageID {
			t.Fatalf("ready reservation did not return the exact queued message: %#v", result.reservation)
		}
		messages, err := agents.runtime.CommitInbox(ctx, gated.AgentToken, result.reservation.ID)
		if err != nil || len(messages) != 1 || messages[0].ID != receipt.MessageID {
			t.Fatalf("ready reservation did not commit the exact message: %#v, %v", messages, err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ready heartbeat did not wake the blocked reservation within 200ms (no ready-edge wake)")
	}

	agents.runtime.signals.mu.Lock()
	signal := agents.runtime.signals.channels[key]
	agents.runtime.signals.mu.Unlock()
	if signal != nil {
		t.Fatal("completed reservation retained an inbox subscription")
	}
}
