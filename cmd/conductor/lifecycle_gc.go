package main

// PHASE 2 — agent A owns this file: fill in the non-dry-run branch of
// runWtGC with lifecycle.Sweep. Registration lives in lifecycle.go.

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/hammashamzah/conductor/internal/lifecycle"
	"github.com/spf13/cobra"
)

var (
	wtGCDryRun bool
	wtGCForce  bool
	wtGCOnly   string
	wtGCJSON   bool
	wtGCSize   bool
	wtGCFresh  bool
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
}

func runWtGC(cmd *cobra.Command, args []string) error {
	switch wtGCOnly {
	case "", "slim", "retire":
	default:
		return fmt.Errorf("--only must be slim or retire")
	}
	if !wtGCDryRun {
		return fmt.Errorf("wt gc: %w; use --dry-run to see the plan", lifecycle.ErrNotImplemented)
	}
	ctx, cancel := lifecycleContext()
	defer cancel()
	env, err := lifecycle.NewEnv()
	if err != nil {
		return err
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

// onlyAllows reports whether --only admits an action. "retire" includes the
// needs-you snapshot, which is the first step towards retiring.
func onlyAllows(only string, a lifecycle.Action) bool {
	switch only {
	case "slim":
		return a == lifecycle.ActionSlim
	case "retire":
		return a == lifecycle.ActionRetire || a == lifecycle.ActionSnapshot
	}
	return true
}

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
