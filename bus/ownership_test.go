package bus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseOwnershipUsesCanonicalPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bus.db")
	first, err := OpenStore(path)
	requireNoError(t, err)
	defer first.Close()
	assertLocked := func(t *testing.T, path string) {
		t.Helper()
		second, err := OpenStore(path)
		if err == nil {
			second.Close()
			t.Fatal("opened a second owner of the database")
		}
	}
	t.Run("same path", func(t *testing.T) { assertLocked(t, path) })
	t.Run("file symlink", func(t *testing.T) {
		alias := filepath.Join(root, "alias.db")
		if err := os.Symlink(path, alias); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		assertLocked(t, alias)
	})
	t.Run("parent symlink", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(root, alias); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		assertLocked(t, filepath.Join(alias, "bus.db"))
	})
	t.Run("hard link rejected", func(t *testing.T) {
		alias := filepath.Join(root, "hardlink.db")
		if err := os.Link(path, alias); err != nil {
			t.Skipf("hard link creation unavailable: %v", err)
		}
		assertLocked(t, alias)
	})
}

func TestDifferentRuntimeDirectoriesCannotOwnSameDatabase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths := DaemonPaths{DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "first")}
	paths.Database = filepath.Join(paths.DataDir, "bus.db")
	paths.RunFile = filepath.Join(paths.RuntimeDir, "bus.json")
	paths.LockFile = filepath.Join(paths.RuntimeDir, "bus.lock")
	first, err := StartDaemon(ctx, 0, &paths)
	requireNoError(t, err)
	defer first.Stop(ctx)
	paths.RuntimeDir = filepath.Join(root, "second")
	paths.RunFile = filepath.Join(paths.RuntimeDir, "bus.json")
	paths.LockFile = filepath.Join(paths.RuntimeDir, "bus.lock")
	second, err := StartDaemon(ctx, 0, &paths)
	if err == nil {
		second.Stop(ctx)
		t.Fatal("a different discovery directory bypassed database ownership")
	}
	if _, err := ReadRunFile(first.Paths.RunFile); err != nil {
		t.Fatalf("failed second start removed the first owner's discovery: %v", err)
	}
}
