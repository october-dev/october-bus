package bus

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Interpose precisely between Runtime authentication and the store mutation.
// This models another request committing execution replacement in that gap.
type auditReplacementStore struct {
	storageBackend
	afterAuth func(Principal)
}

func (s *auditReplacementStore) AuthenticateAgent(ctx context.Context, token string) (Principal, error) {
	p, err := s.storageBackend.AuthenticateAgent(ctx, token)
	if err == nil && s.afterAuth != nil {
		callback := s.afterAuth
		s.afterAuth = nil
		callback(p)
	}
	return p, err
}

func TestAuditReplacementMustFenceMutations(t *testing.T) {
	for _, operation := range []string{"send", "claim", "complete", "ask"} {
		t.Run(operation, func(t *testing.T) {
			agents := setupAgents(t, ":memory:")
			defer agents.runtime.Close()
			ctx := context.Background()
			task, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "audit task"})
			requireNoError(t, err)
			if operation == "complete" {
				if _, err := agents.runtime.ClaimTask(ctx, agents.plannerToken, task.ID); err != nil {
					t.Fatal(err)
				}
			}
			base := agents.runtime.store
			agents.runtime.store = &auditReplacementStore{storageBackend: base, afterAuth: func(p Principal) {
				_, err := base.RegisterAgent(ctx, p.ScopeID, RegisterAgentInput{ID: p.AgentID, DisplayName: "Replacement", Capabilities: []AgentCapability{}, LeaseMS: 300000})
				requireNoError(t, err)
			}}
			switch operation {
			case "send":
				_, err = agents.runtime.SendMessage(ctx, agents.plannerToken, SendMessageInput{To: "reviewer", Body: "stale write"})
			case "claim":
				_, err = agents.runtime.ClaimTask(ctx, agents.plannerToken, task.ID)
			case "complete":
				_, err = agents.runtime.CompleteTask(ctx, agents.plannerToken, task.ID, "stale completion")
			case "ask":
				_, err = agents.runtime.AskHuman(ctx, agents.plannerToken, AskHumanInput{Question: "stale escalation"})
			}
			if err == nil {
				t.Errorf("replaced execution successfully performed %s after replacement committed", operation)
			}
		})
	}
}

func TestAuditSessionCloseMustRetireAuthorityAndClaims(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	server := httptest.NewServer(NewServer(agents.runtime, ServerOptions{}))
	defer server.Close()
	session, err := StartAgentSession(ctx, AgentSessionOptions{Address: server.URL, ScopeToken: agents.scope.ScopeToken, Registration: RegisterAgentInput{ID: "managed", DisplayName: "Managed"}})
	requireNoError(t, err)
	task, err := session.Client.AddTask(ctx, AddTaskInput{Title: "owned work"})
	requireNoError(t, err)
	if _, err := session.Client.ClaimTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	requireNoError(t, session.Close(ctx))
	if _, err := session.Client.Heartbeat(ctx, HeartbeatInput{Lifecycle: LifecycleOffline, LeaseMS: 300000}); err == nil {
		t.Error("closed session token can still renew its lease")
	}
	tasks, err := agents.runtime.ListTasks(ctx, agents.scope.ScopeToken, false)
	requireNoError(t, err)
	if tasks[0].Status != "open" {
		t.Errorf("closed session task remains %s", tasks[0].Status)
	}
}

func TestAuditPruneA2ACompletedConversation(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, issued := setupA2APrincipal(t, agents)
	task, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, AcceptA2AMessageInput{ClientMessageID: "audit-request", Body: "review"})
	requireNoError(t, err)
	reservation, err := agents.runtime.ReserveInbox(ctx, agents.reviewerToken, 10, 0)
	require(t, err == nil && reservation != nil, "reserve: %v", err)
	if _, err := agents.runtime.CommitInbox(ctx, agents.reviewerToken, reservation.ID); err != nil {
		t.Fatal(err)
	}
	requestID := task.Messages[0].BusRequestMessageID
	if _, err := agents.runtime.SendMessage(ctx, agents.reviewerToken, SendMessageInput{To: issued.Principal.ID, Mode: MessageResponse, ResponseTo: requestID, Body: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.AcknowledgeMessages(ctx, agents.reviewerToken, []string{requestID}); err != nil {
		t.Fatal(err)
	}
	input := PruneScopeInput{Before: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)}
	dry, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, input)
	require(t, err == nil && dry.Records.Messages == 2, "dry run: %#v %v", dry, err)
	input.Execute = true
	if _, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, input); err != nil {
		t.Errorf("dry run offers 2 terminal messages but execution fails: %v", err)
	}
}

