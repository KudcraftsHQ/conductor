package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/tunnel"
	"github.com/spf13/cobra"
)

var tunnelCmd = &cobra.Command{
	Use:   "tunnel",
	Short: "Manage Cloudflare tunnels",
	Long:  "Start, stop, and manage Cloudflare tunnels for worktree dev servers",
}

var (
	tunnelStartPort  int
	tunnelStartNamed bool
)

var tunnelStartCmd = &cobra.Command{
	Use:   "start <worktree>",
	Short: "Start a tunnel for a worktree",
	Long:  "Start a Cloudflare tunnel to expose a worktree's dev server",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project. Run 'conductor project add .' first")
		}

		wtName := args[0]
		wt, ok := s.GetWorktree(projectName, wtName)
		if !ok {
			return fmt.Errorf("worktree '%s' not found", wtName)
		}

		// Determine port
		port := tunnelStartPort
		if port == 0 && len(wt.Ports) > 0 {
			port = wt.Ports[0]
		}
		if port == 0 {
			return fmt.Errorf("no port specified and worktree has no allocated ports. Use --port flag")
		}

		mgr := tunnel.NewManager(cfg)
		defer func() { _ = mgr.Close() }()

		var state *config.TunnelState
		if tunnelStartNamed {
			// Load project config for named tunnel settings
			projectConfig, _ := config.LoadProjectConfig(project.Path)
			state, err = mgr.StartNamedTunnel(projectName, wtName, port, projectConfig)
		} else {
			state, err = mgr.StartQuickTunnel(projectName, wtName, port)
		}

		if err != nil {
			return fmt.Errorf("failed to start tunnel: %w", err)
		}

		// Update worktree tunnel state via store
		if err := s.SetTunnelState(projectName, wtName, state); err != nil {
			return fmt.Errorf("failed to update tunnel state: %w", err)
		}

		fmt.Printf("Tunnel started for %s\n", wtName)
		fmt.Printf("  URL: %s\n", state.URL)
		fmt.Printf("  Port: %d\n", state.Port)
		fmt.Printf("  Mode: %s\n", state.Mode)
		fmt.Printf("  PID: %d\n", state.PID)

		return nil
	},
}

var tunnelStopCmd = &cobra.Command{
	Use:   "stop <worktree>",
	Short: "Stop a tunnel for a worktree",
	Long:  "Stop a running Cloudflare tunnel for a worktree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, _, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		wtName := args[0]
		if !s.WorktreeExists(projectName, wtName) {
			return fmt.Errorf("worktree '%s' not found", wtName)
		}

		mgr := tunnel.NewManager(cfg)
		defer func() { _ = mgr.Close() }()

		if err := mgr.StopTunnel(projectName, wtName); err != nil {
			return fmt.Errorf("failed to stop tunnel: %w", err)
		}

		// Clear worktree tunnel state via store
		if err := s.ClearTunnelState(projectName, wtName); err != nil {
			return fmt.Errorf("failed to clear tunnel state: %w", err)
		}

		fmt.Printf("Tunnel stopped for %s\n", wtName)
		return nil
	},
}

var tunnelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active tunnels",
	Long:  "List all active tunnels for the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, _, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		fmt.Printf("Tunnels for %s:\n\n", projectName)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "WORKTREE\tMODE\tPORT\tURL\tPID")
		_, _ = fmt.Fprintln(w, "--------\t----\t----\t---\t---")

		count := 0
		worktrees := s.GetAllWorktrees(projectName)
		for name, wt := range worktrees {
			if wt.Tunnel != nil && wt.Tunnel.Active {
				// Verify process is still running
				if tunnel.IsProcessRunning(wt.Tunnel.PID) {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%d\n",
						name, wt.Tunnel.Mode, wt.Tunnel.Port, wt.Tunnel.URL, wt.Tunnel.PID)
					count++
				}
			}
		}
		_ = w.Flush()

		if count == 0 {
			fmt.Println("No active tunnels.")
		}

		return nil
	},
}

