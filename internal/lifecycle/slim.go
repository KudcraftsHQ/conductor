package lifecycle

import "context"

// PHASE 2 — agent A owns this file.

// SlimResult is what Slim removed.
type SlimResult struct {
	Removed    []string `json:"removed"`           // relative paths deleted
	Skipped    []string `json:"skipped,omitempty"` // "path: why" (not ignored, tracked, NeverSlim…)
	FreedBytes int64    `json:"freedBytes"`
}

// Slim deletes the worktree's regenerable dirs (f.SlimTargets), each confirmed
// gitignored with `git check-ignore` and never tracked, never in NeverSlim or
// .env*. node_modules only when f.LockfileTracked. Closes f.DevWindows first,
// re-checks ProcsInside, logs, and marks the inventory entry SlimmedAt /
// SlimmedPaths.
func Slim(ctx context.Context, env *Env, f *Facts, plan Plan, s Settings, o ExecOptions) (*SlimResult, error) {
	return nil, ErrNotImplemented
}
