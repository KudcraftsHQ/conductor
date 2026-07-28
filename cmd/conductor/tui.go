package main

import (
	"fmt"
	"os"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/mux"
	"github.com/hammashamzah/conductor/internal/tui"
	"github.com/hammashamzah/conductor/internal/tui/ipc"
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
	mx := mux.Current()

	// If already inside the conductor session, run TUI directly
	if mx.IsInsideConductorSession() {
		runTUIDirectly()
		return
	}

	// If inside a different session of the same multiplexer, warn and run directly
	if mx.IsInsideSession() {
		fmt.Printf("Warning: Running inside an existing %s session. For best experience, exit and run 'conductor' directly.\n", mx.Kind())
		runTUIDirectly()
		return
	}

	// Not in a session - start one (this execs, doesn't return)
	if err := mx.StartSession(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start session: %v\n", err)
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

	// Disable IPC notifications from the TUI's own store to prevent self-notification loops
	ipc.DisableNotifications()

	m := tui.NewModelWithVersion(cfg, version)

	// Check for session state from a previous update restart
	if state := mux.LoadSessionState(); state != nil {
		m.SetPendingRestore(state)
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Start IPC server for CLI-to-TUI notifications (from external CLI commands)
	ipcServer, err := ipc.NewServer(p)
	if err == nil {
		go ipcServer.Start()
		defer func() { _ = ipcServer.Close() }()
	}

	// Start the agent session tracker so window names get live status icons.
	m.StartSessionTracker(p)
	defer m.StopSessionTracker()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