var tunnelStatusCmd = &cobra.Command{
	Use:   "status <worktree>",
	Short: "Show tunnel status for a worktree",
	Long:  "Show detailed tunnel status for a worktree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, _, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		wtName := args[0]
		wt, ok := s.GetWorktree(projectName, wtName)
		if !ok {
			return fmt.Errorf("worktree '%s' not found", wtName)
		}

		fmt.Printf("Tunnel status for %s/%s:\n\n", projectName, wtName)

		if wt.Tunnel == nil || !wt.Tunnel.Active {
			fmt.Println("No active tunnel.")
			return nil
		}

		// Verify process is running
		running := tunnel.IsProcessRunning(wt.Tunnel.PID)

		fmt.Printf("  Active: %t\n", running)
		fmt.Printf("  Mode: %s\n", wt.Tunnel.Mode)
		fmt.Printf("  Port: %d\n", wt.Tunnel.Port)
		fmt.Printf("  URL: %s\n", wt.Tunnel.URL)
		fmt.Printf("  PID: %d\n", wt.Tunnel.PID)
		fmt.Printf("  Started: %s\n", wt.Tunnel.StartedAt.Format("2006-01-02 15:04:05"))

		if !running {
			fmt.Println("\nWarning: Tunnel process is not running. State may be stale.")
		}

		return nil
	},
}

var tunnelLogsCmd = &cobra.Command{
	Use:   "logs <worktree>",
	Short: "Show tunnel logs",
	Long:  "Show recent logs from a worktree's tunnel process",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, _, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		wtName := args[0]
		if !s.WorktreeExists(projectName, wtName) {
			return fmt.Errorf("worktree '%s' not found", wtName)
		}

		mgr := tunnel.NewManager(cfg)
		defer func() { _ = mgr.Close() }()

		logs := mgr.GetLogs(projectName, wtName)
		if len(logs) == 0 {
			fmt.Println("No tunnel logs available.")
			return nil
		}

		fmt.Printf("Tunnel logs for %s/%s:\n\n", projectName, wtName)
		for _, line := range logs {
			fmt.Println(line)
		}

		return nil
	},
}

var tunnelSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup cloudflared for named tunnels",
	Long:  "Guide to setup cloudflared authentication for named tunnels",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := tunnel.NewCloudflaredCLI()

		// Check if cloudflared is installed
		if !cli.IsInstalled() {
			fmt.Println("cloudflared is not installed.")
			fmt.Println("")
			fmt.Println("Install with:")
			fmt.Println("  brew install cloudflared")
			fmt.Println("")
			return nil
		}

		fmt.Println("cloudflared is installed.")
		fmt.Println("")

		// Check if authenticated
		if cli.IsAuthenticated() {
			fmt.Println("cloudflared is authenticated.")
			fmt.Println("")
			fmt.Println("You're all set! To use named tunnels, just configure your domain:")
			fmt.Println("")
			fmt.Println("  Add to ~/.conductor/conductor.json:")
			fmt.Println("  {")
			fmt.Println("    \"defaults\": {")
			fmt.Println("      \"tunnel\": {")
			fmt.Println("        \"domain\": \"your-domain.com\"")
			fmt.Println("      }")
			fmt.Println("    }")
			fmt.Println("  }")
			fmt.Println("")
			fmt.Println("  Or per-project in conductor.json:")
			fmt.Println("  {")
			fmt.Println("    \"tunnel\": {")
			fmt.Println("      \"domain\": \"your-domain.com\"")
			fmt.Println("    }")
			fmt.Println("  }")
			fmt.Println("")
			fmt.Println("Named tunnel URLs follow the pattern: <worktree>-<port>.<domain>")
			fmt.Println("Example: tokyo-3100.your-domain.com")
		} else {
			fmt.Println("cloudflared is NOT authenticated.")
			fmt.Println("")
			fmt.Println("To authenticate, run:")
			fmt.Println("  cloudflared tunnel login")
			fmt.Println("")
			fmt.Println("This will open a browser where you can authorize cloudflared")
			fmt.Println("to access your Cloudflare account and manage tunnels/DNS.")
			fmt.Println("")
			fmt.Println("After login, configure your domain in conductor:")
			fmt.Println("")
			fmt.Println("  Add to ~/.conductor/conductor.json:")
			fmt.Println("  {")
			fmt.Println("    \"defaults\": {")
			fmt.Println("      \"tunnel\": {")
			fmt.Println("        \"domain\": \"your-domain.com\"")
			fmt.Println("      }")
			fmt.Println("    }")
			fmt.Println("  }")
		}

		return nil
	},
}

func init() {
	tunnelStartCmd.Flags().IntVarP(&tunnelStartPort, "port", "p", 0, "Port to tunnel (defaults to first worktree port)")
	tunnelStartCmd.Flags().BoolVar(&tunnelStartNamed, "named", false, "Use named tunnel with custom domain")

	tunnelCmd.AddCommand(tunnelStartCmd)
	tunnelCmd.AddCommand(tunnelStopCmd)
	tunnelCmd.AddCommand(tunnelListCmd)
	tunnelCmd.AddCommand(tunnelStatusCmd)
	tunnelCmd.AddCommand(tunnelLogsCmd)
	tunnelCmd.AddCommand(tunnelSetupCmd)
}
