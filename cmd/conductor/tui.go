package main

import (
	"fmt"
	"os"

	"github.com/hammashamzah/conductor/internal/config"
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
	cfg, err := config.Load()
	if err != nil {
		if !config.Exists() {
			fmt.Println("Conductor not initialized. Run 'conductor init' first.")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	m := tui.NewModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
