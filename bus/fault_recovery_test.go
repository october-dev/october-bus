package bus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStorageFullRollsBackAcceptedWorkAndCanRetry(t *testing.T) {
	a := setupAgents(t, filepath.Join(t.TempDir(), "full.db"))
	defer a.runtime.Close()
	s := sqliteStore(t, a.runtime)
	ctx := context.Background()
	var pages int
	requireNoError(t, s.db.QueryRow("PRAGMA page_count").Scan(&pages))
	if _, err := s.db.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	input := SendMessageInput{To: "reviewer", Body: strings.Repeat("x", 65536), IdempotencyKey: "retry-after-full"}
	if _, err := a.runtime.SendMessage(ctx, a.plannerToken, input); err == nil {
		t.Fatal("storage-full injection did not fail")
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE idempotency_key='retry-after-full'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial message persisted: %d %v", count, err)
	}
	if _, err := s.db.Exec("PRAGMA max_page_count=1073741823"); err != nil {
		t.Fatal(err)
	}
	first, err := a.runtime.SendMessage(ctx, a.plannerToken, input)
	requireNoError(t, err)
	retry, err := a.runtime.SendMessage(ctx, a.plannerToken, input)
	require(t, err == nil && retry.MessageID == first.MessageID, "retry after full duplicated work: %+v %v", retry, err)
}

func TestDurableWriteCrashChild(t *testing.T) {
	path := os.Getenv("BUS_DURABILITY_CRASH_DB")
	if path == "" {
		return
	}
	a := setupAgents(t, path)
	if _, err := a.runtime.SendMessage(context.Background(), a.plannerToken, SendMessageInput{To: "reviewer", Body: "committed before crash", IdempotencyKey: "durable-crash"}); err != nil {
		t.Fatal(err)
	}
	// No graceful Close/checkpoint: process death must release both OS locks and
	// leave the accepted WAL transaction recoverable.
	os.Exit(42)
}

func TestAcceptedWriteAndLockRecoverAfterProcessDeath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crashed.db")
	child := exec.Command(os.Args[0], "-test.run=^TestDurableWriteCrashChild$")
	child.Env = append(os.Environ(), "BUS_DURABILITY_CRASH_DB="+path)
	if err := child.Run(); err == nil {
		t.Fatal("child did not crash")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 42 {
		t.Fatalf("child setup failed: %v", err)
	}
	s, err := OpenStore(path)
	requireNoError(t, err)
	defer s.Close()
	var body string
	if err := s.db.QueryRow("SELECT body FROM messages WHERE idempotency_key='durable-crash'").Scan(&body); err != nil || body != "committed before crash" {
		t.Fatalf("accepted write lost: %q %v", body, err)
	}
	if second, err := OpenStore(path); err == nil {
		second.Close()
		t.Fatal("second database owner admitted")
	}
}
