package lifecycle

import "context"

// PHASE 2 — agent A owns this file.

// SweepOptions control one executed sweep (`conductor wt gc`).
type SweepOptions struct {
	Pass PassOptions
	Exec ExecOptions
	// Only limits the actions: "" (all), "slim", "retire" (retire includes
	// the needs-you snapshot that precedes it).
	Only string
	// Notify sends the ntfy summary (the scheduled sweep sets it).
	Notify bool
}

// Sweep runs PlanPass, executes each plan's Action (Slim, Snapshot, Retire),
// records outcomes with Inventory.Observe + per-entry fields, saves the
// inventory, and notifies. Paused → returns ErrPaused before acting.
//
// Scheduling (also agent A): run Sweep every Settings.SweepInterval from the
// long-running `conductor t3 watch` daemon, or a systemd user timer
// conductor-lifecycle.timer — and at cutover disable the interim
// t3-stale-worktrees-report.timer and t3-stale-worktrees-listen.service.
func Sweep(ctx context.Context, env *Env, o SweepOptions) (*SweepSummary, error) {
	return nil, ErrNotImplemented
}
