package bus

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A repeatable local baseline, not a hosted capacity certification. Use a fixed
// iteration count to compare changes on the same machine/filesystem.
func BenchmarkLocalMixedHTTP(b *testing.B) {
	r, err := Open(filepath.Join(b.TempDir(), "load.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	server := httptest.NewServer(NewServer(r, ServerOptions{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	scope, err := r.CreateScope(ctx, CreateScopeInput{ID: "load"})
	if err != nil {
		b.Fatal(err)
	}
	type pair struct{ sender, receiver Client }
	pairs := []pair{}
	for i := 0; i < 16; i++ {
		sender, err := r.RegisterAgent(ctx, scope.ScopeToken, RegisterAgentInput{ID: fmt.Sprintf("sender-%d", i), DisplayName: "Sender", LeaseMS: 300000})
		if err != nil {
			b.Fatal(err)
		}
		receiver, err := r.RegisterAgent(ctx, scope.ScopeToken, RegisterAgentInput{ID: fmt.Sprintf("receiver-%d", i), DisplayName: "Receiver", ConnectTo: []string{sender.AgentID}, LeaseMS: 300000})
		if err != nil {
			b.Fatal(err)
		}
		pairs = append(pairs, pair{Client{Address: server.URL, Token: sender.AgentToken}, Client{Address: server.URL, Token: receiver.AgentToken}})
	}
	var next atomic.Int64
	var mu sync.Mutex
	durations := []time.Duration{}
	errors := make(chan error, len(pairs)+1)
	var workers sync.WaitGroup
	var prunedMessages, prunedTasks int64
	backupPath := filepath.Join(b.TempDir(), "during-load.db")
	b.ResetTimer()
	if b.N >= 1000 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for next.Load() < 500 {
				if ctx.Err() != nil {
					errors <- ctx.Err()
					return
				}
				time.Sleep(time.Millisecond)
			}
			if err := r.store.Backup(ctx, backupPath); err != nil {
				errors <- err
				return
			}
			result, err := r.PruneScope(ctx, scope.ScopeToken, PruneScopeInput{Before: time.Now().Add(-100 * time.Millisecond).UTC().Format(time.RFC3339Nano), Execute: true})
			if err != nil {
				errors <- err
				return
			}
			prunedMessages, prunedTasks = result.Records.Messages, result.Records.Tasks
		}()
	}
	for i, p := range pairs {
		workers.Add(1)
		go func(i int, p pair) {
			defer workers.Done()
			for int(next.Add(1)) <= b.N {
				started := time.Now()
				_, err := p.sender.Heartbeat(ctx, HeartbeatInput{Lifecycle: LifecycleWorking, Ready: true, LeaseMS: 300000})
				elapsed := time.Since(started)
				mu.Lock()
				durations = append(durations, elapsed)
				mu.Unlock()
				if err != nil {
					errors <- err
					return
				}
				_, err = p.sender.SendMessage(ctx, SendMessageInput{To: fmt.Sprintf("receiver-%d", i), Body: "load notification"})
				if err != nil {
					errors <- err
					return
				}
				reservation, err := p.receiver.ReserveInbox(ctx, 1, 0)
				if err != nil {
					errors <- err
					return
				}
				if reservation == nil {
					errors <- fmt.Errorf("accepted message missing from inbox")
					return
				}
				messages, err := p.receiver.CommitInbox(ctx, reservation.ID)
				if err != nil || len(messages) != 1 {
					errors <- fmt.Errorf("delivery: %v", err)
					return
				}
				if _, err := p.receiver.AcknowledgeMessages(ctx, []string{messages[0].ID}); err != nil {
					errors <- err
					return
				}
				task, err := p.sender.AddTask(ctx, AddTaskInput{Title: "load task"})
				if err != nil {
					errors <- err
					return
				}
				if _, err := p.receiver.ClaimTask(ctx, task.ID); err != nil {
					errors <- err
					return
				}
				if _, err := p.receiver.CompleteTask(ctx, task.ID, "done"); err != nil {
					errors <- err
					return
				}
			}
		}(i, p)
	}
	workers.Wait()
	b.StopTimer()
	close(errors)
	for err := range errors {
		b.Fatal(err)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if len(durations) > 0 {
		b.ReportMetric(float64(durations[(len(durations)-1)*95/100].Microseconds())/1000, "heartbeat-p95-ms")
	}
	var messages, tasks int
	s := r.store.(*Store)
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE state='acknowledged'").Scan(&messages); err != nil {
		b.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status='done'").Scan(&tasks); err != nil {
		b.Fatal(err)
	}
	if int64(messages)+prunedMessages != int64(b.N) || int64(tasks)+prunedTasks != int64(b.N) {
		b.Fatalf("durable results: messages=%d tasks=%d want=%d", messages, tasks, b.N)
	}
	if b.N >= 1000 {
		backup, err := OpenStore(backupPath)
		if err != nil {
			b.Fatal(err)
		}
		defer backup.Close()
		var integrity string
		if err := backup.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
			b.Fatalf("load snapshot integrity: %q %v", integrity, err)
		}
	}
}
