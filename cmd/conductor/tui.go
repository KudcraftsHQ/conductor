package main

import (
	"fmt"
	"os"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/tmux"
	"github.com/hammashamzah/conductor/internal/tui"
	"github.com/spf13/cobra"

	tea "github.com/charmbracelet/bubbletea"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch interactive TUI",
	Long:  "Open the interactive terminal user interface for managing projects and worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		runTUI()
		return nil
	},
}

func runTUI() {
	// If already inside conductor tmux session, run TUI directly
	if tmux.IsInsideConductorSession() {
		runTUIDirectly()
		return
	}

	// If inside different tmux session, warn and run directly
	if tmux.IsInsideTmux() {
		fmt.Println("Warning: Running inside existing tmux session. For best experience, exit and run 'conductor' directly.")
		runTUIDirectly()
		return
	}

	// Not in tmux - start conductor session (this execs, doesn't return)
	if err := tmux.StartSession(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start tmux session: %v\n", err)
		os.Exit(1)
	}
}

func runTUIDirectly() {
	cfg, err := config.Load()
	if err != nil {
		if !config.Exists() {
			fmt.Println("Conductor not initialized. Run 'conductor init' first.")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	m := tui.NewModelWithVersion(cfg, version)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
