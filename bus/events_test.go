package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestScopeEventsAreOrderedAndDoNotContainRecordBodies(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()

	message, err := agents.runtime.SendMessage(ctx, agents.plannerToken, SendMessageInput{
		To: "reviewer", Mode: MessageRequest, Body: "private message body", Context: []ContextItem{{Kind: "text", Title: "private context", Text: "secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "private task title", Description: "private task description"})
	if err != nil {
		t.Fatal(err)
	}
	escalation, err := agents.runtime.AskHuman(ctx, agents.reviewerToken, AskHumanInput{Question: "private question"})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := agents.runtime.ReserveInbox(ctx, agents.reviewerToken, 10, 0)
	if err != nil || reservation == nil {
		t.Fatalf("unexpected reservation: %#v, %v", reservation, err)
	}
	if err := agents.runtime.ReleaseInbox(ctx, agents.reviewerToken, reservation.ID); err != nil {
		t.Fatal(err)
	}
	reservation, err = agents.runtime.ReserveInbox(ctx, agents.reviewerToken, 10, 0)
	if err != nil || reservation == nil {
		t.Fatalf("unexpected second reservation: %#v, %v", reservation, err)
	}
	if _, err := agents.runtime.CommitInbox(ctx, agents.reviewerToken, reservation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.AcknowledgeMessages(ctx, agents.reviewerToken, []string{message.MessageID}); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.SendMessage(ctx, agents.reviewerToken, SendMessageInput{
		To: "planner", Mode: MessageResponse, ResponseTo: message.MessageID, Body: "private reply",
	}); err != nil {
		t.Fatal(err)
	}
	expiring, err := agents.runtime.SendMessage(ctx, agents.plannerToken, SendMessageInput{To: "reviewer", Body: "private expiring message", ExpiresInMS: 20})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := agents.runtime.Receipt(ctx, agents.plannerToken, expiring.MessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.ClaimTask(ctx, agents.plannerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.AddTaskProgress(ctx, agents.plannerToken, task.ID, AddTaskProgressInput{Kind: "progress", Text: "private progress"}); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.ReleaseTask(ctx, agents.plannerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.ClaimTask(ctx, agents.plannerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.CompleteTask(ctx, agents.plannerToken, task.ID, "private completion"); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.ResolveEscalation(ctx, agents.scope.ScopeToken, escalation.ID, "private answer"); err != nil {
		t.Fatal(err)
	}

	batch, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if batch.ResyncRequired || len(batch.Events) < 6 || batch.NextRevision != batch.CurrentRevision {
		t.Fatalf("unexpected event batch: %#v", batch)
	}
	for index, event := range batch.Events {
		if event.Revision != int64(index+1) || event.ScopeID != agents.scope.ScopeID || event.ID == "" || event.CreatedAt == "" {
			t.Fatalf("invalid event at %d: %#v", index, event)
		}
		encoded := event.Type + event.SubjectID
		for key, value := range event.Attributes {
			encoded += key + value
		}
		for _, private := range []string{"private message body", "private reply", "private expiring message", "private context", "secret", "private task title", "private task description", "private question", "private progress", "private completion", "private answer"} {
			if strings.Contains(encoded, private) {
				t.Fatalf("event exposed record content %q: %#v", private, event)
			}
		}
	}
	subjects := map[string]bool{}
	for _, event := range batch.Events {
		subjects[event.SubjectID] = true
	}
	for _, subject := range []string{message.MessageID, task.ID, escalation.ID} {
		if !subjects[subject] {
			t.Fatalf("event batch omitted subject %s", subject)
		}
	}
	types := map[string]bool{}
	for _, event := range batch.Events {
		types[event.Type] = true
	}
	for _, eventType := range []string{
		"agent.registered", "link.created", "message.accepted", "message.replied", "message.reserved", "message.released",
		"message.delivered", "message.acknowledged", "message.expired", "task.created", "task.claimed", "task.progress_added",
		"task.released", "task.completed", "escalation.created", "escalation.resolved",
	} {
		if !types[eventType] {
			t.Fatalf("event batch omitted type %s", eventType)
		}
	}

	resumed, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, batch.NextRevision, 100, 0)
	if err != nil || len(resumed.Events) != 0 || resumed.NextRevision != batch.NextRevision {
		t.Fatalf("unexpected resumed batch: %#v, %v", resumed, err)
	}
	if _, err := agents.runtime.Events(ctx, agents.plannerToken, 0, 50, 0); err == nil {
		t.Fatal("agent token read the scope event stream")
	} else {
		requireCode(t, err, CodeUnauthenticated)
	}
}

func TestScopeEventWaitWakesAndCanBeCanceled(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	current, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan EventBatch, 1)
	failure := make(chan error, 1)
	go func() {
		batch, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, current.NextRevision, 100, time.Second)
		if err != nil {
			failure <- err
			return
		}
		result <- batch
	}()
	time.Sleep(40 * time.Millisecond)
	if _, err := agents.runtime.Heartbeat(ctx, agents.plannerToken, HeartbeatInput{Lifecycle: LifecycleWorking, Ready: true, LeaseMS: 30000}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failure:
		t.Fatal(err)
	case batch := <-result:
		if len(batch.Events) != 1 || batch.Events[0].Type != "agent.lifecycle_changed" {
			t.Fatalf("unexpected wake batch: %#v", batch)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	canceled, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := agents.runtime.Events(canceled, agents.scope.ScopeToken, current.NextRevision+1, 100, time.Second)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestIdempotentRetryDoesNotAppendAnotherEvent(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	input := SendMessageInput{To: "reviewer", Body: "retry once", IdempotencyKey: "event-retry"}
	first, err := agents.runtime.SendMessage(ctx, agents.plannerToken, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := agents.runtime.SendMessage(ctx, agents.plannerToken, input)
	if err != nil || retry.MessageID != first.MessageID {
		t.Fatalf("unexpected retry: %#v, %v", retry, err)
	}
	batch, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, event := range batch.Events {
		if event.Type == "message.accepted" && event.SubjectID == first.MessageID {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("idempotent send appended %d accepted events", accepted)
	}
}

func TestPrunedEventCursorRequiresResync(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	old := time.Now().Add(-2 * time.Hour).UnixMilli()
	cutoff := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := agents.runtime.store.db.Exec(`UPDATE events SET created_at=? WHERE scope_id=?`, old, agents.scope.ScopeID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.Heartbeat(ctx, agents.plannerToken, HeartbeatInput{Lifecycle: LifecycleReady, Ready: true, LeaseMS: 30000}); err != nil {
		t.Fatal(err)
	}
	pruned, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: cutoff, Execute: true})
	if err != nil || pruned.Records.Events == 0 {
		t.Fatalf("unexpected event retention: %#v, %v", pruned, err)
	}
	stale, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, 0, 100, 0)
	if err != nil || !stale.ResyncRequired || len(stale.Events) != 0 || stale.MinimumCursor == 0 {
		t.Fatalf("stale cursor did not require resync: %#v, %v", stale, err)
	}
	resumed, err := agents.runtime.Events(ctx, agents.scope.ScopeToken, stale.MinimumCursor, 100, 0)
	if err != nil || resumed.ResyncRequired || len(resumed.Events) != 1 || resumed.Events[0].Type != "agent.lifecycle_changed" {
		t.Fatalf("unexpected retained events: %#v, %v", resumed, err)
	}
	_, err = agents.runtime.Events(ctx, agents.scope.ScopeToken, resumed.CurrentRevision+1, 100, 0)
	requireCode(t, err, CodeInvalidArgument)
}
