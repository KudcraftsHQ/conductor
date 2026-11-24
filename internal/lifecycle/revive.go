package lifecycle

import "context"

// PHASE 2 — agent B owns this file.

// ReviveOptions control Revive.
type ReviveOptions struct {
	Exec ExecOptions
	// NoInstall skips the dependency install after the worktree is back.
	NoInstall bool
}

// ReviveResult says what came back.
type ReviveResult struct {
	Record    *Record  `json:"record"`
	Path      string   `json:"path"`
	Restored  []string `json:"restored,omitempty"` // ignored files restored from the archive
	Installed bool     `json:"installed"`
}

// FindRecord resolves a revive target — thread id, branch, path or record id —
// to the newest matching record not yet revived.
func FindRecord(ctx context.Context, env *Env, target string) (*Record, error) {
	return nil, ErrNotImplemented
}

// Revive recreates a retired worktree at the SAME path (so its T3 thread
// continues unchanged): `git worktree add <path> <branch>` (the graveyard SHA
// if the branch is gone); for a snapshot, check out the branch at HEAD then
// restore the snapshot's changes as uncommitted changes; restore the ignored
// archive; Hydrate unless NoInstall; re-provision ports/DB only through the
// existing adopt path when the project wants them; mark the inventory entry
// and record revived; log.
func Revive(ctx context.Context, env *Env, target string, o ReviveOptions) (*ReviveResult, error) {
	return nil, ErrNotImplemented
}
