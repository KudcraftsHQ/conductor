package t3watch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/lifecycle"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// The worktree lifecycle (internal/lifecycle) slims idle worktrees and
// retires finished ones. When a thread on one unsettles, the work has to be
// back before anything else touches the path: a retired worktree is revived
// at the same path, a slimmed one gets its dependencies reinstalled. That
// runs here, in the daemon, never in the decider: Decide stays a pure
// function of two snapshots.
//
// It fires on the transition to unsettled, for every thread path in T3's
// snapshot — including worktrees conductor never registered — and is a no-op
// for paths the lifecycle does not know. While it runs, the path is held out
// of Decide, so the watcher neither wakes nor starts a dev server on a
// half-restored tree; the next pass after it finishes treats the worktree as
// it always would.

// maxLifecycleHooks bounds concurrent revives/installs (a reboot with several
// unsettled, slimmed worktrees should not start them all at once).
const maxLifecycleHooks = 2

type lifecycleHooks struct {
	mu        sync.Mutex
	busy      map[string]bool // normalized path → hook in flight
	unsettled map[string]bool // paths with an unsettled thread last pass
	sem       chan struct{}
	wg        sync.WaitGroup
}

// runLifecycleHooks starts the lifecycle hook for every path that has just
// gained an unsettled thread and that the lifecycle has something to restore
// for. The check is stat-only; the restore itself runs in a goroutine.
func (w *Watcher) runLifecycleHooks(snapshot *t3.ShellSnapshot) {
	if snapshot == nil {
		return
	}
	h := &w.lc
	now := countUnsettled(snapshot)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.busy == nil {
		h.busy = make(map[string]bool)
		h.sem = make(chan struct{}, maxLifecycleHooks)
	}
	prev := h.unsettled
	h.unsettled = make(map[string]bool, len(now))
	var fresh []string
	for path := range now {
		h.unsettled[path] = true
		if !prev[path] && !h.busy[path] {
			fresh = append(fresh, path)
		}
	}
	if len(fresh) == 0 {
		return
	}
	env, err := lifecycle.NewEnv()
	if err != nil {
		return
	}
	for _, path := range fresh {
		if !lifecycle.NeedsRestore(env, path) {
			continue
		}
		h.busy[path] = true
		h.wg.Add(1)
		go w.lifecycleHook(env, path)
	}
}

func (w *Watcher) lifecycleHook(env *lifecycle.Env, path string) {
	h := &w.lc
	defer func() {
		h.mu.Lock()
		delete(h.busy, path)
		h.mu.Unlock()
		h.wg.Done()
	}()
	h.sem <- struct{}{}
	defer func() { <-h.sem }()

	// Not the loop's context: an install cut off by a watcher restart leaves
	// a half-written node_modules. Hydrate and Revive carry their own timeouts.
	line, err := lifecycle.OnThreadUnsettledWith(context.Background(), env, path, lifecycle.UnsettledOptions{
		DryRun:      w.DryRun,
		Reprovision: ReviveReprovision(w.store, false),
	})
	if err != nil {
		w.logf("lifecycle: %v", err)
		return
	}
	if line != "" {
		w.logf("lifecycle: %s", line)
	}
}

// lifecycleHeld drops the worktrees a lifecycle hook is restoring.
func (w *Watcher) lifecycleHeld(worktrees []Worktree) []Worktree {
	w.lc.mu.Lock()
	defer w.lc.mu.Unlock()
	if len(w.lc.busy) == 0 {
		return worktrees
	}
	out := make([]Worktree, 0, len(worktrees))
	for _, wt := range worktrees {
		if !w.lc.busy[normalize(wt.Path)] {
			out = append(out, wt)
		}
	}
	return out
}

// WaitLifecycle blocks until every lifecycle hook started so far has
// finished. `conductor t3 watch once` calls it so a single pass does not exit
// in the middle of a revive or an install.
func (w *Watcher) WaitLifecycle() { w.lc.wg.Wait() }

// ReviveReprovision returns the Reprovision step for lifecycle.Revive:
// ports and database come back only for a worktree that was registered with
// conductor, and only through the paths that already exist for it.
//
//   - entry gone: `conductor adopt` registers and provisions it, as T3's
//     runOnWorktreeCreate hook would have;
//   - entry archived (retire kept it): put back as hibernated, then as below;
//   - entry hibernated: with T3 threads the watcher wakes it on its next pass
//     (it is the one waker); without, and when wake is set (a person ran
//     `wt revive`), it is provisioned like `conductor worktree wake`.
func ReviveReprovision(s *store.Store, wake bool) func(ctx context.Context, rec *lifecycle.Record) (string, error) {
	return func(ctx context.Context, rec *lifecycle.Record) (string, error) {
		if s == nil {
			return "", nil
		}
		if err := s.Reload(); err != nil {
			return "", err
		}
		cfg := s.GetConfigSnapshot()
		projectName, project := projectForRoot(cfg, rec.RepoRoot)
		if project == nil {
			return "project not registered with conductor — no ports or database", nil
		}
		name, wt := worktreeByPath(project, rec.Path)
		if wt == nil {
			out, err := runAdopt(ctx, rec.Path, project.Path)
			if err != nil {
				return "", fmt.Errorf("conductor adopt: %v %s", err, out)
			}
			return "registered and provisioned through conductor adopt", nil
		}
		if wt.IsRoot {
			return "", nil
		}
		if wt.Archived {
			err := s.BatchMutate(func(c *config.Config) error {
				if p, ok := c.Projects[projectName]; ok {
					if e, ok := p.Worktrees[name]; ok {
						e.Archived, e.ArchivedAt = false, time.Time{}
						e.Hibernated, e.HibernatedAt = true, time.Now()
						e.Ports = nil
					}
				}
				return nil
			})
			if err == nil {
				err = s.ForceSave()
			}
			if err != nil {
				return "", fmt.Errorf("restore %s/%s from archived: %w", projectName, name, err)
			}
			wt.Archived, wt.Hibernated = false, true
		}
		if !wt.Hibernated {
			return fmt.Sprintf("%s/%s still provisioned", projectName, name), nil
		}
		if len(wt.T3Threads) > 0 {
			return fmt.Sprintf("%s/%s is hibernated; the T3 watcher wakes it (fresh ports and database)", projectName, name), nil
		}
		if !wake {
			return fmt.Sprintf("%s/%s is hibernated; `conductor worktree wake %s` provisions it", projectName, name, name), nil
		}
		manager := workspace.NewManagerWithStore(s.GetConfigSnapshot(), s)
		if err := manager.Provision(projectName, name); err != nil {
			return "", fmt.Errorf("provision %s/%s: %w", projectName, name, err)
		}
		return fmt.Sprintf("woke %s/%s with fresh ports and database", projectName, name), nil
	}
}

func projectForRoot(cfg *config.Config, root string) (string, *config.Project) {
	if cfg == nil {
		return "", nil
	}
	want := evalPath(root)
	for name, p := range cfg.Projects {
		if p != nil && p.Path != "" && evalPath(p.Path) == want {
			return name, p
		}
	}
	return "", nil
}

func worktreeByPath(p *config.Project, path string) (string, *config.Worktree) {
	want := evalPath(path)
	for name, wt := range p.Worktrees {
		if wt != nil && wt.Path != "" && (normalize(wt.Path) == normalize(path) || evalPath(wt.Path) == want) {
			return name, wt
		}
	}
	return "", nil
}

func evalPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return normalize(p)
}

func runAdopt(ctx context.Context, path, repo string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "adopt", "--path", path, "--repo", repo)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	return strings.TrimSpace(out.String()), err
}
