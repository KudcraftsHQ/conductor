package lifecycle

// Helpers shared by the phase-2 executors (slim, snapshot, retire, sweep).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gitRun runs `git -C dir args...` with a timeout and extra environment, for
// the executors' writes (snapshot, worktree remove) that fxGit's read-only,
// 30-second shape does not fit.
func gitRun(ctx context.Context, dir string, timeout time.Duration, extraEnv []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"), extraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(msg))
	}
	return strings.TrimSpace(out.String()), nil
}

// refreshFacts re-reads one worktree's facts right before acting, so an
// executor never acts on a plan made from stale facts.
func refreshFacts(ctx context.Context, env *Env, f *Facts) (*Facts, error) {
	inv, err := env.LoadInventory()
	if err != nil {
		return nil, err
	}
	fresh := Gather(ctx, env, []Candidate{f.Candidate}, inv, GatherOptions{})
	if len(fresh) != 1 || fresh[0] == nil {
		return nil, fmt.Errorf("could not re-read facts for %s", f.Path)
	}
	return fresh[0], nil
}

// recheck re-reads the facts and re-classifies. It returns the fresh facts
// and plan, and a non-empty reason when the executor must skip: HEAD moved,
// the work changed, or the fresh plan no longer asks for this action (a
// thread woke, a process started, a PR reopened, the lifecycle was disabled).
func recheck(ctx context.Context, env *Env, f *Facts, want Action, s Settings, force bool) (*Facts, Plan, string) {
	fresh, err := refreshFacts(ctx, env, f)
	if err != nil {
		return nil, Plan{}, err.Error()
	}
	switch {
	case fresh.Missing:
		return fresh, Plan{}, "directory is gone"
	case fresh.Head != f.Head:
		return fresh, Plan{}, fmt.Sprintf("HEAD moved (%s → %s)", short(f.Head), short(fresh.Head))
	case fresh.Dirty != f.Dirty || fresh.Untracked != f.Untracked || fresh.Unpushed != f.Unpushed:
		return fresh, Plan{}, fmt.Sprintf("work changed since the plan (%s → %s)", GitSummary(f), GitSummary(fresh))
	}
	plan := Classify(fresh, s, env.Now(), ClassifyOptions{Force: force})
	if plan.Action != want {
		return fresh, plan, fmt.Sprintf("now %s: %s", plan.State, plan.Reason)
	}
	return fresh, plan, ""
}

func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	if sha == "" {
		return "none"
	}
	return sha
}

// busyInside re-reads the process table and reports what still has its cwd
// inside path, tmux panes included. "" means nothing.
func busyInside(ctx context.Context, path string) string {
	t := readProcTable(ctx)
	if t.unknown {
		return "process table unreadable"
	}
	probe := &Facts{Candidate: Candidate{Path: path}}
	t.inspect(probe)
	var parts []string
	for _, w := range probe.DevWindows {
		parts = append(parts, fmt.Sprintf("tmux window %s (%s)", w.ID, w.Name))
	}
	for _, p := range probe.ProcsInside {
		parts = append(parts, fmt.Sprintf("pid %d (%s)", p.PID, p.Comm))
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) > 3 {
		parts = append(parts[:3], fmt.Sprintf("+%d", len(parts)-3))
	}
	return "in use by " + strings.Join(parts, ", ")
}

// closeDevWindows closes every tmux window with a pane inside the worktree —
// conductor's dev server and its restart loop — the way the interim
// t3-stale-worktrees script does: kill the window, then TERM each pane's
// children and the pane process (the loop traps INT/TERM). Left running, a dev
// server rewrites caches into the tree while git removes it.
func closeDevWindows(ctx context.Context, wins []TmuxWindow) []string {
	var closed []string
	for _, w := range wins {
		_, _ = fxRun(ctx, "", "tmux", "kill-window", "-t", w.ID)
		for _, pid := range w.PanePIDs {
			_, _ = fxRun(ctx, "", "pkill", "-TERM", "-P", strconv.Itoa(pid))
			terminate(pid)
		}
		closed = append(closed, w.Name)
	}
	if len(closed) > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
	return closed
}

// safeRel reports whether rel is a clean relative path that stays inside
// root and whose every component is a real directory (no symlink escapes).
func safeRel(root, rel string) bool {
	if rel == "" || filepath.IsAbs(rel) || filepath.Clean(rel) != rel || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	full := filepath.Join(root, rel)
	real, err := filepath.EvalSymlinks(full)
	return err == nil && real == full
}

// recordOutcome stores an executor's outcome on the inventory entry.
func recordOutcome(env *Env, path string, action Action, errMsg string, fn func(en *Entry)) {
	now := env.Now().UTC()
	_ = env.UpdateInventory(func(inv *Inventory) error {
		en := inv.Ensure(path, now)
		en.LastAction, en.LastActionAt, en.LastError = action, now, errMsg
		if fn != nil {
			fn(en)
		}
		return nil
	})
}

// skipped logs a skip and returns it as an error the sweep reports as skipped.
func skipped(env *Env, action Action, f *Facts, forced bool, why string) error {
	_ = env.LogEvent(Event{Action: action, Path: f.Path, Result: "skipped", Reason: why, Forced: forced})
	return &SkipError{Reason: why}
}

// SkipError is returned by an executor that chose not to act; it is not a failure.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return "skipped: " + e.Reason }
