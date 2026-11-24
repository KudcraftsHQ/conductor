package main

// Worktree lifecycle phase 2, agent B: `wt revive`, `wt hydrate` and
// `wt hooks`. revive/hydrate are registered in lifecycle.go; `wt hooks` is
// added from registerLifecycleRevive.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/lifecycle"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/t3watch"
	"github.com/spf13/cobra"
)

var (
	wtReviveNoInstall bool
	wtReviveDryRun    bool
	wtReviveJSON      bool
	wtHydrateHook     bool
	wtHydrateDryRun   bool
	wtHooksSettings   string
	wtHooksCommand    string
	wtHooksDryRun     bool
)

var wtReviveCmd = &cobra.Command{
	Use:   "revive <thread-id|branch|path|record-id>",
	Short: "Bring a retired worktree back at its original path",
	Long: `Recreates a retired worktree at exactly the path it had, so a T3 thread on it
continues unchanged:

  - git worktree add <path> <branch> (the branch is recreated at the recorded
    HEAD if it was deleted; a detached HEAD comes back detached);
  - a needs-you snapshot is restored as uncommitted changes, untracked files
    included;
  - the archived ignored files (.env and the like) are put back;
  - dependencies are installed (bun install for bun lockfiles) unless
    --no-install;
  - ports and the dev database come back only if the worktree was registered
    with conductor, through the adopt / wake path.

Reviving something already present is a no-op. The T3 watcher does all of
this by itself when a thread on a retired worktree unsettles.`,
	Args: cobra.ExactArgs(1),
	RunE: runWtRevive,
}

