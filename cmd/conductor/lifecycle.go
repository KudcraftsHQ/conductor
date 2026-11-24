package main

// Worktree lifecycle commands under `conductor worktree` (alias `wt`):
// ls, gc, revive, hydrate, pause, resume, config. See docs/WORKTREE-LIFECYCLE.md.
//
// All lifecycle subcommands are registered here, once, so phase-2 work only
// fills in RunE bodies in lifecycle_gc.go (agent A) and lifecycle_revive.go
// (agent B) without touching registration.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/lifecycle"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/spf13/cobra"
)

func init() {
	wtLsCmd.Flags().BoolVar(&wtLsAll, "all", false, "Include retired worktrees (records only)")
	wtLsCmd.Flags().StringVar(&wtLsState, "state", "", "Only this state: active|slim|retired-candidate|needs-you|untouchable|retired")
	wtLsCmd.Flags().BoolVar(&wtLsJSON, "json", false, "Print JSON (facts + plan per worktree)")
	wtLsCmd.Flags().BoolVar(&wtLsSize, "size", false, "Measure OWN SIZE (bytes only this worktree holds; slow)")
	wtLsCmd.Flags().BoolVar(&wtLsFresh, "fresh", false, "Bypass the 15-minute gh PR cache")

	wtPauseCmd.Flags().StringVar(&wtPauseReason, "reason", "", "Why (shown by wt ls / gc)")

	wtConfigCmd.Flags().StringVar(&wtConfigProject, "project", "", "Show or set the override for this conductor project")

	registerLifecycleGC()
	registerLifecycleRevive()

	worktreeCmd.AddCommand(wtLsCmd, wtGCCmd, wtReviveCmd, wtHydrateCmd, wtPauseCmd, wtResumeCmd, wtConfigCmd)
}

// lifecycleContext is cancelled by Ctrl-C.
func lifecycleContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

var (
	wtLsAll   bool
	wtLsState string
	wtLsJSON  bool
	wtLsSize  bool
	wtLsFresh bool
)

var wtLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List every worktree on this machine with its lifecycle state",
	Long: `Lists every git worktree on the machine — conductor's, T3's and Claude Code's
(.claude/worktrees) — discovered from git itself, with the lifecycle state
and what the next sweep would do. Repo roots are not listed. Read-only.

OWN SIZE is measured only with --size: the bytes held by files with a single
link, i.e. what removing the worktree would free (node_modules hardlinked
from bun's cache is not counted).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := lifecycleContext()
		defer cancel()
		env, err := lifecycle.NewEnv()
		if err != nil {
			return err
		}
		pass, err := lifecycle.PlanPass(ctx, env, lifecycle.PassOptions{
			Gather: lifecycle.GatherOptions{Sizes: wtLsSize, NoPRCache: wtLsFresh},
		})
		if err != nil {
			return err
		}
		var records []*lifecycle.Record
		if wtLsAll || wtLsState == string(lifecycle.StateRetired) {
			all, err := env.ListRecords()
			if err != nil {
				return err
			}
			present := map[string]bool{}
			for _, f := range pass.Facts {
				present[f.Path] = true
			}
			for _, r := range all {
				if r.RevivedAt.IsZero() && !present[r.Path] {
					records = append(records, r)
				}
			}
		}
		return printLs(pass, records, wtLsState, wtLsJSON)
	},
}

type lsRow struct {
	Facts  *lifecycle.Facts  `json:"facts,omitempty"`
	Plan   lifecycle.Plan    `json:"plan"`
	Record *lifecycle.Record `json:"record,omitempty"`
}

func printLs(pass *lifecycle.Pass, records []*lifecycle.Record, state string, asJSON bool) error {
	var rows []lsRow
	for i, f := range pass.Facts {
		if state == "" || string(pass.Plans[i].State) == state {
			rows = append(rows, lsRow{Facts: f, Plan: pass.Plans[i]})
		}
	}
	for _, r := range records {
		if state == "" || state == string(lifecycle.StateRetired) {
			rows = append(rows, lsRow{Plan: lifecycle.Plan{State: lifecycle.StateRetired, Action: lifecycle.ActionNone, Reason: r.Reason}, Record: r})
		}
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if pass.Paused {
		fmt.Printf("PAUSED since %s%s — no actions will run (conductor wt resume)\n\n",
			pass.PauseInfo.PausedAt.Local().Format("2006-01-02 15:04"), reasonSuffix(pass.PauseInfo.Reason))
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATE\tREPO\tBRANCH\tTHREAD\tPR\tGIT\tOWN SIZE\tIDLE\tPATH")
	for _, row := range rows {
		if r := row.Record; r != nil {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				row.Plan.State, repoName(r.RepoRoot, r.Remote), dash(r.Branch), threadsCount(r.T3Threads),
				prCell(r.PR), ternary(r.SnapshotSHA != "", "snapshot", "clean"), bytesCell(r.FreedBytes),
				"retired "+lifecycle.Human(time.Since(r.RetiredAt)), tildePath(r.Path))
			continue
		}
		f := row.Facts
		idle := "-"
		if last := f.LastActivity(); !last.IsZero() {
			idle = lifecycle.Human(pass.At.Sub(last))
		}
		pr := f.PRStatus()
		if f.PRErr != "" {
			pr = "?"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Plan.State, repoName(f.RepoRoot, f.Remote), dash(f.Branch), f.ThreadStatus(), pr,
			lifecycle.GitSummary(f), bytesCell(f.OwnBytes), idle, tildePath(f.Path))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(countsLine(pass, len(records)))
	return nil
}

func countsLine(pass *lifecycle.Pass, retired int) string {
	counts := pass.Counts()
	if retired > 0 {
		counts[lifecycle.StateRetired] = retired
	}
	order := []lifecycle.State{lifecycle.StateActive, lifecycle.StateSlim, lifecycle.StateRetireCandidate,
		lifecycle.StateNeedsYou, lifecycle.StateUntouchable, lifecycle.StateRetired}
	var parts []string
	for _, s := range order {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", s, counts[s]))
		}
	}
	return fmt.Sprintf("%d worktrees: %s", len(pass.Facts), strings.Join(parts, " · "))
}

func repoName(root, remote string) string {
	if remote != "" {
		if _, name, ok := strings.Cut(remote, "/"); ok {
			return name
		}
		return remote
	}
	if root == "" {
		return "-"
	}
	return root[strings.LastIndex(root, "/")+1:]
}

func prCell(pr *lifecycle.PR) string {
	if pr == nil {
		return "none"
	}
	return fmt.Sprintf("#%d %s", pr.Number, strings.ToLower(pr.State))
}

func threadsCount(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return fmt.Sprintf("%d", len(ids))
}

func bytesCell(n int64) string {
	switch {
	case n < 0:
		return "-"
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%dM", n>>20)
	default:
		return fmt.Sprintf("%dK", n>>10)
	}
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func ternary(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return " (" + r + ")"
}

// --- pause / resume ---

var wtPauseReason string

var wtPauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Stop every worktree-lifecycle action until resumed",
	Long:  "Writes the pause file (~/.conductor/lifecycle.paused). While it exists no sweep, slim, retire or automatic revive acts; ls and gc --dry-run still report.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		env, err := lifecycle.NewEnv()
		if err != nil {
			return err
		}
		if err := env.Pause(wtPauseReason); err != nil {
			return err
		}
		fmt.Printf("Worktree lifecycle paused (%s). Resume with: conductor wt resume\n", tildePath(env.PauseFile))
		return nil
	},
}

var wtResumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume worktree-lifecycle actions",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		env, err := lifecycle.NewEnv()
		if err != nil {
			return err
		}
		was, _ := env.Paused()
		if err := env.Resume(); err != nil {
			return err
		}
		if !was {
			fmt.Println("Worktree lifecycle was not paused.")
			return nil
		}
		fmt.Println("Worktree lifecycle resumed.")
		return nil
	},
}

// --- config ---

var wtConfigProject string

var wtConfigCmd = &cobra.Command{
	Use:   "config [set <key> <value> | unset <key>]",
	Short: "Show or set worktree-lifecycle settings",
	Long: `Shows the effective lifecycle settings (built-in defaults, overlaid by
defaults.lifecycle in conductor.json, overlaid by projects.<name>.lifecycle),
or sets one key. With --project the override for that project is set.

