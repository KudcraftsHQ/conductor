package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// HydrateOptions control Hydrate.
type HydrateOptions struct {
	Exec ExecOptions
	// Quiet: the SessionStart hook — print nothing when there is nothing to
	// do, never return an error that would fail the session.
	Quiet bool
	// RequireSlimmed acts only on a worktree whose inventory entry carries a
	// slim mark newer than its last hydrate. The watcher and the SessionStart
	// hook set it, so a fresh worktree whose setup script is still installing
	// is never raced; the manual command and Revive leave it off.
	RequireSlimmed bool
	// Output receives the installer's output; nil discards it.
	Output io.Writer
}

// HydrateResult says whether deps were installed.
type HydrateResult struct {
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Command   string `json:"command,omitempty"` // e.g. "bun install"
	Skipped   string `json:"skipped,omitempty"` // why nothing ran
}

// hydrateTimeout bounds one install. Long, because a cold install of a big
// monorepo is minutes; it is also the age at which a stale hydrate lock is broken.
const hydrateTimeout = 15 * time.Minute

// installers maps a lockfile to its exact-reinstall command. bun comes first:
// when a bun lockfile is tracked it wins over any other lockfile in the tree.
var installers = []struct {
	lockfile string
	argv     []string
}{
	{"bun.lock", []string{"bun", "install"}},
	{"bun.lockb", []string{"bun", "install"}},
	{"pnpm-lock.yaml", []string{"pnpm", "install", "--frozen-lockfile"}},
	{"yarn.lock", []string{"yarn", "install", "--frozen-lockfile"}},
	{"package-lock.json", []string{"npm", "ci"}},
}

