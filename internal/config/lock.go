package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ProvisioningSentinel is the file conductor used to write into a worktree
// while it set it up. Nothing writes it any more — it is kept only so that
// `conductor adopt` can delete one left by an older build, and so that git can
// be told to ignore it.
//
// It was replaced because a marker cannot answer the question it was asked.
// Only one of the six paths that provision a worktree ever wrote it, it was
// removed whether setup succeeded or failed, a killed provisioner left it
// behind forever, and it was created after conductor's own startup — so an
// agent whose first turn began immediately, which is exactly what T3 does,
// could check before it existed. Readiness now comes from the worktree's setup
// status in conductor.json; see internal/ready.
const ProvisioningSentinel = ".conductor-provisioning"

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
