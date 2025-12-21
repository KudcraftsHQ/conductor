package main

import (
	"fmt"
	"os"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/runner"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Run the setup script",
	Long:  "Executes the setup script for the current worktree",
	RunE:  runScript("setup"),
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the dev server",
	Long:  "Executes the run script for the current worktree",
	RunE:  runScript("run"),
}

var archiveCmd = &cobra.Command{
	Use:   "archive-script",
	Short: "Run the archive script",
	Long:  "Executes the archive script (cleanup) for the current worktree",
	RunE:  runScript("archive"),
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current worktree status",
	Long:  "Display environment and port information for the current worktree",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, worktree, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		// Find worktree name
		var wtName string
		for name, wt := range project.Worktrees {
			if wt == worktree {
				wtName = name
				break
			}
		}

		fmt.Printf("Project: %s\n", projectName)
		fmt.Printf("Worktree: %s\n", wtName)
		fmt.Printf("Path: %s\n", worktree.Path)
		fmt.Printf("Branch: %s\n", worktree.Branch)
		fmt.Printf("Ports: %v\n", worktree.Ports)

		// Show environment variables
		projCfg, _ := config.LoadProjectConfig(project.Path)
		envMap := runner.GetEnvMap(projectName, project, wtName, worktree, projCfg)

		fmt.Println("\nEnvironment Variables:")
		for k, v := range envMap {
			fmt.Printf("  %s=%s\n", k, v)
		}

		// Show available scripts
		r := runner.NewRunner(cfg)
		scripts := r.ListScripts(project.Path)
		if len(scripts) > 0 {
			fmt.Println("\nAvailable Scripts:")
			for _, s := range scripts {
				fmt.Printf("  - %s\n", s)
			}
		}

		return nil
	},
}

func runScript(scriptName string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, worktree, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		// Find worktree name
		var wtName string
		for name, wt := range project.Worktrees {
			if wt == worktree {
				wtName = name
				break
			}
		}

		r := runner.NewRunner(cfg)
		if !r.HasScript(project.Path, scriptName) {
			return fmt.Errorf("script '%s' not found. Create it in conductor.json or .conductor-scripts/%s.sh", scriptName, scriptName)
		}

		return r.Run(projectName, wtName, scriptName)
	}
}
