package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
)

// SweepOptions control one executed sweep (`conductor wt gc`).
type SweepOptions struct {
	Pass PassOptions
	Exec ExecOptions
	// Only limits the actions: "" (all), "slim", "retire" (retire includes
	// the needs-you snapshot that precedes it).
	Only string
	// Notify sends the ntfy summary. A needs-you snapshot is taken only by a
	// notifying sweep: telling Hammas is half of that action, and the retire
	// date counts from when he was told.
	Notify bool
}

// Sweep runs PlanPass, executes each plan's Action (Slim, NeedsYouSnapshot,
// Retire), records outcomes on the inventory, and notifies. Paused → returns
// ErrPaused before acting. Every item is independent: a failure, a skip or a
// panic on one never stops the pass.
//
// Scheduling: `conductor wt schedule install` writes a systemd user timer
// (conductor-lifecycle.timer) that runs `conductor wt gc` every
// Settings.SweepInterval. See cmd/conductor/lifecycle_gc.go.
func Sweep(ctx context.Context, env *Env, o SweepOptions) (*SweepSummary, error) {
	if paused, _ := env.Paused(); paused {
		return nil, ErrPaused
	}
	release, err := lockSweep(filepath.Join(env.ConductorDir, "lifecycle-sweep.lock"))
	if err != nil {
		return nil, err
	}
	defer release()

	pass, err := PlanPass(ctx, env, o.Pass)
	if err != nil {
		return nil, err
	}
	if pass.Paused {
		return nil, ErrPaused
	}
	sum := &SweepSummary{Pass: pass}
	if err := env.UpdateInventory(func(inv *Inventory) error {
		inv.Observe(pass.Facts, pass.Plans, pass.At)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("inventory: %w", err)
	}

	freeBefore := freeBytes(env.Home)
	var told []string // paths whose needs-you snapshot waits on the notification

items:
	for i, f := range pass.Facts {
		if ctx.Err() != nil {
			break
		}
		plan := pass.Plans[i]
		if plan.Action == ActionNone || !OnlyAllows(o.Only, plan.Action) {
			continue
		}
		if plan.Action == ActionSnapshot && !o.Notify && !o.Exec.DryRun {
			sum.Skipped = append(sum.Skipped, f.Path+": needs-you waits for a notifying sweep")
			continue
		}
		s := env.SettingsFor(f.RepoRoot)
		res, err := runItem(ctx, env, f, plan, s, o.Exec)
		var skip *SkipError
		switch {
		case errors.Is(err, ErrPaused):
			sum.Skipped = append(sum.Skipped, "paused mid-sweep; stopped")
			break items
		case errors.As(err, &skip):
			sum.Skipped = append(sum.Skipped, f.Path+": "+skip.Reason)
			continue
		case err != nil:
			line := fmt.Sprintf("%s: %s %s", f.Path, plan.Action, err)
			if f.Entry != nil && f.Entry.LastError != "" && f.Entry.LastAction == plan.Action && err.Error() == f.Entry.LastError {
				sum.FailedAgain = append(sum.FailedAgain, line)
			} else {
				sum.Failed = append(sum.Failed, line)
			}
			// A slim can remove some dirs and fail on others.
			if r, ok := res.(*SlimResult); ok && r != nil && len(r.Removed) > 0 {
				sum.Slimmed = append(sum.Slimmed, f.Path)
				sum.FreedBytes += r.FreedBytes
			}
			continue
		}
		switch r := res.(type) {
		case *SlimResult:
			sum.Slimmed = append(sum.Slimmed, f.Path)
			sum.FreedBytes += r.FreedBytes
			sum.Lines = append(sum.Lines, fmt.Sprintf("slimmed  %s: %s", f.Path, slimSummary(r)))
		case *SnapshotResult:
			ny := NeedsYou{Path: f.Path, Branch: f.Branch, Work: GitSummary(f), RetireAt: plan.RetireAt.Format("2006-01-02"), Ref: r.Ref}
			sum.NeedsYou = append(sum.NeedsYou, ny)
			told = append(told, f.Path)
			sum.Lines = append(sum.Lines, fmt.Sprintf("needs-you %s: %s → %s, retire on %s", f.Path, ny.Work, r.Ref, ny.RetireAt))
		case *Record:
			sum.Retired = append(sum.Retired, r)
			sum.FreedBytes += r.FreedBytes
			sum.Lines = append(sum.Lines, fmt.Sprintf("retired  %s: record %s, %s (%s)", f.Path, r.ID, r.GraveyardRef, humanBytes(r.FreedBytes)))
		}
	}
	if freeBefore >= 0 {
		if after := freeBytes(env.Home); after >= 0 {
			sum.DiskFreed = after - freeBefore
		}
	}

	if o.Notify && !o.Exec.DryRun && !sum.Quiet() {
		if err := Notify(ctx, env, sum); err != nil {
			sum.NotifyErr = err.Error()
		} else {
			sum.Notified = true
		}
	}
	// Each needs-you worktree is told once: mark it only once the message is
	// out. If it failed, the next sweep snapshots again and retries.
	if sum.Notified && len(told) > 0 {
		now := env.Now().UTC()
		_ = env.UpdateInventory(func(inv *Inventory) error {
			for _, p := range told {
				en := inv.Ensure(p, now)
				en.NeedsYouNotifiedAt = now
			}
			return nil
		})
	}
	return sum, nil
}

// runItem runs one executor, turning a panic into a failure so a bug on one
// worktree never ends the pass.
func runItem(ctx context.Context, env *Env, f *Facts, plan Plan, s Settings, o ExecOptions) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, firstLine(string(debug.Stack())))
			_ = env.LogEvent(Event{Action: plan.Action, Path: f.Path, Result: "failed", Error: err.Error()})
		}
	}()
	switch plan.Action {
	case ActionSlim:
		r, err := Slim(ctx, env, f, plan, s, o)
		return r, err
	case ActionSnapshot:
		r, err := NeedsYouSnapshot(ctx, env, f, plan, s, o)
		return r, err
	case ActionRetire:
		r, err := Retire(ctx, env, f, plan, s, o)
		return r, err
	}
	return nil, &SkipError{Reason: "unknown action " + string(plan.Action)}
}

// OnlyAllows reports whether --only admits an action. "retire" includes the
// needs-you snapshot, which is the first step towards retiring.
func OnlyAllows(only string, a Action) bool {
	switch only {
	case "slim":
		return a == ActionSlim
	case "retire":
		return a == ActionRetire || a == ActionSnapshot
	}
	return true
}
