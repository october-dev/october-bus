package bus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func offlineAgents(t *testing.T, agents testAgents) {
	t.Helper()
	ctx := context.Background()
	for _, token := range []string{agents.plannerToken, agents.reviewerToken} {
		if _, err := agents.runtime.Heartbeat(ctx, token, HeartbeatInput{Lifecycle: LifecycleOffline, Ready: false}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPortableScopeArchiveRoundTripAndRetry(t *testing.T) {
	ctx := context.Background()
	source := setupAgents(t, ":memory:")
	defer source.runtime.Close()

	request, err := source.runtime.SendMessage(ctx, source.plannerToken, SendMessageInput{
		To: "reviewer", Mode: MessageRequest, Body: "Review this", IdempotencyKey: "review-archive",
		Context: []ContextItem{{Kind: "text", Title: "Patch", Text: "bounded context"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := source.runtime.ReserveInbox(ctx, source.reviewerToken, 10, 0)
	if err != nil || reservation == nil {
		t.Fatalf("unexpected reservation: %#v, %v", reservation, err)
	}
	if _, err := source.runtime.CommitInbox(ctx, source.reviewerToken, reservation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.runtime.AcknowledgeMessages(ctx, source.reviewerToken, []string{request.MessageID}); err != nil {
		t.Fatal(err)
	}
	reply, err := source.runtime.SendMessage(ctx, source.reviewerToken, SendMessageInput{To: "planner", Mode: MessageResponse, ResponseTo: request.MessageID, Body: "Looks good"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := source.runtime.AddTask(ctx, source.plannerToken, AddTaskInput{Title: "Review archive", Description: "Check restored state"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.runtime.ClaimTask(ctx, source.reviewerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.runtime.AddTaskProgress(ctx, source.reviewerToken, task.ID, AddTaskProgressInput{Kind: "progress", Text: "Reviewed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := source.runtime.CompleteTask(ctx, source.reviewerToken, task.ID, "Complete"); err != nil {
		t.Fatal(err)
	}
	escalation, err := source.runtime.AskHuman(ctx, source.plannerToken, AskHumanInput{Question: "Ship?", Options: []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.runtime.ResolveEscalation(ctx, source.scope.ScopeToken, escalation.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	publication, err := source.runtime.CreateAgentCardPublication(ctx, source.scope.ScopeToken, PublishAgentCardInput{AgentID: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	a2aPrincipal, err := source.runtime.CreateA2APrincipal(ctx, source.scope.ScopeToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Remote caller"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := source.runtime.CreateOutputStream(ctx, source.scope.ScopeToken, CreateOutputStreamInput{Name: "preview", RetentionLimit: 10, PublisherAgentIDs: []string{"reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.runtime.PublishOutput(ctx, source.reviewerToken, stream.ID, PublishOutputInput{ContentType: OutputJSON, Value: map[string]any{"status": "ready"}}); err != nil {
		t.Fatal(err)
	}
	outputPrincipal, err := source.runtime.CreateOutputPrincipal(ctx, source.scope.ScopeToken, CreateOutputPrincipalInput{StreamID: stream.ID, Label: "Viewer", Permissions: []OutputPermission{OutputRead}})
	if err != nil {
		t.Fatal(err)
	}
	pendingMessage, err := source.runtime.SendMessage(ctx, source.plannerToken, SendMessageInput{To: "reviewer", Body: "Reserved during export"})
	if err != nil {
		t.Fatal(err)
	}
	pendingReservation, err := source.runtime.ReserveInbox(ctx, source.reviewerToken, 10, 0)
	if err != nil || pendingReservation == nil {
		t.Fatalf("unexpected pending reservation: %#v, %v", pendingReservation, err)
	}
	pendingTask, err := source.runtime.AddTask(ctx, source.plannerToken, AddTaskInput{Title: "Continue after restore"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.runtime.ClaimTask(ctx, source.reviewerToken, pendingTask.ID); err != nil {
		t.Fatal(err)
	}
	a2aTask, err := source.runtime.AcceptA2AMessage(ctx, a2aPrincipal.Credential, publication.ID, AcceptA2AMessageInput{ClientMessageID: "portable-turn", Body: "Review after restore"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := source.runtime.ExportScope(ctx, source.scope.ScopeID); err == nil {
		t.Fatal("active scope was exported")
	} else {
		requireCode(t, err, CodeConflict)
	}
	offlineAgents(t, source)
	archive, err := source.runtime.ExportScope(ctx, source.scope.ScopeID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{source.scope.ScopeToken, source.plannerToken, source.reviewerToken, a2aPrincipal.Credential, a2aPrincipal.Principal.ID, outputPrincipal.Credential, outputPrincipal.Principal.ID, source.planner.ExecutionID, source.reviewer.ExecutionID} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("archive exposed credential or execution authority %q", secret)
		}
	}
	archivedMessages := map[string]ArchivedMessage{}
	for _, message := range archive.Messages {
		archivedMessages[message.ID] = message
	}
	if len(archive.Messages) != 4 || archivedMessages[request.MessageID].ResponseMessageID != reply.MessageID || archivedMessages[reply.MessageID].ResponseTo != request.MessageID {
		t.Fatalf("messages were not preserved: %#v", archive.Messages)
	}
	if archivedMessages[pendingMessage.MessageID].State != DeliveryQueued {
		t.Fatalf("active reservation was not made portable: %#v", archivedMessages[pendingMessage.MessageID])
	}
	archivedTasks := map[string]ArchivedTask{}
	for _, archivedTask := range archive.Tasks {
		archivedTasks[archivedTask.ID] = archivedTask
	}
	if len(archive.Tasks) != 2 || archivedTasks[pendingTask.ID].Status != "open" || archivedTasks[pendingTask.ID].ClaimedBy != "" {
		t.Fatalf("active task claim was not made portable: %#v", archive.Tasks)
	}
	if len(archive.AgentCardPublications) != 1 || len(archive.A2ATasks) != 1 || archive.A2ATasks[0].ID != a2aTask.ID || len(archive.A2AMessages) != 1 || len(archive.OutputStreams) != 1 || len(archive.OutputValues) != 1 {
		t.Fatalf("configuration and output history were not preserved: %#v", archive)
	}

	destination, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	imported, err := destination.ImportScope(ctx, archive)
	if err != nil || !imported.Imported || imported.ScopeToken == "" || imported.ScopeID != source.scope.ScopeID {
		t.Fatalf("unexpected import result: %#v, %v", imported, err)
	}
	retry, err := destination.ImportScope(ctx, archive)
	if err != nil || retry.Imported || retry.ScopeID != imported.ScopeID || retry.ScopeToken != "" {
		t.Fatalf("archive retry was not idempotent: %#v, %v", retry, err)
	}
	agents, err := destination.ListAgents(ctx, imported.ScopeToken)
	if err != nil || len(agents) != 2 || agents[0].Lifecycle != LifecycleOffline || agents[0].Reachable {
		t.Fatalf("agents were not restored offline: %#v, %v", agents, err)
	}
	if _, err := destination.ListAgents(ctx, source.scope.ScopeToken); err == nil {
		t.Fatal("source scope token worked after import")
	}
	events, err := destination.Events(ctx, imported.ScopeToken, 0, 10, 0)
	if err != nil || len(events.Events) != 1 || events.Events[0].Type != "scope.imported" {
		t.Fatalf("import did not start a fresh event history: %#v, %v", events, err)
	}
	registered, err := destination.RegisterAgent(ctx, imported.ScopeToken, RegisterAgentInput{ID: "planner", DisplayName: "Planner"})
	if err != nil {
		t.Fatal(err)
	}
	if registered.ExecutionID == source.planner.ExecutionID {
		t.Fatal("source execution authority survived import")
	}
	var restoredA2ATasks, restoredA2AMessages int
	if err := sqliteStore(t, destination).db.QueryRow(`SELECT COUNT(*) FROM a2a_tasks WHERE scope_id=?`, imported.ScopeID).Scan(&restoredA2ATasks); err != nil {
		t.Fatal(err)
	}
	if err := sqliteStore(t, destination).db.QueryRow(`SELECT COUNT(*) FROM a2a_message_correlations`).Scan(&restoredA2AMessages); err != nil {
		t.Fatal(err)
	}
	if restoredA2ATasks != 1 || restoredA2AMessages != 1 {
		t.Fatalf("A2A correlations were not restored: tasks=%d messages=%d", restoredA2ATasks, restoredA2AMessages)
	}
	receipt, err := destination.Receipt(ctx, registered.AgentToken, request.MessageID)
	if err != nil || receipt.State != DeliveryAcknowledged || receipt.ResponseMessageID != reply.MessageID {
		t.Fatalf("message state was not restored: %#v, %v", receipt, err)
	}
	tasks, err := destination.ListTasks(ctx, imported.ScopeToken, false)
	restoredTasks := map[string]Task{}
	for _, restoredTask := range tasks {
		restoredTasks[restoredTask.ID] = restoredTask
	}
	restoredDone := restoredTasks[task.ID]
	if err != nil || len(tasks) != 2 || restoredDone.Status != "done" || len(restoredDone.RecentProgress) != 1 || restoredDone.RecentProgress[0].ExecutionID != "imported" || restoredTasks[pendingTask.ID].Status != "open" {
		t.Fatalf("task state was not restored: %#v, %v", tasks, err)
	}
	pendingReceipt, err := destination.Receipt(ctx, registered.AgentToken, pendingMessage.MessageID)
	if err != nil || pendingReceipt.State != DeliveryQueued {
		t.Fatalf("reserved message was not restored as queued: %#v, %v", pendingReceipt, err)
	}
	escalations, err := destination.ListEscalations(ctx, imported.ScopeToken)
	if err != nil || len(escalations) != 1 || escalations[0].Answer != "yes" {
		t.Fatalf("escalation was not restored: %#v, %v", escalations, err)
	}
	publications, err := destination.ListAgentCardPublications(ctx, imported.ScopeToken)
	if err != nil || len(publications) != 1 || publications[0].Enabled {
		t.Fatalf("Agent Card was not restored disabled: %#v, %v", publications, err)
	}
	latest, err := destination.LatestOutput(ctx, imported.ScopeToken, stream.ID)
	if err != nil || latest == nil || latest.Sequence != 1 {
		t.Fatalf("output state was not restored: %#v, %v", latest, err)
	}
	if _, err := destination.LatestOutput(ctx, outputPrincipal.Credential, stream.ID); err == nil {
		t.Fatal("source scoped credential worked after import")
	}
	if _, err := destination.PruneScope(ctx, imported.ScopeToken, PruneScopeInput{Before: "2100-01-01T00:00:00Z", Execute: true}); err != nil {
		t.Fatal(err)
	}
	retryAfterRetention, err := destination.ImportScope(ctx, archive)
	if err != nil || retryAfterRetention.Imported || retryAfterRetention.ScopeID != imported.ScopeID {
		t.Fatalf("archive retry lost idempotency after retention: %#v, %v", retryAfterRetention, err)
	}
}

func TestPortableScopeArchiveRejectsMalformedInputAtomically(t *testing.T) {
	ctx := context.Background()
	source := setupAgents(t, ":memory:")
	defer source.runtime.Close()
	offlineAgents(t, source)
	archive, err := source.runtime.ExportScope(ctx, source.scope.ScopeID)
	if err != nil {
		t.Fatal(err)
	}
	archive.Scope.ID = "broken"
	archive.Messages = append(archive.Messages, ArchivedMessage{ID: "msg_bad", From: "planner", To: "missing", Mode: MessageNotify, Body: "bad", Context: []ContextItem{}, State: DeliveryQueued, CreatedAt: archive.ExportedAt})
	destination, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	_, err = destination.ImportScope(ctx, archive)
	requireCode(t, err, CodeInvalidArgument)
	var count int
	if err := sqliteStore(t, destination).db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scopes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("malformed import applied partial state: %d, %v", count, err)
	}
	archive.Version = ScopeArchiveVersion + 1
	_, err = destination.ImportScope(ctx, archive)
	requireCode(t, err, CodeInvalidArgument)
}
