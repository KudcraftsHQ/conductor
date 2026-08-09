package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ProvisioningSentinel is written into a worktree while conductor is setting it
// up and removed when it finishes.
//
// T3 Code launches the setup script and starts the thread's first turn straight
// afterwards, without waiting. A dev database is a full clone and takes
// minutes, so an agent that does not check this will run migrations against a
// database that does not exist yet.
const ProvisioningSentinel = ".conductor-provisioning"

// IsProvisioning reports whether a worktree is still being set up.
func IsProvisioning(worktreePath string) bool {
	_, err := os.Stat(filepath.Join(worktreePath, ProvisioningSentinel))
	return err == nil
}

// AcquireLock takes a cross-process lock and returns the function that releases
// it.
//
// conductor.json is one file shared by every conductor process, and worktrees
// created back to back in T3's UI run `conductor adopt` concurrently. Without
// this they can be handed the same ports: each reads the allocation table
// before the other has written to it.
//
// The lock is a file created O_EXCL holding the owning pid, so a crashed holder
// can be detected rather than blocking every later run forever.
func AcquireLock(name string, timeout time.Duration) (func(), error) {
	dir, err := ConductorDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".lock")

	deadline := time.Now().Add(timeout)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("failed to take the %s lock: %w", name, err)
		}

		// A lock older than the timeout belongs to a process that died holding
		// it. Break it rather than wait out a deadline that will never clear.
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > timeout {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for the %s lock (%s)", name, path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
