//go:build darwin || linux

package bus

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func lockFile(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) }

func processMayBeAlive(pid int) bool { return unix.Kill(pid, 0) != unix.ESRCH }

func validateDatabaseLinks(_ string, info os.FileInfo) error {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
		return fmt.Errorf("hard-linked database files are unsupported; use one canonical database path")
	}
	return nil
}
