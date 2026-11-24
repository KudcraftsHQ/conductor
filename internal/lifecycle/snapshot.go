package lifecycle

import "context"

// PHASE 2 — agent A owns this file.

// SnapshotResult is a needs-you (or pre-retire) snapshot.
type SnapshotResult struct {
	ID  string `json:"id"`  // record id the ref is named after
	Ref string `json:"ref"` // refs/graveyard/<id>
	SHA string `json:"sha"` // the snapshot commit (parent = HEAD); HEAD itself when clean
}

// Snapshot commits the worktree's full dirty state — tracked changes plus
// untracked non-ignored files — into a commit object whose parent is HEAD,
// WITHOUT touching the worktree's branch, index or files (temp GIT_INDEX_FILE
// seeded from HEAD, `git add -A` into it, write-tree, commit-tree), and points
// CreateGraveyardRef(repoRoot, id, sha) at it. Nothing is pushed. Used by the
// needs-you action (then the entry gets SnapshotSHA/SnapshotAt and Notify
// tells Hammas once) and by Retire on a worktree with unsaved work.
func Snapshot(ctx context.Context, env *Env, f *Facts, id string, o ExecOptions) (*SnapshotResult, error) {
	return nil, ErrNotImplemented
}
