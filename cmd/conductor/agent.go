package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/hammashamzah/conductor/internal/agent"
	"github.com/hammashamzah/conductor/internal/clickup"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage the ClickUp agent daemon",
	Long:  `The agent daemon watches ClickUp for task status changes and automatically creates worktrees with Claude Code.`,
}

var agentStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the agent daemon",
	RunE:  runAgentStart,
}

var agentStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the agent daemon",
	RunE:  runAgentStop,
}

var agentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show agent daemon status",
	RunE:  runAgentStatus,
}

var agentSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup for ClickUp integration",
	RunE:  runAgentSetup,
}

var agentPickCmd = &cobra.Command{
	Use:   "pick [project-name]",
	Short: "AI-pick the next task for a sequential-mode project",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runAgentPick,
}

var foregroundFlag bool

func init() {
	agentCmd.AddCommand(agentStartCmd)
	agentCmd.AddCommand(agentStopCmd)
	agentCmd.AddCommand(agentStatusCmd)
	agentCmd.AddCommand(agentSetupCmd)
	agentCmd.AddCommand(agentPickCmd)

	agentStartCmd.Flags().BoolVar(&foregroundFlag, "foreground", false, "Run in foreground instead of background")
}

func runAgentStart(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cfg.Defaults.ClickUp == nil || cfg.Defaults.ClickUp.APIToken == "" {
		return fmt.Errorf("ClickUp not configured. Run 'conductor agent setup' first")
	}

	if !foregroundFlag {
		// Background mode: fork and write PID file
		return startBackground()
	}

	// Foreground mode
	s := store.New(cfg)
	defer func() { _, _ = s.Close() }()

	daemon, err := agent.NewDaemon(s, cfg.Defaults.ClickUp, nil)
	if err != nil {
		return err
	}

	if err := daemon.Start(); err != nil {
		return err
	}

	// Write PID file
	if err := writePIDFile(os.Getpid()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write PID file: %v\n", err)
	}
	defer removePIDFile()

	fmt.Printf("Agent daemon started (mode: %s, PID: %d)\n", daemon.Mode(), os.Getpid())

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	fmt.Println("\nShutting down agent daemon...")
	if err := daemon.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "error during shutdown: %v\n", err)
	}

	// Save updated ClickUp config (webhook IDs cleared on stop)
	_ = config.Save(cfg)

	fmt.Println("Agent daemon stopped.")
	return nil
}

func runAgentStop(cmd *cobra.Command, args []string) error {
	pid, err := readPIDFile()
	if err != nil {
		return fmt.Errorf("agent daemon not running (no PID file): %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		removePIDFile()
		return fmt.Errorf("process %d not found", pid)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		removePIDFile()
		return fmt.Errorf("failed to signal process %d: %w", pid, err)
	}

	fmt.Printf("Sent SIGTERM to agent daemon (PID %d)\n", pid)
	removePIDFile()
	return nil
}

func runAgentStatus(cmd *cobra.Command, args []string) error {
	pid, err := readPIDFile()
	if err != nil {
		fmt.Println("Agent daemon: stopped")
		return nil
	}

	// Check if process is running
	process, err := os.FindProcess(pid)
	if err != nil || process.Signal(syscall.Signal(0)) != nil {
		fmt.Println("Agent daemon: stopped (stale PID file)")
		removePIDFile()
		return nil
	}

	fmt.Printf("Agent daemon: running (PID %d)\n", pid)

	// Show watched projects
	cfg, err := config.Load()
	if err != nil {
		return nil
	}

	fmt.Println("\nWatched projects:")
	for projectName, project := range cfg.Projects {
		projectConfig, err := config.LoadProjectConfig(project.Path)
		if err != nil || projectConfig == nil {
			continue
		}
		if projectConfig.ClickUp != nil && projectConfig.ClickUp.ListID != "" {
			triggerStatus := projectConfig.ClickUp.TriggerStatus
			if triggerStatus == "" {
				triggerStatus = "in progress"
			}
			mode := projectConfig.ClickUp.GetMode()
			modeStr := string(mode)
			if projectConfig.ClickUp.AutoPick {
				modeStr += "+autopick"
			}
			fmt.Printf("  %s (list: %s, trigger: %q, mode: %s)\n", projectName, projectConfig.ClickUp.ListID, triggerStatus, modeStr)
		}
	}

	return nil
}

func runAgentSetup(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)

	if cfg.Defaults.ClickUp == nil {
		cfg.Defaults.ClickUp = &config.ClickUpConfig{}
	}

	// API Token
	fmt.Printf("ClickUp API Token [%s]: ", maskToken(cfg.Defaults.ClickUp.APIToken))
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)
	if token != "" {
		cfg.Defaults.ClickUp.APIToken = token
	}

	// Team ID
	fmt.Printf("ClickUp Team/Workspace ID [%s]: ", cfg.Defaults.ClickUp.TeamID)
	teamID, _ := reader.ReadString('\n')
	teamID = strings.TrimSpace(teamID)
	if teamID != "" {
		cfg.Defaults.ClickUp.TeamID = teamID
	}

	// Trigger Status
	defaultTrigger := cfg.Defaults.ClickUp.TriggerStatus
	if defaultTrigger == "" {
		defaultTrigger = "in progress"
	}
	fmt.Printf("Trigger status [%s]: ", defaultTrigger)
	trigger, _ := reader.ReadString('\n')
	trigger = strings.TrimSpace(trigger)
	if trigger != "" {
		cfg.Defaults.ClickUp.TriggerStatus = trigger
	} else if cfg.Defaults.ClickUp.TriggerStatus == "" {
		cfg.Defaults.ClickUp.TriggerStatus = "in progress"
	}

	// Webhook Port
	defaultPort := cfg.Defaults.ClickUp.WebhookPort
	if defaultPort == 0 {
		defaultPort = 9876
	}
	fmt.Printf("Webhook port [%d]: ", defaultPort)
	portStr, _ := reader.ReadString('\n')
	portStr = strings.TrimSpace(portStr)
	if portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.Defaults.ClickUp.WebhookPort = port
		}
	} else if cfg.Defaults.ClickUp.WebhookPort == 0 {
		cfg.Defaults.ClickUp.WebhookPort = 9876
	}

	// Save
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("\nClickUp configuration saved!")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Add clickup.listId to each project's conductor.json")
	fmt.Println("  2. Run: conductor agent start")

	return nil
}

