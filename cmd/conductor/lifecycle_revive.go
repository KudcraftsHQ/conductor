package main

// PHASE 2 — agent B owns this file: fill in runWtRevive and runWtHydrate with
// lifecycle.Revive / lifecycle.Hydrate / lifecycle.SessionStartHook.
// Registration lives in lifecycle.go.

import (
	"fmt"

	"github.com/hammashamzah/conductor/internal/lifecycle"
	"github.com/spf13/cobra"
)

var (
	wtReviveNoInstall bool
	wtReviveDryRun    bool
	wtHydrateHook     bool
	wtHydrateDryRun   bool
)

var wtReviveCmd = &cobra.Command{
	Use:   "revive <thread-id|branch|path|record-id>",
	Short: "Bring a retired worktree back at its original path",
	Args:  cobra.ExactArgs(1),
	RunE:  runWtRevive,
}

var wtHydrateCmd = &cobra.Command{
	Use:   "hydrate [path]",
	Short: "Reinstall dependencies in a slimmed worktree (no-op when present)",
	Long: `Reinstalls dependencies (bun install for bun lockfiles) in a worktree the
lifecycle slimmed. Idempotent and fast when node_modules exists.

--hook is the Claude Code SessionStart mode: silent when there is nothing to
do, and never fails the session.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runWtHydrate,
}

func registerLifecycleRevive() {
	wtReviveCmd.Flags().BoolVar(&wtReviveNoInstall, "no-install", false, "Skip installing dependencies")
	wtReviveCmd.Flags().BoolVar(&wtReviveDryRun, "dry-run", false, "Show what would be restored")
	wtHydrateCmd.Flags().BoolVar(&wtHydrateHook, "hook", false, "SessionStart hook mode: silent, never fails")
	wtHydrateCmd.Flags().BoolVar(&wtHydrateDryRun, "dry-run", false, "Show what would be installed")
}

func runWtRevive(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("wt revive: %w", lifecycle.ErrNotImplemented)
}

func runWtHydrate(cmd *cobra.Command, args []string) error {
	if wtHydrateHook {
		return nil // a hook must never fail the session
	}
	return fmt.Errorf("wt hydrate: %w", lifecycle.ErrNotImplemented)
}
