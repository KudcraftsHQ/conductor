package main

import (
	"fmt"
	"os"

	"github.com/hammashamzah/conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var version = "0.12.25.6"

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "conductor",
	Short: "Manage git worktrees with isolated development environments",
	Long: `Conductor is a CLI tool for managing git worktrees across multiple projects
with port isolation and environment management.

When run without subcommands, it launches an interactive TUI.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Default to TUI when no subcommand is given
		runTUI()
	},
}

func init() {
	cobra.OnInitialize(checkDependencies)

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(projectCmd)
	rootCmd.AddCommand(worktreeCmd)
	rootCmd.AddCommand(portsCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(archiveCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(tunnelCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(migrateCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("conductor version %s\n", version)
	},
}

func checkDependencies() {
	if err := tmux.CheckInstalled(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, tmux.TmuxInstallGuide())
		os.Exit(1)
	}
}
