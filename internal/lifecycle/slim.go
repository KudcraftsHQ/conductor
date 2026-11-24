package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SlimResult is what Slim removed.
type SlimResult struct {
	Removed    []string `json:"removed"`           // relative paths deleted
	Skipped    []string `json:"skipped,omitempty"` // "path: why" (not ignored, tracked, NeverSlim…)
	FreedBytes int64    `json:"freedBytes"`
}

// Slim deletes the worktree's regenerable dirs (f.SlimTargets), each confirmed
// gitignored with `git check-ignore` and never tracked, never in NeverSlim or
// .env*. node_modules only when the lockfile is tracked. Logs, and marks the
// inventory entry SlimmedAt / SlimmedPaths.
//
// Unlike Retire, Slim does not close tmux windows: a window still open inside
// an idle tree is more likely a shell somebody left than a dev server (the
// watcher stops those once threads settle), so Slim skips instead and tries
// again next pass.
func Slim(ctx context.Context, env *Env, f *Facts, plan Plan, s Settings, o ExecOptions) (*SlimResult, error) {
	if paused, _ := env.Paused(); paused {
		return nil, ErrPaused
	}
	fresh, _, why := recheck(ctx, env, f, ActionSlim, s, o.Force)
	if why != "" {
		return nil, skipped(env, ActionSlim, f, plan.Forced, why)
	}
	if busy := busyInside(ctx, fresh.Path); busy != "" {
		return nil, skipped(env, ActionSlim, f, plan.Forced, busy)
	}

	allowed := map[string]bool{}
	for _, a := range s.SlimAllowlist {
		allowed[a] = true
	}
	for _, n := range NeverSlim {
		delete(allowed, n)
	}

	res := &SlimResult{}
	var targets []string
	for _, rel := range fresh.SlimTargets {
		if reason := slimRefusal(ctx, fresh, rel, allowed); reason != "" {
			res.Skipped = append(res.Skipped, rel+": "+reason)
			continue
		}
		targets = append(targets, rel)
	}
	sort.Strings(targets)

	if o.DryRun {
		res.Removed = targets
		_ = env.LogEvent(Event{Action: ActionSlim, Path: f.Path, Result: "dry-run",
			Reason: "would remove " + strings.Join(targets, ", "), Forced: plan.Forced})
		return res, nil
	}
	if len(targets) == 0 {
		why := "nothing slimmable"
		if len(res.Skipped) > 0 {
			why += ": " + strings.Join(res.Skipped, "; ")
		}
		return res, skipped(env, ActionSlim, f, plan.Forced, why)
	}

	var failures []string
	for _, rel := range targets {
		full := filepath.Join(fresh.Path, rel)
		freed, _ := OwnBytes(full)
		if err := os.RemoveAll(full); err != nil {
			failures = append(failures, rel+": "+err.Error())
			continue
		}
		res.Removed = append(res.Removed, rel)
		res.FreedBytes += freed
	}

	ev := Event{Action: ActionSlim, Path: f.Path, Result: "ok", FreedBytes: res.FreedBytes, Forced: plan.Forced,
		Reason: "removed " + strings.Join(res.Removed, ", ")}
	var err error
	if len(failures) > 0 {
		err = errors.New(strings.Join(failures, "; "))
		ev.Result, ev.Error = "failed", err.Error()
	}
	_ = env.LogEvent(ev)
	if len(res.Removed) > 0 {
		now := env.Now().UTC()
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		recordOutcome(env, f.Path, ActionSlim, errMsg, func(en *Entry) {
			en.SlimmedAt = now
			en.SlimmedPaths = mergePaths(en.SlimmedPaths, res.Removed)
		})
	} else if err != nil {
		recordOutcome(env, f.Path, ActionSlim, err.Error(), nil)
	}
	return res, err
}

// slimRefusal returns why rel must not be deleted, or "".
func slimRefusal(ctx context.Context, f *Facts, rel string, allowed map[string]bool) string {
	base := filepath.Base(rel)
	switch {
	case !safeRel(f.Path, rel):
		return "not a plain directory inside the worktree"
	case !allowed[base]:
		return "not on the slim allowlist"
	case strings.HasPrefix(base, ".env"):
		return "never slimmed (.env*)"
	case base == "node_modules" && !f.LockfileTracked:
		return "no tracked lockfile"
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		for _, n := range NeverSlim {
			if part == n {
				return "inside " + n
			}
		}
	}
	info, err := os.Lstat(filepath.Join(f.Path, rel))
	if err != nil || !info.IsDir() {
		return "not a directory"
	}
	// check-ignore exits 0 only when the path is ignored; tracked paths are
	// never reported as ignored.
	if _, err := fxGit(ctx, f.Path, "check-ignore", "-q", "--", rel); err != nil {
		return "not gitignored"
	}
	tracked, err := fxGit(ctx, f.Path, "ls-files", "--", rel)
	if err != nil {
		return "git ls-files failed: " + err.Error()
	}
	if tracked != "" {
		return "has tracked files"
	}
	return ""
}

func mergePaths(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range append(append([]string(nil), a...), b...) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// slimSummary is one human line for a slim result.
func slimSummary(r *SlimResult) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("removed %s (%s)", strings.Join(r.Removed, ", "), humanBytes(r.FreedBytes))
}

// humanBytes renders 1.2 GB / 340 MB / 12 KB.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
