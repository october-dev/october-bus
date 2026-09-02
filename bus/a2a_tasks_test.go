package bus

import (
	"context"
	"path/filepath"
	"testing"
)

func setupA2APrincipal(t *testing.T, agents testAgents) (agentCardPublication, IssuedA2APrincipal) {
	t.Helper()
	ctx := context.Background()
	publication, err := agents.runtime.CreateAgentCardPublication(ctx, agents.scope.ScopeToken, PublishAgentCardInput{AgentID: agents.reviewer.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := agents.runtime.CreateA2APrincipal(ctx, agents.scope.ScopeToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Remote caller"})
	if err != nil {
		t.Fatal(err)
	}
	return publication, principal
}

func TestA2ATaskCorrelationIsDurableAndIdempotent(t *testing.T) {
	database := filepath.Join(t.TempDir(), "bus.db")
	agents := setupAgents(t, database)
	ctx := context.Background()
	publication, issued := setupA2APrincipal(t, agents)
	input := AcceptA2AMessageInput{ClientMessageID: "remote-message-1", Body: "Review this change"}
	task, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" || task.ContextID == "" || task.State != A2ATaskSubmitted || task.PrincipalID != issued.Principal.ID || len(task.Messages) != 1 {
		t.Fatalf("unexpected A2A task: %#v", task)
	}
	correlation := task.Messages[0]
	if correlation.ClientMessageID != input.ClientMessageID || correlation.BusRequestMessageID == "" || correlation.BusResponseMessageID != "" {
		t.Fatalf("unexpected message correlation: %#v", correlation)
	}
	retry, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, input)
	if err != nil || retry.ID != task.ID || retry.Messages[0].BusRequestMessageID != correlation.BusRequestMessageID {
		t.Fatalf("idempotent retry created different work: %#v, %v", retry, err)
	}
	input.Body = "Review something else"
	_, err = agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, input)
	requireCode(t, err, CodeConflict)
	if err := agents.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	stored, err := restarted.A2ATask(ctx, issued.Credential, publication.ID, task.ID)
	if err != nil || stored.ID != task.ID || len(stored.Messages) != 1 || stored.Messages[0].BusRequestMessageID != correlation.BusRequestMessageID {
		t.Fatalf("correlation did not survive restart: %#v, %v", stored, err)
	}
}

func TestA2ATaskTracksBusDeliveryAndResponse(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, issued := setupA2APrincipal(t, agents)
	task, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, AcceptA2AMessageInput{ClientMessageID: "remote-message-1", Body: "Review this change"})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := agents.runtime.ReserveInbox(ctx, agents.reviewerToken, 10, 0)
	if err != nil || reservation == nil || len(reservation.Messages) != 1 {
		t.Fatalf("unexpected inbox: %#v, %v", reservation, err)
	}
	inbound := reservation.Messages[0]
	if inbound.FromKind != MessageParticipantA2APrincipal || inbound.From != issued.Principal.ID || inbound.ToKind != MessageParticipantAgent {
		t.Fatalf("unexpected A2A Bus message: %#v", inbound)
	}
	if _, err := agents.runtime.CommitInbox(ctx, agents.reviewerToken, reservation.ID); err != nil {
		t.Fatal(err)
	}
	working, err := agents.runtime.A2ATask(ctx, issued.Credential, publication.ID, task.ID)
	if err != nil || working.State != A2ATaskWorking {
		t.Fatalf("delivery did not advance task: %#v, %v", working, err)
	}
	reply, err := agents.runtime.SendMessage(ctx, agents.reviewerToken, SendMessageInput{
		To: issued.Principal.ID, Mode: MessageResponse, ResponseTo: inbound.ID, Body: "Found one issue", IdempotencyKey: "review-reply",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply.State != DeliveryAcknowledged {
		t.Fatalf("A2A response was not committed durably: %#v", reply)
	}
	completed, err := agents.runtime.A2ATask(ctx, issued.Credential, publication.ID, task.ID)
	if err != nil || completed.State != A2ATaskCompleted || completed.Messages[0].BusResponseMessageID != reply.MessageID {
		t.Fatalf("response did not complete task: %#v, %v", completed, err)
	}
}

func TestA2ATaskAllowsFollowUpOnlyBeforeTerminalState(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, issued := setupA2APrincipal(t, agents)
	task, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, AcceptA2AMessageInput{ClientMessageID: "turn-1", Body: "Start"})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := agents.runtime.AuthenticateA2APrincipal(ctx, issued.Credential, publication.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.store.SetA2ATaskState(ctx, principal, task.ID, A2ATaskInputRequired); err != nil {
		t.Fatal(err)
	}
	followUp, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, AcceptA2AMessageInput{
		TaskID: task.ID, ContextID: task.ContextID, ClientMessageID: "turn-2", Body: "Here is the answer",
	})
	if err != nil || followUp.State != A2ATaskWorking || len(followUp.Messages) != 2 {
		t.Fatalf("follow-up was not correlated: %#v, %v", followUp, err)
	}
	if _, err := agents.runtime.store.SetA2ATaskState(ctx, principal, task.ID, A2ATaskCanceled); err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, AcceptA2AMessageInput{
		TaskID: task.ID, ClientMessageID: "turn-3", Body: "Too late",
	})
	requireCode(t, err, CodeConflict)
}

func TestA2ATaskIsIsolatedByPrincipal(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, owner := setupA2APrincipal(t, agents)
	other, err := agents.runtime.CreateA2APrincipal(ctx, agents.scope.ScopeToken, CreateA2APrincipalInput{PublicationID: publication.ID, Label: "Other caller"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := agents.runtime.AcceptA2AMessage(ctx, owner.Credential, publication.ID, AcceptA2AMessageInput{ClientMessageID: "private-turn", Body: "Private work"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.A2ATask(ctx, other.Credential, publication.ID, task.ID)
	requireCode(t, err, CodeNotFound)
	otherPrincipal, err := agents.runtime.AuthenticateA2APrincipal(ctx, other.Credential, publication.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.store.SetA2ATaskState(ctx, otherPrincipal, task.ID, A2ATaskCanceled)
	requireCode(t, err, CodeNotFound)
}
