//go:build unix

package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func terminate(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }

// freeBytes is the space available to an unprivileged user on path's filesystem.
func freeBytes(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// errSweepRunning: another sweep holds the lock.
var errSweepRunning = errors.New("another worktree-lifecycle sweep is running")

// lockSweep takes an exclusive, non-blocking flock so two sweeps (the timer
// and a manual gc) never act at once. The kernel drops it if we die.
func lockSweep(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errSweepRunning
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
