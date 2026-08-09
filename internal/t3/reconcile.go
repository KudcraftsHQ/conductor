package t3

import (
	"context"
	"path/filepath"
	"strings"
)

// Drift is one worktree whose T3 thread has gone away.
//
// A thread disappears when the user archives or deletes it in T3's UI. The
// worktree, its ports, its database and its tunnel all survive that, because
// they belong to conductor rather than to T3.
type Drift struct {
	Project      string
	Worktree     string
	WorktreePath string
}

// Reconcile reports the T3-hosted worktrees that no longer have a thread.
//
// Only worktrees carrying a marker file are considered. A worktree created
// under tmux or herdr never had a thread, so treating its absence as drift
// would flag every legacy worktree at once — and archiving on drift would then
// destroy all of them.
//
// It deliberately only *reports*. Acting on drift means archiving a worktree,
// which drops its database and removes it from disk — far too destructive to
// trigger from a sidebar tidy-up. The caller decides what to do, and the
// `conductor t3 reconcile` command requires an explicit flag before it acts.
//
// Worktrees are supplied by the caller rather than read here so this stays
// independent of conductor's config and store packages, and therefore testable
// without either.
func (c *Client) Reconcile(ctx context.Context, worktrees []Drift) ([]Drift, error) {
	hosted := make([]Drift, 0, len(worktrees))
	for _, worktree := range worktrees {
		if HasMarker(worktree.WorktreePath) {
			hosted = append(hosted, worktree)
		}
	}
	if len(hosted) == 0 {
		return nil, nil
	}

	snapshot, err := c.Shell(ctx)
	if err != nil {
		return nil, err
	}

	live := make(map[string]bool, len(snapshot.Threads))
	for _, thread := range snapshot.LiveThreadsWithWorktrees() {
		live[normalizePath(thread.Worktree())] = true
	}

	var drifted []Drift
	for _, worktree := range hosted {
		if !live[normalizePath(worktree.WorktreePath)] {
			drifted = append(drifted, worktree)
		}
	}
	return drifted, nil
}

// CountHosted returns how many of the given worktrees T3 is hosting.
func CountHosted(worktrees []Drift) int {
	n := 0
	for _, worktree := range worktrees {
		if HasMarker(worktree.WorktreePath) {
			n++
		}
	}
	return n
}

// normalizePath makes paths comparable across the two systems.
func normalizePath(path string) string {
	return strings.TrimRight(filepath.Clean(path), string(filepath.Separator))
}
