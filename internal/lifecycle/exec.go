package lifecycle

import "errors"

// ErrNotImplemented is returned by the phase-2 stubs until they are filled in.
var ErrNotImplemented = errors.New("not implemented yet (worktree lifecycle phase 2)")

// ExecOptions are shared by every executor.
type ExecOptions struct {
	// DryRun: report what would happen, change nothing, log result "dry-run".
	DryRun bool
	// Force was given on the command line; the Plan already reflects it.
	// Executors never use it to skip a safety re-check.
	Force bool
}

// Executors must, in order:
//  1. return ErrPaused when env.Paused() (re-read at act time, not plan time);
//  2. re-check what may have changed since the plan: ProcsInside after
//     closing DevWindows, Head, Dirty/Untracked/Unpushed, thread Working();
//     on any change, skip with a reason rather than act on stale facts;
//  3. env.LogEvent before returning, for ok, skipped and failed alike;
//  4. record the outcome on the inventory entry (env.UpdateInventory).

// ErrPaused is returned by executors while the pause file exists.
var ErrPaused = errors.New("worktree lifecycle is paused (conductor wt resume)")
