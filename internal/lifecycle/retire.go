package lifecycle

import "context"

// PHASE 2 — agent A owns this file.

// Retire removes a worktree so that Revive can bring it back exactly:
//  1. id := NewRecordID; Snapshot when f.HasUnsavedWork(), else
//     CreateGraveyardRef(f.RepoRoot, id, f.Head);
//  2. archive small ignored, non-regenerable files (not on the slim
//     allowlist; cap s.IgnoredArchiveCapMB) into RecordDir(id)/IgnoredArchiveName,
//     listing ArchivedPaths and SkippedPaths;
//  3. env.WriteRecord (fsynced) BEFORE anything is removed;
//  4. conductor-registered: release ports/DB/tunnel through the existing
//     hibernate / ReleaseResources path and keep the entry (marked archived);
//     close f.DevWindows, re-check ProcsInside (skip if busy);
//  5. `git worktree remove <path>` WITHOUT --force, clear leftovers written
//     during removal (interim script's remove_worktree), `git worktree prune`.
//     Never delete the branch.
//
// Returns the record written.
func Retire(ctx context.Context, env *Env, f *Facts, plan Plan, s Settings, o ExecOptions) (*Record, error) {
	return nil, ErrNotImplemented
}
