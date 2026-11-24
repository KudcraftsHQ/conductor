package lifecycle

import "context"

// PHASE 2 — agent B owns this file.

// OnThreadUnsettled is the T3 watcher hook (internal/t3watch, `conductor t3
// watch`): called when a thread bound to worktreePath unsettles, BEFORE the
// watcher starts that worktree's dev server. A retired path (record exists,
// directory gone) → Revive; a slimmed one (lockfile present, node_modules
// missing) → Hydrate. Must be fast and silent when there is nothing to do,
// and must not change the watcher's behaviour for worktrees the lifecycle
// does not know.
func OnThreadUnsettled(ctx context.Context, env *Env, worktreePath string) error {
	return ErrNotImplemented
}

// SessionStartHook is `conductor wt hydrate --hook`, wired as a Claude Code
// SessionStart hook in ~/.claude/settings.json: runs in the session's cwd,
// silent and fast when nothing to do, never fails the session.
func SessionStartHook(ctx context.Context, env *Env, cwd string) error {
	return ErrNotImplemented
}
