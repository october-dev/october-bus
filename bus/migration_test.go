package bus

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func releasedV2Fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "released.db")
	db, err := sql.Open("sqlite", path)
	requireNoError(t, err)
	defer db.Close()
	ddl, err := os.ReadFile("testdata/schema-v2-rc4.sql")
	requireNoError(t, err)
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	now := nowMillis()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO scopes VALUES('migration',?,?)`, []any{tokenDigest("scope-secret"), now}},
		{`INSERT INTO agents VALUES('migration','a','A','[]','exec_a',?,'ready',1,?,?,?),('migration','b','B','[]','exec_b',?,'ready',1,?,?,?)`, []any{tokenDigest("agent-a"), now + 300000, now, now, tokenDigest("agent-b"), now + 300000, now, now}},
		{`INSERT INTO peer_links VALUES('migration','a','b',?)`, []any{now}},
		{`INSERT INTO messages(message_id,scope_id,from_agent,to_agent,mode,body,context_json,request_hash,state,created_at,delivered_at) VALUES('msg_request','migration','a','b','request','outstanding','[]','hash','delivered',?,?)`, []any{now, now}},
		{`INSERT INTO tasks VALUES('task_done','migration','preserve description','a','a','exec_a','done','[]','finished',?,?),('task_claim','migration','dependent','a','b','exec_b','claimed','["task_done"]',NULL,?,?)`, []any{now, now, now, now}},
		{`INSERT INTO escalations VALUES('ask_pending','migration','a','question','[]','pending',NULL,?,NULL)`, []any{now}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestReleasedV2MigrationPreservesStateAndBackup(t *testing.T) {
	path := releasedV2Fixture(t)
	s, err := OpenStore(path)
	requireNoError(t, err)
	defer s.Close()
	ctx := context.Background()
	if _, err := s.AuthenticateScope(ctx, "scope-secret"); err != nil {
		t.Fatal(err)
	}
	p, err := s.AuthenticateAgent(ctx, "agent-b")
	require(t, err == nil && p.ExecutionID == "exec_b", "execution not preserved: %+v %v", p, err)
	tasks, err := s.ListTasks(ctx, "migration", false)
	require(t, err == nil && len(tasks) == 2, "tasks: %+v %v", tasks, err)
	for _, task := range tasks {
		require(t, task.ID != "task_claim" || task.Status == "claimed", "claim lost: %+v", task)
		require(t, task.ID != "task_done" || task.Description == "preserve description", "description lost: %+v", task)
	}
	var requestCount, escalationCount int
	s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE mode='request' AND state='delivered'").Scan(&requestCount)
	s.db.QueryRow("SELECT COUNT(*) FROM escalations WHERE status='pending'").Scan(&escalationCount)
	if requestCount != 1 || escalationCount != 1 {
		t.Fatal("outstanding obligations lost")
	}
	backups, err := filepath.Glob(path + ".schema2-backup-*")
	require(t, err == nil && len(backups) == 1, "backup: %v %v", backups, err)
	backup, err := sql.Open("sqlite", backups[0])
	requireNoError(t, err)
	defer backup.Close()
	var version int
	if err := backup.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("original backup missing: %d %v", version, err)
	}
	if _, err := s.db.Exec("UPDATE agents SET lifecycle='offline',ready=0,lease_expires_at=0"); err != nil {
		t.Fatal(err)
	}
	archive, err := s.ExportScope(ctx, "migration")
	requireNoError(t, err)
	destination, err := OpenStore(":memory:")
	requireNoError(t, err)
	defer destination.Close()
	if _, err := destination.ImportScope(ctx, archive); err != nil {
		t.Fatalf("migrated archive cannot restore: %v", err)
	}
}

func TestMigrationCrashChild(t *testing.T) {
	path := os.Getenv("BUS_MIGRATION_CRASH_DB")
	if path == "" {
		return
	}
	db, err := sql.Open("sqlite", path)
	requireNoError(t, err)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	requireNoError(t, err)
	requireNoError(t, migrateV2Tx(context.Background(), tx))
	// Simulate process death after all migration writes, before commit.
	os.Exit(42)
}

func TestMigrationInterruptedBeforeCommitKeepsReleasedSchema(t *testing.T) {
	path := releasedV2Fixture(t)
	command := exec.Command(os.Args[0], "-test.run=^TestMigrationCrashChild$")
	command.Env = append(os.Environ(), "BUS_MIGRATION_CRASH_DB="+path)
	err := command.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 42 {
		t.Fatalf("crash setup: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	requireNoError(t, err)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("partial version: %d %v", version, err)
	}
	var description string
	if err := db.QueryRow("SELECT description FROM tasks WHERE task_id='task_done'").Scan(&description); err != nil || description != "preserve description" {
		t.Fatalf("partial migration: %q %v", description, err)
	}
	db.Close()
	upgraded, err := OpenStore(path)
	requireNoError(t, err)
	upgraded.Close()
}