// WorktreeTop returns the top of the linked git worktree containing dir, from
// the filesystem alone (no git process, so the SessionStart hook stays fast):
// the nearest ancestor holding a `.git` FILE. A `.git` directory means a main
// checkout (a repo root), which is never hydrated: ok is false.
func WorktreeTop(dir string) (top string, ok bool) {
	dir = realpath(dir)
	for {
		info, err := os.Lstat(filepath.Join(dir, ".git"))
		if err == nil {
			if info.IsDir() {
				return dir, false // repo root
			}
			return dir, info.Mode().IsRegular()
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// depsMissing reports whether path needs an install: no root node_modules,
// or an inventory entry still marked slim (slim removed node_modules, here or
// in workspace packages, and no hydrate has succeeded since). A slim mark wins
// over a root node_modules that exists: it may be a half-finished install, and
// rerunning the installer on a complete tree is a quick no-op.
func depsMissing(path string, en *Entry) bool {
	return !isDir(filepath.Join(path, "node_modules")) || en.Slimmed()
}

// installerFor picks the install command from the tracked lockfiles at path.
// Only a lockfile git tracks counts: the reinstall must be exact.
func installerFor(ctx context.Context, path string) (lockfile string, argv []string, err error) {
	names := make([]string, len(installers))
	for i, in := range installers {
		names[i] = in.lockfile
	}
	out, err := fxGit(ctx, path, append([]string{"ls-files", "--"}, names...)...)
	if err != nil {
		return "", nil, err
	}
	tracked := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		tracked[strings.TrimSpace(l)] = true
	}
	for _, in := range installers {
		if tracked[in.lockfile] {
			return in.lockfile, in.argv, nil
		}
	}
	return "", nil, nil
}

// hasAnyLockfile is the cheap, stat-only pre-check for the hot paths.
func hasAnyLockfile(path string) bool {
	for _, in := range installers {
		if exists(filepath.Join(path, in.lockfile)) {
			return true
		}
	}
	return false
}

// Hydrate reinstalls dependencies in a slimmed worktree: idempotent and a fast
// no-op when node_modules exists. Acts only inside a linked worktree, never in
// a repo root; with RequireSlimmed only on a worktree the lifecycle slimmed.
// Uses the tracked lockfile's package manager (bun.lock/bun.lockb → bun
// install, preferred over any other). Marks the entry HydratedAt and active.
func Hydrate(ctx context.Context, env *Env, path string, o HydrateOptions) (*HydrateResult, error) {
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		path = wd
	}
	top, ok := WorktreeTop(path)
	res := &HydrateResult{Path: top}
	if !ok {
		if top == "" {
			res.Path = realpath(path)
			res.Skipped = "not inside a git worktree"
		} else {
			res.Skipped = "repository root — never hydrated"
		}
		return res, nil
	}
	if o.RequireSlimmed && !hasAnyLockfile(top) {
		res.Skipped = "no lockfile"
		return res, nil
	}

	inv, err := env.LoadInventory()
	if err != nil && o.RequireSlimmed {
		return res, err
	}
	en := inv.Get(top)
	if hydrateInFlight(top) {
		if o.RequireSlimmed {
			res.Skipped = "an install is already running"
			return res, nil
		}
		// A person asked: wait for the running install rather than call a
		// half-written node_modules "present".
		if release, err := config.AcquireLock(hydrateLockName(top), hydrateTimeout); err == nil {
			release()
		}
		if inv2, err := env.LoadInventory(); err == nil {
			en = inv2.Get(top)
		}
	}
	if !depsMissing(top, en) {
		res.Skipped = "dependencies present"
		return res, nil
	}
	if o.RequireSlimmed && !en.Slimmed() {
		res.Skipped = "not slimmed by the lifecycle"
		return res, nil
	}

	lockfile, argv, err := installerFor(ctx, top)
	if err != nil {
		return res, fmt.Errorf("read lockfiles: %w", err)
	}
	if argv == nil {
		res.Skipped = "no tracked lockfile"
		return res, nil
	}
	res.Command = strings.Join(argv, " ")
	if o.Exec.DryRun {
		res.Skipped = "dry run"
		_ = env.LogEvent(Event{Action: ActionHydrate, Path: top, Result: "dry-run", Reason: res.Command + " (" + lockfile + ")"})
		return res, nil
	}
	if paused, _ := env.Paused(); paused && o.RequireSlimmed {
		// The automatic paths stop while paused; a person asking by hand
		// (manual command, revive) still gets their dependencies back.
		return res, ErrPaused
	}

	// One install per worktree at a time: two sessions starting together, or
	// a session and the watcher, would otherwise run two installers into the
	// same node_modules. The loser waits, then finds nothing to do.
	release, err := config.AcquireLock(hydrateLockName(top), hydrateTimeout)
	if err != nil {
		return res, err
	}
	defer release()
	if inv2, err := env.LoadInventory(); err == nil {
		en = inv2.Get(top)
	}
	if !depsMissing(top, en) {
		res.Skipped = "dependencies present (installed concurrently)"
		res.Command = ""
		return res, nil
	}

	bin, err := exec.LookPath(argv[0])
	if err != nil {
		err = fmt.Errorf("%s not on PATH: %w", argv[0], err)
		_ = env.LogEvent(Event{Action: ActionHydrate, Path: top, Result: "failed", Error: err.Error()})
		return res, err
	}
	ictx, cancel := context.WithTimeout(ctx, hydrateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ictx, bin, argv[1:]...)
	cmd.Dir = top
	cmd.Stdin = nil
	var tail bytes.Buffer
	out := io.Writer(&tail)
	if o.Output != nil {
		out = io.MultiWriter(o.Output, &tail)
	}
	cmd.Stdout, cmd.Stderr = out, out
	start := env.Now()
	runErr := cmd.Run()
	if runErr != nil {
		msg := strings.TrimSpace(tail.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		err := fmt.Errorf("%s: %v %s", res.Command, runErr, msg)
		_ = env.LogEvent(Event{Action: ActionHydrate, Path: top, Result: "failed", Error: err.Error()})
		_ = env.UpdateInventory(func(inv *Inventory) error {
			e := inv.Ensure(top, env.Now().UTC())
			e.LastAction, e.LastActionAt, e.LastError = ActionHydrate, env.Now().UTC(), err.Error()
			return nil
		})
		return res, err
	}
	res.Installed = true
	now := env.Now().UTC()
	_ = env.LogEvent(Event{Action: ActionHydrate, Path: top, Result: "ok",
		Reason: fmt.Sprintf("%s (%s) in %s", res.Command, lockfile, env.Now().Sub(start).Round(time.Second))})
	if err := env.UpdateInventory(func(inv *Inventory) error {
		e := inv.Ensure(top, now)
		e.HydratedAt, e.State, e.Reason = now, StateActive, "dependencies reinstalled"
		e.LastAction, e.LastActionAt, e.LastError = ActionHydrate, now, ""
		return nil
	}); err != nil {
		return res, fmt.Errorf("installed, but could not update the inventory: %w", err)
	}
	return res, nil
}

func hydrateLockName(top string) string {
	sum := sha1.Sum([]byte(top))
	return "lifecycle-hydrate-" + hex.EncodeToString(sum[:6])
}

// hydrateInFlight reports whether another process holds top's hydrate lock,
// i.e. an install is running there right now (its node_modules is partial).
func hydrateInFlight(top string) bool {
	dir, err := config.ConductorDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, hydrateLockName(top)+".lock"))
	return err == nil
}
