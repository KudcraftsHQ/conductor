package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SnapshotResult is a needs-you (or pre-retire) snapshot.
type SnapshotResult struct {
	ID  string `json:"id"`  // record id the ref is named after
	Ref string `json:"ref"` // refs/graveyard/<id>
	SHA string `json:"sha"` // the snapshot commit (parent = HEAD); HEAD itself when clean
	// Head is the HEAD the snapshot was taken on.
	Head string `json:"head"`
}

// snapshotTimeout bounds `git add -A` into the temporary index: a tree with a
// large untracked, non-ignored directory can take a while.
const snapshotTimeout = 10 * time.Minute

// Snapshot commits the worktree's full dirty state — tracked changes plus
// untracked non-ignored files — into a commit object whose parent is HEAD,
// WITHOUT touching the worktree's branch, index or files (temp GIT_INDEX_FILE
// seeded from HEAD, `git add -A` into it, write-tree, commit-tree), and points
// CreateGraveyardRef(repoRoot, id, sha) at it. Nothing is pushed. When the tree
// matches HEAD (only unpushed commits) the ref points at HEAD itself.
//
// Snapshot is the low-level step; NeedsYouSnapshot is the executor.
func Snapshot(ctx context.Context, env *Env, f *Facts, id string, o ExecOptions) (*SnapshotResult, error) {
	if f.RepoRoot == "" {
		return nil, fmt.Errorf("snapshot %s: repo root unknown", f.Path)
	}
	res := &SnapshotResult{ID: id, Ref: GraveyardRef(id), Head: f.Head}
	if o.DryRun {
		return res, nil
	}
	sha, err := snapshotCommit(ctx, env, f)
	if err != nil {
		return nil, err
	}
	res.SHA = sha
	if err := CreateGraveyardRef(ctx, f.RepoRoot, id, sha); err != nil {
		return nil, err
	}
	if got, ok := ReadGraveyardRef(ctx, f.RepoRoot, id); !ok || got != sha {
		return nil, fmt.Errorf("graveyard ref %s did not read back as %s", res.Ref, short(sha))
	}
	return res, nil
}

// snapshotCommit builds the snapshot commit and returns its sha (HEAD when the
// tree has no changes against HEAD).
func snapshotCommit(ctx context.Context, env *Env, f *Facts) (string, error) {
	if err := os.MkdirAll(env.StateDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(env.StateDir, "snapshot-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	idx := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}

	seed := []string{"read-tree", "--empty"}
	if f.Head != "" {
		seed = []string{"read-tree", f.Head}
	}
	if _, err := gitRun(ctx, f.Path, time.Minute, idx, seed...); err != nil {
		return "", err
	}
	// -A: modifications, deletions and untracked files; .gitignore is honoured,
	// so ignored files stay out (Retire archives the small ones separately).
	if _, err := gitRun(ctx, f.Path, snapshotTimeout, idx, "add", "-A"); err != nil {
		return "", err
	}
	tree, err := gitRun(ctx, f.Path, time.Minute, idx, "write-tree")
	if err != nil {
		return "", err
	}
	if f.Head != "" {
		if headTree, err := fxGit(ctx, f.Path, "rev-parse", f.Head+"^{tree}"); err == nil && headTree == tree {
			return f.Head, nil
		}
	}
	msg := fmt.Sprintf("conductor lifecycle snapshot\n\nworktree: %s\nbranch: %s\nhead: %s\ntaken: %s\n\n"+
		"Tracked changes and untracked non-ignored files, committed without touching\n"+
		"the worktree's branch, index or files. Local only; never pushed.\n",
		f.Path, firstNonEmpty(f.Branch, "(detached)"), firstNonEmpty(f.Head, "(none)"), env.Now().UTC().Format(time.RFC3339))
	args := []string{"commit-tree", tree, "-m", msg}
	if f.Head != "" {
		args = []string{"commit-tree", tree, "-p", f.Head, "-m", msg}
	}
	ident := []string{
		"GIT_AUTHOR_NAME=conductor lifecycle", "GIT_AUTHOR_EMAIL=lifecycle@conductor.invalid",
		"GIT_COMMITTER_NAME=conductor lifecycle", "GIT_COMMITTER_EMAIL=lifecycle@conductor.invalid",
	}
	return gitRun(ctx, f.Path, time.Minute, ident, args...)
}

// NeedsYouSnapshot is the needs-you executor: re-check, snapshot to a fresh
// refs/graveyard/<id>, log, and record SnapshotSHA/Ref/Head/At and RetireAt on
// the entry. The sweep sets NeedsYouNotifiedAt once the ntfy message is sent.
func NeedsYouSnapshot(ctx context.Context, env *Env, f *Facts, plan Plan, s Settings, o ExecOptions) (*SnapshotResult, error) {
	if paused, _ := env.Paused(); paused {
		return nil, ErrPaused
	}
	fresh, freshPlan, why := recheck(ctx, env, f, ActionSnapshot, s, o.Force)
	if why != "" {
		return nil, skipped(env, ActionSnapshot, f, plan.Forced, why)
	}
	id := NewRecordID(fresh, env.Now())
	if o.DryRun {
		_ = env.LogEvent(Event{Action: ActionSnapshot, Path: f.Path, Result: "dry-run", RecordID: id, Forced: plan.Forced})
		return &SnapshotResult{ID: id, Ref: GraveyardRef(id), Head: fresh.Head}, nil
	}
	res, err := Snapshot(ctx, env, fresh, id, o)
	if err != nil {
		_ = env.LogEvent(Event{Action: ActionSnapshot, Path: f.Path, Result: "failed", Error: err.Error(), RecordID: id, Forced: plan.Forced})
		recordOutcome(env, f.Path, ActionSnapshot, err.Error(), nil)
		return nil, err
	}
	retireAt := freshPlan.RetireAt
	_ = env.LogEvent(Event{Action: ActionSnapshot, Path: f.Path, Result: "ok", RecordID: id, SHA: res.SHA, Forced: plan.Forced,
		Reason: GitSummary(fresh) + "; retire on " + retireAt.Format("2006-01-02")})
	now := env.Now().UTC()
	recordOutcome(env, f.Path, ActionSnapshot, "", func(en *Entry) {
		en.SnapshotSHA, en.SnapshotRef, en.SnapshotHead, en.SnapshotAt = res.SHA, res.Ref, res.Head, now
		if !retireAt.IsZero() {
			en.RetireAt = retireAt
		}
	})
	return res, nil
}
