package bus

import (
	"context"
	"testing"
	"time"
)

func TestA2AMultiTurnRetentionPreservesUnfinishedUnitAndRestores(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, issued := setupA2APrincipal(t, agents)
	input := AcceptA2AMessageInput{ClientMessageID: "first-turn", Body: "review"}
	task, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, input)
	requireNoError(t, err)
	principal, err := agents.runtime.AuthenticateA2APrincipal(ctx, issued.Credential, publication.ID)
	requireNoError(t, err)
	finishTurn := func(requestID string) {
		t.Helper()
		reservation, err := agents.runtime.ReserveInbox(ctx, agents.reviewerToken, 10, 0)
		require(t, err == nil && reservation != nil, "reserve: %v", err)
		if _, err := agents.runtime.CommitInbox(ctx, agents.reviewerToken, reservation.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := agents.runtime.SendMessage(ctx, agents.reviewerToken, SendMessageInput{To: issued.Principal.ID, Mode: MessageResponse, ResponseTo: requestID, Body: "reply"}); err != nil {
			t.Fatal(err)
		}
		if _, err := agents.runtime.AcknowledgeMessages(ctx, agents.reviewerToken, []string{requestID}); err != nil {
			t.Fatal(err)
		}
	}
	// A reply while input is required does not complete the remote task.
	if _, err := sqliteStore(t, agents.runtime).SetA2ATaskState(ctx, principal, task.ID, A2ATaskInputRequired); err != nil {
		t.Fatal(err)
	}
	finishTurn(task.Messages[0].BusRequestMessageID)
	prune := PruneScopeInput{Before: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), Execute: true}
	result, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, prune)
	require(t, err == nil && result.Records.Messages == 0 && result.Records.A2ATasks == 0 && result.Records.A2AMessages == 0, "unfinished task lost correlation history: %+v, %v", result, err)
	if retried, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, input); err != nil || retried.ID != task.ID {
		t.Fatalf("unfinished task lost its retry binding: %+v, %v", retried, err)
	}
	task, err = agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, AcceptA2AMessageInput{ClientMessageID: "second-turn", TaskID: task.ID, Body: "more input"})
	requireNoError(t, err)
	for _, message := range task.Messages {
		if message.ClientMessageID == "second-turn" {
			finishTurn(message.BusRequestMessageID)
		}
	}
	prune.Execute = false
	dry, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, prune)
	require(t, err == nil && dry.Records.Messages == 4 && dry.Records.A2ATasks == 1 && dry.Records.A2AMessages == 2, "terminal multi-turn dry run: %+v, %v", dry, err)
	prune.Execute = true
	result, err = agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, prune)
	require(t, err == nil && result.Records == dry.Records, "execution differs from dry run: %+v, %v", result, err)
	offlineAgents(t, agents)
	archive, err := agents.runtime.ExportScope(ctx, agents.scope.ScopeID)
	requireNoError(t, err)
	restored, err := Open(":memory:")
	requireNoError(t, err)
	defer restored.Close()
	if _, err := restored.ImportScope(ctx, archive); err != nil {
		t.Fatalf("post-retention archive does not restore: %v", err)
	}
}

func TestA2AExpiredUndeliveredRequestPrunesWithFailedTask(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, issued := setupA2APrincipal(t, agents)
	task, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, AcceptA2AMessageInput{ClientMessageID: "expire", Body: "review"})
	requireNoError(t, err)
	// Move expiry into the past without waiting for the default TTL.
	if _, err := sqliteStore(t, agents.runtime).db.ExecContext(ctx, "UPDATE messages SET expires_at=? WHERE message_id=?", time.Now().Add(-time.Minute).UnixMilli(), task.Messages[0].BusRequestMessageID); err != nil {
		t.Fatal(err)
	}
	result, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), Execute: true})
	require(t, err == nil && result.Records.Messages == 1 && result.Records.A2ATasks == 1 && result.Records.A2AMessages == 1, "expired work did not prune atomically: %+v, %v", result, err)
}