func TestAuditPrunedDependencyArchiveRestores(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	parent, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "old parent"})
	requireNoError(t, err)
	if _, err := agents.runtime.ClaimTask(ctx, agents.plannerToken, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.CompleteTask(ctx, agents.plannerToken, parent.ID, ""); err != nil {
		t.Fatal(err)
	}
	child, err := agents.runtime.AddTask(ctx, agents.plannerToken, AddTaskInput{Title: "newer completed dependent", Dependencies: []string{parent.ID}})
	requireNoError(t, err)
	if _, err := agents.runtime.ClaimTask(ctx, agents.plannerToken, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.runtime.CompleteTask(ctx, agents.plannerToken, child.ID, ""); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().Add(-time.Hour)
	if _, err := sqliteStore(t, agents.runtime).db.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE task_id=?`, cutoff.Add(-time.Hour).UnixMilli(), parent.ID); err != nil {
		t.Fatal(err)
	}
	prune, err := agents.runtime.PruneScope(ctx, agents.scope.ScopeToken, PruneScopeInput{Before: cutoff.UTC().Format(time.RFC3339Nano), Execute: true})
	require(t, err == nil && prune.Records.Tasks == 0, "prune: %#v %v", prune, err)
	offlineAgents(t, agents)
	archive, err := agents.runtime.ExportScope(ctx, agents.scope.ScopeID)
	requireNoError(t, err)
	restored, err := Open(":memory:")
	requireNoError(t, err)
	defer restored.Close()
	if _, err := restored.ImportScope(ctx, archive); err != nil {
		t.Errorf("export after successful prune cannot restore: %v", err)
	}
}

func TestAuditOutputWithoutAgentPublishersArchiveRestores(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	if _, err := agents.runtime.CreateOutputStream(ctx, agents.scope.ScopeToken, CreateOutputStreamInput{Name: "external-output"}); err != nil {
		t.Fatal(err)
	}
	offlineAgents(t, agents)
	archive, err := agents.runtime.ExportScope(ctx, agents.scope.ScopeID)
	requireNoError(t, err)
	restored, err := Open(":memory:")
	requireNoError(t, err)
	defer restored.Close()
	if _, err := restored.ImportScope(ctx, archive); err != nil {
		t.Errorf("valid stream export cannot restore: %v", err)
	}
}

func TestAuditUnhealthyDaemonCannotLoseItsLock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths := DaemonPaths{DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "run")}
	paths.Database = filepath.Join(paths.DataDir, "bus.db")
	paths.RunFile = filepath.Join(paths.RuntimeDir, "bus.json")
	paths.LockFile = filepath.Join(paths.RuntimeDir, "bus.lock")
	first, err := StartDaemon(ctx, 0, &paths)
	requireNoError(t, err)
	defer first.Stop(ctx)
	old := time.Now().Add(-time.Minute)
	requireNoError(t, os.Chtimes(paths.LockFile, old, old))
	// Occupy the first daemon's single connection without taking a SQLite write lock.
	connection, err := sqliteStore(t, first.Server.runtime).db.Conn(ctx)
	requireNoError(t, err)
	defer connection.Close()
	second, err := StartDaemon(ctx, 0, &paths)
	if err == nil {
		defer second.Stop(ctx)
		t.Error("second daemon started against the same database while the first remained alive")
	}
}

type auditRevocationStore struct {
	storageBackend
	afterAuth func(A2APrincipal)
}

func (s *auditRevocationStore) AuthenticateA2APrincipal(ctx context.Context, token, publication string) (A2APrincipal, error) {
	principal, err := s.storageBackend.AuthenticateA2APrincipal(ctx, token, publication)
	if err == nil && s.afterAuth != nil {
		callback := s.afterAuth
		s.afterAuth = nil
		callback(principal)
	}
	return principal, err
}

func TestAuditA2ARevocationMustFenceAcceptance(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	publication, issued := setupA2APrincipal(t, agents)
	base := agents.runtime.store
	agents.runtime.store = &auditRevocationStore{storageBackend: base, afterAuth: func(p A2APrincipal) {
		if _, err := base.SetA2APrincipalEnabled(ctx, p.ScopeID, p.ID, false); err != nil {
			t.Fatal(err)
		}
	}}
	_, err := agents.runtime.AcceptA2AMessage(ctx, issued.Credential, publication.ID, AcceptA2AMessageInput{ClientMessageID: "revoked-inflight", Body: "work after disable"})
	if err == nil {
		t.Error("disabled principal created new work after revocation committed")
	}
}
