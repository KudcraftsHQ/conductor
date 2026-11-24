//go:build !unix

package lifecycle

import "errors"

func terminate(int) {}

func freeBytes(string) int64 { return -1 }

// The lifecycle executors need /proc and flock; off Unix the sweep refuses.
func lockSweep(string) (func(), error) {
	return nil, errors.New("worktree-lifecycle sweeps run on Unix only")
}
