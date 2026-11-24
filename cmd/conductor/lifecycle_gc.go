package main

// `conductor wt gc` (plan or execute one lifecycle sweep) and
// `conductor wt schedule` (the hourly systemd user timer that runs it).
// gc itself is registered in lifecycle.go; schedule is added here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/hammashamzah/conductor/internal/lifecycle"
	"github.com/spf13/cobra"
)

var (
	wtGCDryRun   bool
	wtGCForce    bool
	wtGCOnly     string
	wtGCJSON     bool
	wtGCSize     bool
	wtGCFresh    bool
	wtGCNoNotify bool
)

var wtGCCmd = &cobra.Command{
	Use:   "gc [path...]",
	Short: "Run one worktree-lifecycle sweep (slim, needs-you, retire)",
	Long: `Plans one sweep over every worktree (or the given paths) and executes it:
slim idle worktrees, snapshot and flag ones with unsaved work, retire ones
whose PR is merged or closed. --dry-run prints the plan and changes nothing.

--force overrides the timing thresholds, never the safety rules: nothing
with a working or pinned thread, a process inside, or an open PR is touched.`,
	RunE: runWtGC,
}

func registerLifecycleGC() {
	wtGCCmd.Flags().BoolVar(&wtGCDryRun, "dry-run", false, "Print the plan; change nothing")
	wtGCCmd.Flags().BoolVar(&wtGCForce, "force", false, "Ignore timing thresholds (never the safety rules)")
	wtGCCmd.Flags().StringVar(&wtGCOnly, "only", "", "Only this action: slim|retire")
	wtGCCmd.Flags().BoolVar(&wtGCJSON, "json", false, "Print the plan as JSON")
	wtGCCmd.Flags().BoolVar(&wtGCSize, "size", false, "Measure what each action would free (slow)")
	wtGCCmd.Flags().BoolVar(&wtGCFresh, "fresh", false, "Bypass the 15-minute gh PR cache")
	wtGCCmd.Flags().BoolVar(&wtGCNoNotify, "no-notify", false, "Send no ntfy summary (needs-you snapshots then wait for a notifying sweep)")
	registerLifecycleSchedule()
}

func runWtGC(cmd *cobra.Command, args []string) error {
	switch wtGCOnly {
	case "", "slim", "retire":
	default:
		return fmt.Errorf("--only must be slim or retire")
	}
	ctx, cancel := lifecycleContext()
	defer cancel()
	env, err := lifecycle.NewEnv()
	if err != nil {
		return err
	}
	if !wtGCDryRun {
		return runWtSweep(ctx, env, args)
	}
	pass, err := lifecycle.PlanPass(ctx, env, lifecycle.PassOptions{
		Gather: lifecycle.GatherOptions{Sizes: wtGCSize, NoPRCache: wtGCFresh},
		Force:  wtGCForce,
		Paths:  args,
	})
	if err != nil {
		return err
	}
	return printGCPlan(pass, wtGCOnly, wtGCJSON)
}

// runWtSweep executes one sweep and prints what it did. A paused lifecycle
// is not an error (the scheduled run must not show as failed while paused).
func runWtSweep(ctx context.Context, env *lifecycle.Env, args []string) error {
	sum, err := lifecycle.Sweep(ctx, env, lifecycle.SweepOptions{
		Pass: lifecycle.PassOptions{
			Gather: lifecycle.GatherOptions{Sizes: wtGCSize, NoPRCache: wtGCFresh},
			Force:  wtGCForce,
			Paths:  args,
		},
		Exec:   lifecycle.ExecOptions{Force: wtGCForce},
		Only:   wtGCOnly,
		Notify: !wtGCNoNotify,
	})
	if errors.Is(err, lifecycle.ErrPaused) {
		_, info := env.Paused()
		fmt.Printf("Paused — nothing done. %s\n", info.Reason)
		return nil
	}
	if err != nil {
		return err
	}
	if len(args) > 0 && len(sum.Pass.Facts) == 0 {
		return fmt.Errorf("no discovered worktree matches %s", strings.Join(args, " "))
	}
	if wtGCJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sum)
	}
	for _, l := range sum.Lines {
		fmt.Println(tildeAll(l))
	}
	for _, l := range sum.Skipped {
		fmt.Println("skipped  " + tildeAll(l))
	}
	for _, l := range append(append([]string{}, sum.Failed...), sum.FailedAgain...) {
		fmt.Println("FAILED   " + tildeAll(l))
	}
	fmt.Printf("\nSlimmed %d · needs-you %d · retired %d · failed %d · skipped %d · freed %s (disk %s)\n",
		len(sum.Slimmed), len(sum.NeedsYou), len(sum.Retired), len(sum.Failed)+len(sum.FailedAgain), len(sum.Skipped),
		bytesCell(sum.FreedBytes), bytesCell(max(sum.DiskFreed, 0)))
	switch {
	case sum.NotifyErr != "":
		fmt.Println("ntfy: " + sum.NotifyErr)
	case sum.Notified:
		fmt.Println("ntfy: summary sent")
	}
	return nil
}

// tildeAll shortens every home-directory path in a line.
func tildeAll(s string) string {
	if home, err := os.UserHomeDir(); err == nil {
		return strings.ReplaceAll(s, home+"/", "~/")
	}
	return s
}

func onlyAllows(only string, a lifecycle.Action) bool { return lifecycle.OnlyAllows(only, a) }

func printGCPlan(pass *lifecycle.Pass, only string, asJSON bool) error {
	type item struct {
		Path   string         `json:"path"`
		Branch string         `json:"branch,omitempty"`
		Plan   lifecycle.Plan `json:"plan"`
		Bytes  int64          `json:"ownBytes"`
	}
	var acts, holds []item
	for i, f := range pass.Facts {
		p := pass.Plans[i]
		it := item{Path: f.Path, Branch: f.Branch, Plan: p, Bytes: f.OwnBytes}
		if p.Action != lifecycle.ActionNone && onlyAllows(only, p.Action) {
			acts = append(acts, it)
		} else if p.State == lifecycle.StateNeedsYou || len(p.Blockers) > 0 {
			holds = append(holds, it)
		}
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"paused": pass.Paused, "counts": pass.Counts(), "actions": acts, "held": holds})
	}
	if pass.Paused {
		fmt.Printf("PAUSED — a real sweep would do nothing. Plan if resumed:\n\n")
	}
	fmt.Printf("Dry run: %d worktree(s) would be acted on.\n\n", len(acts))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if len(acts) > 0 {
		fmt.Fprintln(w, "ACTION\tSTATE\tSIZE\tPATH\tREASON")
		for _, it := range acts {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", it.Plan.Action, it.Plan.State, bytesCell(it.Bytes), tildePath(it.Path), it.Plan.Reason)
		}
		_ = w.Flush()
		fmt.Println()
	}
	if len(holds) > 0 {
		fmt.Println("Held back:")
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, it := range holds {
			fmt.Fprintf(w, "  %s\t%s\t%s\n", it.Plan.State, tildePath(it.Path), it.Plan.Reason)
		}
		_ = w.Flush()
		fmt.Println()
	}
	counts := pass.ActionCounts()
	fmt.Printf("%s\nWould: slim %d · needs-you snapshot %d · retire %d\n",
		countsLine(pass, 0), counts[lifecycle.ActionSlim], counts[lifecycle.ActionSnapshot], counts[lifecycle.ActionRetire])
	return nil
}
