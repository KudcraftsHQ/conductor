package lifecycle

import "context"

// PHASE 2 — agent B owns this file.

// HydrateOptions control Hydrate.
type HydrateOptions struct {
	Exec ExecOptions
	// Quiet: the SessionStart hook — print nothing when there is nothing to
	// do, never return an error that would fail the session.
	Quiet bool
}

// HydrateResult says whether deps were installed.
type HydrateResult struct {
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Command   string `json:"command,omitempty"` // e.g. "bun install"
	Skipped   string `json:"skipped,omitempty"` // why nothing ran
}

// Hydrate reinstalls dependencies in a slimmed worktree: idempotent and a fast
// no-op when node_modules exists. Acts only inside a worktree the lifecycle
// knows (inventory entry or discovered), never in a repo root without
// node_modules unless it is a known slimmed worktree. Uses the lockfile's
// package manager (bun.lock/bun.lockb → bun install). Marks the entry
// HydratedAt.
func Hydrate(ctx context.Context, env *Env, path string, o HydrateOptions) (*HydrateResult, error) {
	return nil, ErrNotImplemented
}