var wtHydrateCmd = &cobra.Command{
	Use:   "hydrate [path]",
	Short: "Reinstall dependencies in a slimmed worktree (no-op when present)",
	Long: `Reinstalls dependencies in a worktree the lifecycle slimmed, with the tracked
lockfile's package manager (bun install whenever a bun lockfile is tracked).
Idempotent, and a fast no-op when node_modules is there. Never runs in a
repository root.

--hook is the Claude Code SessionStart mode (see 'conductor wt hooks'): it
acts only inside a worktree the inventory marks slimmed, prints nothing when
there is nothing to do, never exits non-zero, and leaves a long install
running in the background rather than holding the session.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runWtHydrate,
}

var wtHooksCmd = &cobra.Command{
	Use:   "hooks <install|uninstall|status>",
	Short: "Manage the Claude Code SessionStart hook that rehydrates slimmed worktrees",
	Long: `Adds, removes or reports a SessionStart command hook in Claude Code's
settings.json that runs 'conductor wt hydrate --hook', so a session opened in
a slimmed worktree gets its dependencies back.

Every other key in settings.json is kept as it was, and the file is backed up
(settings.json.conductor-backup-<time>) before it is written.

The file is --settings, else $CONDUCTOR_CLAUDE_SETTINGS, else
$CLAUDE_CONFIG_DIR/settings.json, else ~/.claude/settings.json.`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"install", "uninstall", "status"},
	RunE:      runWtHooks,
}

func registerLifecycleRevive() {
	wtReviveCmd.Flags().BoolVar(&wtReviveNoInstall, "no-install", false, "Skip installing dependencies")
	wtReviveCmd.Flags().BoolVar(&wtReviveDryRun, "dry-run", false, "Show what would be restored")
	wtReviveCmd.Flags().BoolVar(&wtReviveJSON, "json", false, "Print the result as JSON")
	wtHydrateCmd.Flags().BoolVar(&wtHydrateHook, "hook", false, "SessionStart hook mode: silent, never fails")
	wtHydrateCmd.Flags().BoolVar(&wtHydrateDryRun, "dry-run", false, "Show what would be installed")
	wtHooksCmd.Flags().StringVar(&wtHooksSettings, "settings", "", "settings.json to edit (default ~/.claude/settings.json)")
	wtHooksCmd.Flags().StringVar(&wtHooksCommand, "command", "", "Hook command to write (default: this conductor + 'wt hydrate --hook')")
	wtHooksCmd.Flags().BoolVar(&wtHooksDryRun, "dry-run", false, "Report the change without writing")
	worktreeCmd.AddCommand(wtHooksCmd)
}

func runWtRevive(cmd *cobra.Command, args []string) error {
	ctx, cancel := lifecycleContext()
	defer cancel()
	env, err := lifecycle.NewEnv()
	if err != nil {
		return err
	}
	o := lifecycle.ReviveOptions{
		Exec:      lifecycle.ExecOptions{DryRun: wtReviveDryRun},
		NoInstall: wtReviveNoInstall,
	}
	if !wtReviveJSON {
		o.Output = os.Stderr
	}
	if !wtReviveDryRun {
		s, err := store.Load()
		if err == nil {
			defer func() { _, _ = s.Close() }()
			o.Reprovision = t3watch.ReviveReprovision(s, true)
		}
	}
	res, err := lifecycle.Revive(ctx, env, args[0], o)
	if wtReviveJSON && res != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	}
	if err != nil {
		return fmt.Errorf("wt revive: %w", err)
	}
	if wtReviveJSON {
		return nil
	}
	rec := res.Record
	if res.NoOp {
		fmt.Printf("%s is already present — nothing to do (%s)\n", tildePath(res.Path), strings.Join(res.Notes, "; "))
		return nil
	}
	switch {
	case res.DryRun:
		fmt.Printf("Would revive %s (record %s)\n", tildePath(res.Path), rec.ID)
	case res.AlreadyPresent:
		fmt.Printf("%s was already on disk; restored what was missing (record %s)\n", tildePath(res.Path), rec.ID)
	default:
		fmt.Printf("Revived %s (record %s)\n", tildePath(res.Path), rec.ID)
	}
	if res.Checkout != "" {
		fmt.Printf("  checkout:   %s\n", res.Checkout)
	}
	if res.SnapshotApplied {
		fmt.Printf("  snapshot:   %s restored as uncommitted changes\n", shortSHA(rec.SnapshotSHA))
	}
	if len(res.Restored) > 0 {
		fmt.Printf("  ignored:    %d file(s) restored (%s)\n", len(res.Restored), previewList(res.Restored, 4))
	}
	if res.Installed {
		fmt.Printf("  deps:       installed\n")
	}
	if res.Provisioned != "" {
		fmt.Printf("  conductor:  %s\n", res.Provisioned)
	}
	for _, n := range res.Notes {
		fmt.Printf("  note:       %s\n", n)
	}
	return nil
}

func runWtHydrate(cmd *cobra.Command, args []string) error {
	if wtHydrateHook {
		runHydrateHook()
		return nil // a hook must never fail the session
	}
	ctx, cancel := lifecycleContext()
	defer cancel()
	env, err := lifecycle.NewEnv()
	if err != nil {
		return err
	}
	path := ""
	if len(args) == 1 {
		path = args[0]
	}
	res, err := lifecycle.Hydrate(ctx, env, path, lifecycle.HydrateOptions{
		Exec:   lifecycle.ExecOptions{DryRun: wtHydrateDryRun},
		Output: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("wt hydrate: %w", err)
	}
	switch {
	case res.Installed:
		fmt.Printf("Installed dependencies in %s (%s)\n", tildePath(res.Path), res.Command)
	case wtHydrateDryRun && res.Command != "":
		fmt.Printf("Would run %s in %s\n", res.Command, tildePath(res.Path))
	default:
		fmt.Printf("Nothing to do in %s: %s\n", tildePath(res.Path), res.Skipped)
	}
	return nil
}

// runHydrateHook is the SessionStart hook. Any failure is swallowed: a broken
// conductor must never stop a Claude Code session from starting.
func runHydrateHook() {
	defer func() { _ = recover() }()
	cwd := hookCwd()
	if cwd == "" {
		return
	}
	env, err := lifecycle.NewEnv()
	if err != nil {
		return
	}
	ctx, cancel := lifecycleContext()
	defer cancel()
	_ = lifecycle.SessionStartHook(ctx, env, cwd)
}

// hookCwd is the session's cwd: Claude Code passes it as JSON on stdin
// ({"cwd": …}); the process's own working directory is the fallback. Stdin is
// read only when it is a pipe, and never for more than a moment.
func hookCwd() string {
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
		ch := make(chan []byte, 1)
		go func() {
			b, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
			ch <- b
		}()
		select {
		case b := <-ch:
			var in struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(b, &in) == nil && in.Cwd != "" {
				return in.Cwd
			}
		case <-time.After(time.Second):
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func runWtHooks(cmd *cobra.Command, args []string) error {
	env, err := lifecycle.NewEnv()
	if err != nil {
		return err
	}
	path := wtHooksSettings
	if path == "" {
		path = env.ClaudeSettingsPath()
	}
	var st *lifecycle.HookStatus
	switch args[0] {
	case "status":
		st, err = lifecycle.SessionHookStatus(path)
		if err != nil {
			return err
		}
		if st.Installed {
			fmt.Printf("Installed in %s\n  SessionStart: %s\n", tildePath(path), st.Command)
		} else {
			fmt.Printf("Not installed in %s\n", tildePath(path))
		}
		return nil
	case "install":
		command := wtHooksCommand
		if command == "" {
			command = lifecycle.HookCommand()
		}
		st, err = lifecycle.InstallSessionHook(path, command, wtHooksDryRun)
	case "uninstall":
		st, err = lifecycle.UninstallSessionHook(path, wtHooksDryRun)
	default:
		return fmt.Errorf("unknown action %q (install, uninstall or status)", args[0])
	}
	if err != nil {
		return err
	}
	verb := map[string]string{"install": "Installed", "uninstall": "Removed"}[args[0]]
	switch {
	case !st.Changed && args[0] == "install":
		fmt.Printf("Already installed in %s\n  SessionStart: %s\n", tildePath(path), st.Command)
	case !st.Changed:
		fmt.Printf("Not installed in %s — nothing to remove\n", tildePath(path))
	case wtHooksDryRun:
		fmt.Printf("Would update %s (%s)\n", tildePath(path), strings.ToLower(verb))
	default:
		fmt.Printf("%s the SessionStart hook in %s\n", verb, tildePath(path))
		if st.Command != "" {
			fmt.Printf("  SessionStart: %s\n", st.Command)
		}
		if st.Backup != "" {
			fmt.Printf("  backup:       %s\n", tildePath(st.Backup))
		}
	}
	return nil
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func previewList(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(", +%d more", len(items)-n)
}