Keys: ` + strings.Join(lifecycle.SettingKeys, ", ") + `
Durations accept h/m and a day suffix: 24h, 36h, 14d.
slimAllowlist is comma-separated and replaces the default list.

  conductor wt config
  conductor wt config set retireAfter 72h
  conductor wt config set enabled false --project cutroom
  conductor wt config unset retireAfter`,
	Args: cobra.RangeArgs(0, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) == 0:
			return showLifecycleConfig()
		case args[0] == "set" && len(args) == 3:
			return setLifecycleConfig(args[1], args[2])
		case args[0] == "unset" && len(args) == 2:
			return setLifecycleConfig(args[1], "")
		}
		return fmt.Errorf("usage: conductor wt config [set <key> <value> | unset <key>] [--project <name>]")
	},
}

func showLifecycleConfig() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	env, err := lifecycle.NewEnv()
	if err != nil {
		return err
	}
	if wtConfigProject != "" {
		if _, ok := cfg.Projects[wtConfigProject]; !ok {
			return fmt.Errorf("unknown project %q", wtConfigProject)
		}
	}
	s := lifecycle.ResolveSettings(cfg, wtConfigProject)
	scope := "global"
	if wtConfigProject != "" {
		scope = "project " + wtConfigProject
	}
	fmt.Printf("Worktree lifecycle (%s):\n", scope)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  enabled\t%v\n", s.Enabled)
	fmt.Fprintf(w, "  slimAfter\t%s\n", config.Duration(s.SlimAfter))
	fmt.Fprintf(w, "  retireAfter\t%s\n", config.Duration(s.RetireAfter))
	fmt.Fprintf(w, "  needsYouAfter\t%s\n", config.Duration(s.NeedsYouAfter))
	fmt.Fprintf(w, "  abandonAfter\t%s\n", config.Duration(s.AbandonAfter))
	fmt.Fprintf(w, "  sweepInterval\t%s\n", config.Duration(s.SweepInterval))
	fmt.Fprintf(w, "  slimAllowlist\t%s\n", strings.Join(s.SlimAllowlist, ","))
	fmt.Fprintf(w, "  ignoredArchiveCapMB\t%d\n", s.IgnoredArchiveCapMB)
	fmt.Fprintf(w, "  keep\t%s\n", dash(strings.Join(s.Keep, ",")))
	_ = w.Flush()

	var overridden []string
	for name, p := range cfg.Projects {
		if p != nil && p.Lifecycle != nil {
			overridden = append(overridden, name)
		}
	}
	sort.Strings(overridden)
	if len(overridden) > 0 {
		fmt.Printf("\nProject overrides: %s\n", strings.Join(overridden, ", "))
	}
	paused, info := env.Paused()
	if paused {
		fmt.Printf("\nPAUSED since %s%s\n", info.PausedAt.Local().Format("2006-01-02 15:04"), reasonSuffix(info.Reason))
	}
	return nil
}

func setLifecycleConfig(key, value string) error {
	s, err := store.Load()
	if err != nil {
		return err
	}
	err = s.BatchMutate(func(cfg *config.Config) error {
		if wtConfigProject == "" {
			lc, err := lifecycle.SetSetting(cfg.Defaults.Lifecycle, key, value)
			if err != nil {
				return err
			}
			cfg.Defaults.Lifecycle = emptyToNil(lc)
			return nil
		}
		p, ok := cfg.Projects[wtConfigProject]
		if !ok || p == nil {
			return fmt.Errorf("unknown project %q", wtConfigProject)
		}
		lc, err := lifecycle.SetSetting(p.Lifecycle, key, value)
		if err != nil {
			return err
		}
		p.Lifecycle = emptyToNil(lc)
		return nil
	})
	if _, cerr := s.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return showLifecycleConfig()
}

// emptyToNil drops an override that no longer sets anything, so conductor.json
// stays free of empty "lifecycle": {} blocks.
func emptyToNil(c *config.LifecycleConfig) *config.LifecycleConfig {
	if c == nil {
		return nil
	}
	b, _ := json.Marshal(c)
	if string(b) == "{}" {
		return nil
	}
	return c
}
