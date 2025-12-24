package main

import (
	"fmt"
	"os"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/workspace"
	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:     "pr",
	Aliases: []string{},
	Short:   "Manage GitHub pull requests",
	Long:    "Auto-setup worktrees for Claude PRs and manage PR workflows",
}

var prAutoSetupCmd = &cobra.Command{
	Use:   "auto-setup [project]",
	Short: "Auto-create worktrees for all Claude PRs",
	Long: `Fetches all PRs with "claude/*" prefix and automatically creates worktrees for them.

If no project is specified, uses the current directory's project.
Only open and draft PRs are processed.
Skips PRs that already have worktrees.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		var projectName string
		if len(args) > 0 {
			// Project name specified
			projectName = args[0]
			if _, ok := cfg.GetProject(projectName); !ok {
				return fmt.Errorf("project '%s' not found", projectName)
			}
		} else {
			// Detect current project
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}
			projectName, _, _, err = cfg.DetectProject(cwd)
			if err != nil {
				return fmt.Errorf("not in a registered project. Specify project name or run from project directory")
			}
		}

		fmt.Printf("🔍 Scanning for Claude PRs in project '%s'...\n", projectName)

		mgr := workspace.NewManager(cfg)
		result, err := mgr.AutoSetupClaudePRs(projectName)
		if err != nil {
			return fmt.Errorf("failed to auto-setup Claude PRs: %w", err)
		}

		// Display results
		fmt.Printf("\n📊 Results:\n")
		fmt.Printf("  Total PRs found: %d\n", result.TotalPRs)
		fmt.Printf("  Claude PRs (open/draft): %d\n", result.ClaudePRs)
		fmt.Printf("  New worktrees created: %d\n", len(result.NewWorktrees))
		fmt.Printf("  Existing worktrees skipped: %d\n", len(result.ExistingBranch))

		if len(result.NewWorktrees) > 0 {
			fmt.Printf("\n✅ Created worktrees:\n")
			for _, wt := range result.NewWorktrees {
				fmt.Printf("  - %s\n", wt)
			}
		}

		if len(result.ExistingBranch) > 0 {
			fmt.Printf("\n⏭️  Skipped (already exists):\n")
			for _, branch := range result.ExistingBranch {
				fmt.Printf("  - %s\n", branch)
			}
		}

		if len(result.Errors) > 0 {
			fmt.Printf("\n❌ Errors:\n")
			for _, errMsg := range result.Errors {
				fmt.Printf("  - %s\n", errMsg)
			}
		}

		if len(result.NewWorktrees) > 0 {
			fmt.Printf("\n💡 Worktrees are being set up in the background. Use 'conductor tui' to monitor progress.\n")
		}

		return nil
	},
}

func init() {
	prCmd.AddCommand(prAutoSetupCmd)
	rootCmd.AddCommand(prCmd)
}
