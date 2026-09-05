package bus

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskProgressIsOrderedExecutionBoundAndRetained(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	task, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "Review retries"})
	requireNoError(t, err)
	if _, err := agents.runtime.AddTaskProgress(ctx, agents.reviewerToken, task.ID, AddTaskProgressInput{Kind: "progress", Text: "too early"}); err == nil {
		t.Fatal("unclaimed task accepted progress")
	} else {
		requireCode(t, err, CodeConflict)
	}
	if _, err := agents.runtime.ClaimTask(ctx, agents.reviewerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.AddTaskProgress(ctx, agents.plannerToken, task.ID, AddTaskProgressInput{Kind: "note", Text: "not the claimant"}); err == nil {
		t.Fatal("another execution added progress")
	} else {
		requireCode(t, err, CodeConflict)
	}
	kinds := []string{"progress", "note", "blocker"}
	for index := 1; index <= 25; index++ {
		entry, err := agents.runtime.AddTaskProgress(ctx, agents.reviewerToken, task.ID, AddTaskProgressInput{
			Kind: kinds[(index-1)%len(kinds)], Text: fmt.Sprintf("Update %d", index),
		})
		require(t, err == nil && entry.Sequence == int64(index) && entry.ExecutionID == agents.reviewer.ExecutionID, "unexpected progress entry %d: %#v, %v", index, entry, err)
	}
	history, err := agents.runtime.ListTaskProgress(ctx, agents.scope.ScopeToken, task.ID)
	require(t, err == nil && len(history) == 25 && history[0].Sequence == 1 && history[24].Sequence == 25, "unexpected progress history: %#v, %v", history, err)
	tasks, err := agents.runtime.ListTasks(ctx, agents.plannerToken, false)
	require(t, err == nil && len(tasks) == 1 && len(tasks[0].RecentProgress) == 20 && tasks[0].RecentProgress[0].Sequence == 6 && tasks[0].RecentProgress[19].Sequence == 25, "unexpected recent progress: %#v, %v", tasks, err)
	if _, err := agents.runtime.CompleteTask(ctx, agents.reviewerToken, task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	history, err = agents.runtime.ListTaskProgress(ctx, agents.plannerToken, task.ID)
	require(t, err == nil && len(history) == 25, "completion removed progress: %#v, %v", history, err)
	_, err = agents.runtime.AddTaskProgress(ctx, agents.reviewerToken, task.ID, AddTaskProgressInput{Kind: "note", Text: "too late"})
	requireCode(t, err, CodeConflict)
}

func TestTaskProgressRejectsInvalidInputAndReplacedExecution(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	task, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "Review"})
	requireNoError(t, err)
	if _, err := agents.runtime.ClaimTask(ctx, agents.reviewerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	_, err = agents.runtime.AddTaskProgress(ctx, agents.reviewerToken, task.ID, AddTaskProgressInput{Kind: "status", Text: "invalid"})
	requireCode(t, err, CodeInvalidArgument)
	_, err = agents.runtime.AddTaskProgress(ctx, agents.reviewerToken, task.ID, AddTaskProgressInput{Kind: "note", Text: strings.Repeat("x", 4001)})
	requireCode(t, err, CodeInvalidArgument)
	replacement, err := agents.runtime.RegisterAgent(ctx, agents.scope.ScopeToken, RegisterAgentInput{ID: "reviewer", DisplayName: "Reviewer replacement"})
	requireNoError(t, err)
	_, err = agents.runtime.AddTaskProgress(ctx, agents.reviewerToken, task.ID, AddTaskProgressInput{Kind: "note", Text: "stale"})
	requireCode(t, err, CodeUnauthenticated)
	if _, err := agents.runtime.ClaimTask(ctx, replacement.AgentToken, task.ID); err != nil {
		t.Fatal(err)
	}
	entry, err := agents.runtime.AddTaskProgress(ctx, replacement.AgentToken, task.ID, AddTaskProgressInput{Kind: "note", Text: "new execution"})
	require(t, err == nil && entry.ExecutionID == replacement.ExecutionID, "replacement could not add progress: %#v, %v", entry, err)
}

func TestTaskProgressSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bus.db")
	agents := setupAgents(t, path)
	ctx := context.Background()
	task, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "Persistent progress"})
	requireNoError(t, err)
	if _, err := agents.runtime.ClaimTask(ctx, agents.reviewerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.AddTaskProgress(ctx, agents.reviewerToken, task.ID, AddTaskProgressInput{Kind: "blocker", Text: "Waiting for approval"}); err != nil {
		t.Fatal(err)
	}
	requireNoError(t, agents.runtime.Close())
	restarted, err := Open(path)
	requireNoError(t, err)
	defer restarted.Close()
	history, err := restarted.ListTaskProgress(ctx, agents.scope.ScopeToken, task.ID)
	require(t, err == nil && len(history) == 1 && history[0].Kind == "blocker" && history[0].Text == "Waiting for approval", "task progress did not survive restart: %#v, %v", history, err)
}

func TestTaskProgressCountIsBounded(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	task, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "Many updates"})
	requireNoError(t, err)
	if _, err := agents.runtime.ClaimTask(ctx, agents.reviewerToken, task.ID); err != nil {
		t.Fatal(err)
	}
	transaction, err := sqliteStore(t, agents.runtime).db.Begin()
	requireNoError(t, err)
	for sequence := 1; sequence <= maxTaskProgressEvents; sequence++ {
		if _, err := transaction.Exec(`INSERT INTO task_progress(task_id,scope_id,sequence,agent_id,execution_id,kind,text,created_at) VALUES(?,?,?,?,?,'progress','update',1)`,
			task.ID, agents.scope.ScopeID, sequence, agents.reviewer.AgentID, agents.reviewer.ExecutionID); err != nil {
			transaction.Rollback()
			t.Fatal(err)
		}
	}
	requireNoError(t, transaction.Commit())
	_, err = agents.runtime.AddTaskProgress(ctx, agents.reviewerToken, task.ID, AddTaskProgressInput{Kind: "progress", Text: "one too many"})
	requireCode(t, err, CodeBackpressure)
}
