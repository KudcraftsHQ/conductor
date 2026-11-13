package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/mux"
	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/spf13/cobra"
)

var t3ContextAll bool

var t3ContextCmd = &cobra.Command{
	Use:   "context",
	Short: "Rewrite the agent context file and its auto-load shims",
	Long: `Rewrite .conductor-context.md and the files that make agents load it
(CLAUDE.local.md), and list them all in the repository's git exclude.

Adopt, provision and wake already do this. Run it to bring worktrees provisioned
before the shims existed up to date, or after editing conductor.json by hand.
It only writes into worktrees T3 is hosting, and it is idempotent.

  conductor t3 context          the worktree containing the working directory
  conductor t3 context --all    every T3-hosted worktree`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if !t3ContextAll {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			project, _, wt, err := cfg.DetectProject(cwd)
			if err != nil || wt == nil {
				return fmt.Errorf("%s is not inside a conductor worktree — pass --all to rewrite every one", cwd)
			}
			if !t3.HasMarker(wt.Path) {
				return fmt.Errorf("%s is not hosted by T3 — nothing to write", wt.Path)
			}
			if err := mux.WriteT3AgentContext(wt.Path, project, wt.Branch, wt.Ports, wt.DatabaseName); err != nil {
				return err
			}
			fmt.Printf("Wrote %s\n", wt.Path)
			return nil
		}

		written, failed := 0, 0
		for _, target := range t3ContextTargets(cfg) {
			wt := target.worktree
			if err := mux.WriteT3AgentContext(wt.Path, target.project, wt.Branch, wt.Ports, wt.DatabaseName); err != nil {
				fmt.Fprintf(os.Stderr, "%s/%s: %v\n", target.project, target.name, err)
				failed++
				continue
			}
			fmt.Printf("%s/%s  %s\n", target.project, target.name, wt.Path)
			written++
		}
		fmt.Printf("Wrote %d worktree(s)", written)
		if failed > 0 {
			fmt.Printf(", %d failed\n", failed)
			return fmt.Errorf("%d worktree(s) could not be written", failed)
		}
		fmt.Println()
		return nil
	},
}

type t3ContextTarget struct {
	project, name string
	worktree      *config.Worktree
}

// t3ContextTargets is every worktree --all writes into: not the repository
// root, not archived, still on disk, and carrying T3's marker. The marker is
// the filter that matters — a worktree made under tmux or herdr gets its prompt
// on the agent's command line and must not grow files it never asked for.
func t3ContextTargets(cfg *config.Config) []t3ContextTarget {
	var targets []t3ContextTarget
	for projectName, project := range cfg.Projects {
		for name, wt := range project.Worktrees {
			if wt == nil || wt.IsRoot || wt.Archived {
				continue
			}
			if _, err := os.Stat(wt.Path); err != nil {
				continue
			}
			if !t3.HasMarker(wt.Path) {
				continue
			}
			targets = append(targets, t3ContextTarget{project: projectName, name: name, worktree: wt})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].project != targets[j].project {
			return targets[i].project < targets[j].project
		}
		return targets[i].name < targets[j].name
	})
	return targets
}

func init() {
	t3ContextCmd.Flags().BoolVar(&t3ContextAll, "all", false, "Rewrite every T3-hosted worktree")
	t3Cmd.AddCommand(t3ContextCmd)
}
