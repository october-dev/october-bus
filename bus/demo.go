package bus

import (
	"context"
	"fmt"
)

func RunDemo(ctx context.Context) error {
	runtimeValue, err := Open(":memory:")
	if err != nil {
		return err
	}
	adminToken, err := randomValue(32)
	if err != nil {
		runtimeValue.Close()
		return err
	}
	server := NewServer(runtimeValue, ServerOptions{AdminToken: adminToken})
	address, err := server.Start()
	if err != nil {
		runtimeValue.Close()
		return err
	}
	defer server.Stop(context.Background())
	admin := Client{Address: address, Token: adminToken}
	scope, err := admin.CreateScope(ctx, CreateScopeInput{ID: "demo"})
	if err != nil {
		return err
	}
	scopeClient := Client{Address: address, Token: scope.ScopeToken}
	plannerRegistration, err := scopeClient.RegisterAgent(ctx, RegisterAgentInput{ID: "planner", DisplayName: "Planner", Capabilities: []AgentCapability{{Name: "planning"}}})
	if err != nil {
		return err
	}
	reviewerRegistration, err := scopeClient.RegisterAgent(ctx, RegisterAgentInput{ID: "reviewer", DisplayName: "Reviewer", Capabilities: []AgentCapability{{Name: "code_review"}}, ConnectTo: []string{"planner"}})
	if err != nil {
		return err
	}
	planner := Client{Address: address, Token: plannerRegistration.AgentToken}
	reviewer := Client{Address: address, Token: reviewerRegistration.AgentToken}
	if _, err := planner.Heartbeat(ctx, HeartbeatInput{Lifecycle: LifecycleReady, Ready: true}); err != nil {
		return err
	}
	if _, err := reviewer.Heartbeat(ctx, HeartbeatInput{Lifecycle: LifecycleReady, Ready: true}); err != nil {
		return err
	}
	peers, err := planner.ListPeers(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("planner discovered: %s\n", peers[0].DisplayName)
	receipt, err := planner.SendMessage(ctx, SendMessageInput{
		To: "reviewer", Mode: MessageRequest, Body: "Review the checkout retry path.",
		Context: []ContextItem{{Kind: "file", Title: "checkout.ts", URI: "file:///workspace/checkout.ts"}},
	})
	if err != nil {
		return err
	}
	fmt.Printf("request accepted: %s\n", receipt.MessageID)
	task, err := planner.AddTask(ctx, AddTaskInput{Title: "Review checkout retry path"})
	if err != nil {
		return err
	}
	if _, err := reviewer.ClaimTask(ctx, task.ID); err != nil {
		return err
	}
	messages, err := reviewer.PullInbox(ctx, 10, 0)
	if err != nil {
		return err
	}
	reply, err := reviewer.SendMessage(ctx, SendMessageInput{
		To: "planner", Mode: MessageResponse, ResponseTo: messages[0].ID,
		Body: "The retry path drops the idempotency key.",
	})
	if err != nil {
		return err
	}
	if _, err := reviewer.AcknowledgeMessages(ctx, []string{messages[0].ID}); err != nil {
		return err
	}
	if _, err := reviewer.CompleteTask(ctx, task.ID, "done"); err != nil {
		return err
	}
	replies, err := planner.PullInbox(ctx, 10, 0)
	if err != nil {
		return err
	}
	if _, err := planner.AcknowledgeMessages(ctx, []string{replies[0].ID}); err != nil {
		return err
	}
	fmt.Printf("reply received: %s\n", replies[0].Body)
	fmt.Printf("task completed: done\n")
	_ = reply
	return nil
}