func runAgentPick(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cfg.Defaults.ClickUp == nil || cfg.Defaults.ClickUp.APIToken == "" {
		return fmt.Errorf("ClickUp not configured. Run 'conductor agent setup' first")
	}

	// Determine project name
	var projectName string
	if len(args) > 0 {
		projectName = args[0]
	} else {
		// Try to detect from current directory
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("could not determine current directory: %w", err)
		}
		for name, project := range cfg.Projects {
			if strings.HasPrefix(cwd, project.Path) {
				projectName = name
				break
			}
		}
		if projectName == "" {
			return fmt.Errorf("could not detect project from current directory. Specify project name as argument")
		}
	}

	// Validate project exists and is sequential
	project, ok := cfg.Projects[projectName]
	if !ok {
		return fmt.Errorf("project %q not found", projectName)
	}

	projectConfig, err := config.LoadProjectConfig(project.Path)
	if err != nil || projectConfig == nil || projectConfig.ClickUp == nil {
		return fmt.Errorf("project %q has no ClickUp configuration", projectName)
	}

	if projectConfig.ClickUp.GetMode() != config.AgentModeSequential {
		return fmt.Errorf("project %q is not in sequential mode (current: %s)", projectName, projectConfig.ClickUp.GetMode())
	}

	// Create picker and pick next task
	client := clickup.NewClient(cfg.Defaults.ClickUp.APIToken)
	picker := agent.NewTaskPicker(client)

	readyStatus := projectConfig.ClickUp.GetReadyStatus()
	fmt.Printf("Fetching tasks with status %q from list %s...\n", readyStatus, projectConfig.ClickUp.ListID)

	task, err := picker.PickNextTask(projectConfig.ClickUp.ListID, readyStatus)
	if err != nil {
		return fmt.Errorf("failed to pick task: %w", err)
	}

	fmt.Printf("Selected: %s (ID: %s)\n", task.Name, task.ID)

	// Move to trigger status so the running daemon picks it up
	triggerStatus := projectConfig.ClickUp.TriggerStatus
	if triggerStatus == "" {
		triggerStatus = "in progress"
	}
	fmt.Printf("Moving task to %q...\n", triggerStatus)

	if err := client.UpdateTaskStatus(task.ID, triggerStatus); err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	fmt.Printf("Done! Task %q is now %q. The running daemon will pick it up.\n", task.Name, triggerStatus)
	return nil
}

// PID file helpers

func pidFilePath() string {
	dir, err := config.ConductorDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agent.pid")
}

func writePIDFile(pid int) error {
	path := pidFilePath()
	if path == "" {
		return fmt.Errorf("could not determine conductor directory")
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

func readPIDFile() (int, error) {
	path := pidFilePath()
	if path == "" {
		return 0, fmt.Errorf("could not determine conductor directory")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func removePIDFile() {
	if path := pidFilePath(); path != "" {
		_ = os.Remove(path)
	}
}

func startBackground() error {
	// Re-exec ourselves with --foreground
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Build args: conductor agent start --foreground
	args := []string{exe, "agent", "start", "--foreground"}

	attr := &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}

	// In a real daemon we'd redirect to log files, but for now keep stdio
	logDir, _ := config.ConductorDir()
	if logDir != "" {
		logFile := filepath.Join(logDir, "agent.log")
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			attr.Files = []*os.File{nil, f, f}
		}
	}

	proc, err := os.StartProcess(exe, args, attr)
	if err != nil {
		return fmt.Errorf("failed to start background process: %w", err)
	}

	// Release so it runs independently
	_ = proc.Release()

	fmt.Printf("Agent daemon started in background (PID %d)\n", proc.Pid)
	return nil
}

func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

// agentStateInfo is used for JSON status output
//
//nolint:unused // wired up in a follow-up
type agentStateInfo struct {
	Running  bool   `json:"running"`
	PID      int    `json:"pid,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Projects []struct {
		Name   string `json:"name"`
		ListID string `json:"listId"`
	} `json:"projects,omitempty"`
}

//nolint:unused // wired up in a follow-up
func getAgentStateJSON() string {
	info := agentStateInfo{}

	pid, err := readPIDFile()
	if err == nil {
		process, err := os.FindProcess(pid)
		if err == nil && process.Signal(syscall.Signal(0)) == nil {
			info.Running = true
			info.PID = pid
		}
	}

	data, _ := json.Marshal(info)
	return string(data)
}
