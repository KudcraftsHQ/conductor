package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/t3watch"
	"github.com/spf13/cobra"
)

var (
	t3WatchInterval     time.Duration
	t3WatchDebounce     time.Duration
	t3WatchSettle       time.Duration
	t3WatchMaxTeardowns int
	t3WatchDryRun       bool
)

// t3WatchCmd follows T3's threads and keeps conductor's worktrees in step.
//
// T3 owns the worktree lifecycle in this arrangement — you make one by starting
// a thread and dispose of one by deleting the last thread on it — and nothing
// in T3 tells conductor about either. There is a runOnWorktreeCreate hook and
// no counterpart for removal, so this loop is the only cleanup path there is.
var t3WatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Follow T3 Code threads and keep worktrees in step",
	Long: `Watches T3 Code and reconciles conductor's worktrees with it:

  at least one live thread   → active      (ports, database, dev server)
  only archived threads      → hibernated  (resources released, tree kept)
  no threads at all          → gone        (tree removed, entry dropped)

Within active, the dev server follows settling: it runs while any live thread
is unsettled, and stops once every one has stayed settled for --settle-debounce.
Unsettling a thread starts it again. The server always lives in the worktree's
tmux window; one an agent started by hand on the worktree's port is killed.

Archiving a thread is reversible: unarchiving one wakes the worktree with fresh
ports and a rebuilt database, and the working tree was never touched. Deleting
every thread on a worktree removes it — but never its branch, so commits
survive a deletion made by mistake.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		watcher, err := t3watch.New(s)
		if err != nil {
			return err
		}
		watcher.Interval = t3WatchInterval
		watcher.DryRun = t3WatchDryRun
		watcher.SetDebounce(t3WatchDebounce)
		watcher.SetSettleDebounce(t3WatchSettle)
		watcher.SetMaxTeardowns(t3WatchMaxTeardowns)

		if err := watcher.Start(); err != nil {
			return err
		}
		if t3WatchDryRun {
			fmt.Println("Watching T3 Code (dry run — nothing will be changed). Ctrl-C to stop.")
		} else {
			fmt.Printf("Watching T3 Code every %s, hibernating after %s idle. Ctrl-C to stop.\n",
				t3WatchInterval, t3WatchDebounce)
		}

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		<-signals

		fmt.Println("\nStopping the watcher...")
		watcher.Stop()
		return nil
	},
}

// t3WatchOnceCmd runs a single reconciliation pass.
//
// The same code path as the daemon, which is what makes it useful: it is how
// you see what the loop would do without leaving it running, and how a machine
// that has been offline catches up in one shot.
var t3WatchOnceCmd = &cobra.Command{
	Use:   "once",
	Short: "Run a single reconciliation pass and exit",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		watcher, err := t3watch.New(s)
		if err != nil {
			return err
		}
		watcher.DryRun = t3WatchDryRun
		watcher.SetDebounce(t3WatchDebounce)
		watcher.SetSettleDebounce(t3WatchSettle)
		watcher.SetMaxTeardowns(t3WatchMaxTeardowns)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return watcher.Tick(ctx)
	},
}

func init() {
	t3WatchCmd.Flags().DurationVar(&t3WatchInterval, "interval", t3watch.DefaultInterval,
		"How often to poll T3 Code")
	t3WatchCmd.Flags().DurationVar(&t3WatchDebounce, "debounce", t3watch.DefaultDebounce,
		"How long a worktree must have no live thread before it hibernates")
	t3WatchCmd.Flags().IntVar(&t3WatchMaxTeardowns, "max-teardowns", t3watch.DefaultMaxTeardowns,
		"Refuse to remove more than this many worktrees in one pass")
	t3WatchCmd.Flags().DurationVar(&t3WatchSettle, "settle-debounce", t3watch.DefaultSettleDebounce,
		"How long every thread on a worktree must stay settled before its dev server stops")
	t3WatchCmd.Flags().BoolVar(&t3WatchDryRun, "dry-run", false, "Report what would change without changing it")

	t3WatchOnceCmd.Flags().DurationVar(&t3WatchDebounce, "debounce", t3watch.DefaultDebounce,
		"How long a worktree must have no live thread before it hibernates")
	t3WatchOnceCmd.Flags().IntVar(&t3WatchMaxTeardowns, "max-teardowns", t3watch.DefaultMaxTeardowns,
		"Refuse to remove more than this many worktrees in one pass")
	// A single pass has no earlier tick to have started the clock, so it acts
	// on settled worktrees straight away unless told otherwise.
	t3WatchOnceCmd.Flags().DurationVar(&t3WatchSettle, "settle-debounce", 0,
		"How long every thread on a worktree must stay settled before its dev server stops")
	t3WatchOnceCmd.Flags().BoolVar(&t3WatchDryRun, "dry-run", false, "Report what would change without changing it")

	t3WatchCmd.AddCommand(t3WatchOnceCmd)
}
