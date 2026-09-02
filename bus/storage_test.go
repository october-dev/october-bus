package bus

import (
	"context"
	"testing"
	"time"
)

func TestStorageSummaryAndRetentionPreserveActiveObligations(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	old := time.Now().Add(-2 * time.Hour).UnixMilli()
	cutoff := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)

	oldNotify := sendTestMessage(t, ctx, agents, SendMessageInput{To: "reviewer", Body: "old terminal notify"})
	setMessageState(t, agents, oldNotify.MessageID, DeliveryAcknowledged, old, 0)
	sendTestMessage(t, ctx, agents, SendMessageInput{To: "reviewer", Body: "queued work"})

	openRequest := sendTestMessage(t, ctx, agents, SendMessageInput{To: "reviewer", Body: "reply still expected", Mode: MessageRequest})
	setMessageState(t, agents, openRequest.MessageID, DeliveryAcknowledged, old, old)

	pairedRequest := sendTestMessage(t, ctx, agents, SendMessageInput{To: "reviewer", Body: "paired request", Mode: MessageRequest})
	setMessageState(t, agents, pairedRequest.MessageID, DeliveryDelivered, 0, old)
	pairedResponse, err := agents.runtime.SendMessage(ctx, agents.reviewerToken, SendMessageInput{
		To: "planner", Body: "paired response", Mode: MessageResponse, ResponseTo: pairedRequest.MessageID,
	})
	if err != nil {
		t.Fatal(err)
	}
	setMessageState(t, agents, pairedRequest.MessageID, DeliveryAcknowledged, old, old)
	setMessageState(t, agents, pairedResponse.MessageID, DeliveryAcknowledged, old, old)

	expiredUndelivered := sendTestMessage(t, ctx, agents, SendMessageInput{To: "reviewer", Body: "expired before delivery"})
	setMessageExpired(t, agents, expiredUndelivered.MessageID, old, false)
	expiredDelivered := sendTestMessage(t, ctx, agents, SendMessageInput{To: "reviewer", Body: "late reply remains possible", Mode: MessageRequest})
	setMessageExpired(t, agents, expiredDelivered.MessageID, old, true)

	independent, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "Completed independent task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.ClaimTask(ctx, agents.plannerToken, independent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.AddTaskProgress(ctx, agents.plannerToken, independent.ID, AddTaskProgressInput{Kind: "progress", Text: "Completed"}); err != nil {
		t.Fatal(err)
	}
	independent, err = agents.runtime.CompleteTask(ctx, agents.plannerToken, independent.ID, "done")
	if err != nil {
		t.Fatal(err)
	}
	dependency := addAndCompleteTestTask(t, ctx, agents, "Completed active dependency")
	if _, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "Still active", Dependencies: []string{dependency.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.store.db.Exec(`UPDATE tasks SET updated_at=? WHERE task_id IN (?,?)`, old, independent.ID, dependency.ID); err != nil {
		t.Fatal(err)
	}

	resolved, err := agents.runtime.AskHuman(ctx, agents.plannerToken, AskHumanInput{Question: "Resolved question"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.ResolveEscalation(ctx, agents.scope.ScopeToken, resolved.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.store.db.Exec(`UPDATE escalations SET resolved_at=? WHERE escalation_id=?`, old, resolved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.AskHuman(ctx, agents.reviewerToken, AskHumanInput{Question: "Pending question"}); err != nil {
		t.Fatal(err)
	}

	summary, err := agents.runtime.StorageSummary(ctx, agents.scope.ScopeToken)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ScopeID != agents.scope.ScopeID || summary.TotalEstimatedBytes == 0 || len(summary.Records) == 0 {
		t.Fatalf("unexpected storage summary: %#v", summary)
	}

	dryRun, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	want := RetentionCounts{Messages: 4, Tasks: 1, TaskProgress: 1, Escalations: 1}
	if !dryRun.DryRun || dryRun.Records != want {
		t.Fatalf("unexpected dry run: %#v", dryRun)
	}

	result, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: cutoff, Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.DryRun || result.Records != want {
		t.Fatalf("unexpected retention result: %#v", result)
	}
	for _, id := range []string{oldNotify.MessageID, pairedRequest.MessageID, pairedResponse.MessageID, expiredUndelivered.MessageID} {
		requireRowCount(t, agents, "messages", "message_id", id, 0)
	}
	for _, id := range []string{openRequest.MessageID, expiredDelivered.MessageID} {
		requireRowCount(t, agents, "messages", "message_id", id, 1)
	}
	requireRowCount(t, agents, "tasks", "task_id", independent.ID, 0)
	requireRowCount(t, agents, "task_progress", "task_id", independent.ID, 0)
	requireRowCount(t, agents, "tasks", "task_id", dependency.ID, 1)
	requireRowCount(t, agents, "escalations", "escalation_id", resolved.ID, 0)
}

func TestRetentionRequiresScopeAuthorityAndValidCutoff(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	if _, err := agents.runtime.StorageSummary(ctx, agents.plannerToken); err == nil {
		t.Fatal("agent authority inspected scope storage")
	} else {
		requireCode(t, err, CodeUnauthenticated)
	}
	_, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: "yesterday"})
	requireCode(t, err, CodeInvalidArgument)
}

func sendTestMessage(t *testing.T, ctx context.Context, agents testAgents, input SendMessageInput) DeliveryReceipt {
	t.Helper()
	receipt, err := agents.runtime.SendMessage(ctx, agents.plannerToken, input)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func setMessageState(t *testing.T, agents testAgents, id string, state DeliveryState, acknowledgedAt, deliveredAt int64) {
	t.Helper()
	var acknowledged, delivered any
	if acknowledgedAt != 0 {
		acknowledged = acknowledgedAt
	}
	if deliveredAt != 0 {
		delivered = deliveredAt
	}
	if _, err := agents.runtime.store.db.Exec(`UPDATE messages SET state=?,acknowledged_at=?,delivered_at=? WHERE message_id=?`, state, acknowledged, delivered, id); err != nil {
		t.Fatal(err)
	}
}

func setMessageExpired(t *testing.T, agents testAgents, id string, expiresAt int64, delivered bool) {
	t.Helper()
	var deliveredAt any
	if delivered {
		deliveredAt = expiresAt - 1
	}
	if _, err := agents.runtime.store.db.Exec(`UPDATE messages SET state='expired',expires_at=?,delivered_at=? WHERE message_id=?`, expiresAt, deliveredAt, id); err != nil {
		t.Fatal(err)
	}
}

func addAndCompleteTestTask(t *testing.T, ctx context.Context, agents testAgents, title string) Task {
	t.Helper()
	task, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.ClaimTask(ctx, agents.plannerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = agents.runtime.CompleteTask(ctx, agents.plannerToken, task.ID, "done")
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func requireRowCount(t *testing.T, agents testAgents, table, column, id string, want int) {
	t.Helper()
	query := `SELECT COUNT(*) FROM ` + table + ` WHERE ` + column + `=?`
	var count int
	if err := agents.runtime.store.db.QueryRow(query, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s %s count = %d, want %d", table, id, count, want)
	}
}
