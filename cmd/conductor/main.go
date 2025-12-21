package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.1.0"

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
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(projectCmd)
	rootCmd.AddCommand(worktreeCmd)
	rootCmd.AddCommand(portsCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(archiveCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("conductor version %s\n", version)
	},
}
