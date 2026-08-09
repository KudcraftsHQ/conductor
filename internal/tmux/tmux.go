package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
)

const SessionName = "conductor"
const TUIWindowName = "conductor"

// ErrTmuxNotInstalled is returned when tmux is not found
var ErrTmuxNotInstalled = fmt.Errorf("tmux is required but not installed")

// TmuxInstallGuide returns installation instructions for tmux
func TmuxInstallGuide() string {
	return `Install tmux:
  macOS:   brew install tmux
  Ubuntu:  sudo apt install tmux
  Fedora:  sudo dnf install tmux

Then run 'conductor' again.`
}

// CheckInstalled verifies tmux is available
func CheckInstalled() error {
	_, err := exec.LookPath("tmux")
	if err != nil {
		return ErrTmuxNotInstalled
	}
	return nil
}

// IsITerm2 checks if running in iTerm2
func IsITerm2() bool {
	return os.Getenv("TERM_PROGRAM") == "iTerm.app"
}

// IsInsideTmux checks if already inside a tmux session
func IsInsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// IsInsideConductorSession checks if inside the conductor session
func IsInsideConductorSession() bool {
	if !IsInsideTmux() {
		return false
	}
	// Check current session name
	cmd := exec.Command("tmux", "display-message", "-p", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == SessionName
}

// SessionExists checks if the conductor session already exists
func SessionExists() bool {
	cmd := exec.Command("tmux", "has-session", "-t", SessionName)
	return cmd.Run() == nil
}

// DevCommand keeps a dev server pane alive: it reruns `conductor run` when the
// server exits, and offers a shell prompt in between.
const DevCommand = `trap '' INT; while true; do conductor run; ec=$?; echo ''; if [ $ec -eq 130 ]; then echo 'Dev server stopped. Press Enter to restart or type command...'; else echo 'Dev server exited. Press Enter to restart or type command...'; fi; read -r cmd; [ -n "$cmd" ] && eval "$cmd" || continue; done`

// EnsureSession creates the detached conductor session if it does not exist.
//
// It exists for backends that need a window without attaching a client — the
// T3 backend hosts the agent elsewhere and only wants somewhere to put the dev
// server.
//
// The tmux server is started in its own systemd scope when possible. A tmux
// server inherits the cgroup of whoever first started it, so starting it from
// inside another service's cgroup makes every dev server die when that service
// restarts. Escaping to a transient scope is what keeps dev servers alive
// across a T3 Code restart.
func EnsureSession() error {
	if err := CheckInstalled(); err != nil {
		return err
	}
	if SessionExists() {
		return nil
	}

	args := []string{"new-session", "-d", "-s", SessionName, "-n", TUIWindowName, "conductor", "tui"}
	if runner, ok := detachedRunner(); ok {
		full := append(append([]string{}, runner[1:]...), "tmux")
		full = append(full, args...)
		if err := exec.Command(runner[0], full...).Run(); err == nil {
			return nil
		}
		// systemd-run can fail (no user manager, permissions); fall through to
		// a plain start rather than leaving the caller with no session.
	}
	if err := exec.Command("tmux", args...).Run(); err != nil {
		return fmt.Errorf("failed to create tmux session: %w", err)
	}
	return nil
}

// detachedRunner returns a command prefix that launches a process in its own
// cgroup, or false when none is available (non-Linux, no systemd).
func detachedRunner() ([]string, bool) {
	path, err := exec.LookPath("systemd-run")
	if err != nil {
		return nil, false
	}
	return []string{path, "--user", "--scope", "--quiet", "--collect"}, true
}

// CreateDevWindow creates a window containing only a dev server pane.
//
// This is for backends that host the coding agent somewhere other than tmux —
// the T3 Code backend runs the agent as a thread, and wants tmux only as a
// long-lived home for the dev server. One worktree gets one dev window.
func CreateDevWindow(project, branch, worktreePath string) error {
	if err := EnsureSession(); err != nil {
		return err
	}

	windowName := WindowName(project, branch)
	windowTarget := fmt.Sprintf("%s:%s", SessionName, windowName)

	if WindowExists(project, branch) {
		return fmt.Errorf("tmux window %q already exists", windowName)
	}

	cmd := exec.Command("tmux", "new-window",
		"-d", // Do not steal focus from whatever the user is looking at.
		"-t", SessionName+":",
		"-n", windowName,
		"-c", worktreePath,
		"-P", "-F", "#{pane_id}",
		"bash", "-c", DevCommand)
	devPaneIDBytes, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create tmux dev window: %w", err)
	}
	devPaneID := strings.TrimSpace(string(devPaneIDBytes))

	_ = exec.Command("tmux", "select-pane", "-t", devPaneID, "-T", "dev").Run()
	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "automatic-rename", "off").Run()
	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "allow-rename", "off").Run()
	return nil
}

// configureSession sets up session options.
// When useCC is true, configures for iTerm2 -CC mode (status bar off, native tabs).
// When useCC is false, keeps the tmux status bar visible.
func configureSession(session string, useCC bool) {
	if useCC {
		// Disable status bar - iTerm2 shows tmux windows as native tabs
		_ = exec.Command("tmux", "set-option", "-t", session, "status", "off").Run()
		// Enable set-titles so iTerm2 uses tmux window names for window title
		_ = exec.Command("tmux", "set-option", "-g", "set-titles", "on").Run()
		_ = exec.Command("tmux", "set-option", "-g", "set-titles-string", "#W").Run()
	} else {
		// Ensure status bar is on for plain tmux, positioned at top
		_ = exec.Command("tmux", "set-option", "-t", session, "status", "on").Run()
		_ = exec.Command("tmux", "set-option", "-t", session, "status-position", "top").Run()
		// Enable mouse so tabs are clickable
		_ = exec.Command("tmux", "set-option", "-t", session, "mouse", "on").Run()
	}
	// Disable automatic window renaming so our names stick
	_ = exec.Command("tmux", "set-option", "-t", session, "allow-rename", "off").Run()
	_ = exec.Command("tmux", "set-option", "-t", session, "automatic-rename", "off").Run()
}

// StartSession starts or attaches to the conductor tmux session
func StartSession() error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	// Check if iTerm2 -CC mode should be used
	cfg, _ := config.Load()
	useCC := IsITerm2() && (cfg == nil || !cfg.Defaults.Tmux.DisableCC)

	var args []string

	if SessionExists() {
		configureSession(SessionName, useCC)
		if useCC {
			args = []string{"tmux", "-CC", "attach-session", "-t", SessionName}
		} else {
			args = []string{"tmux", "attach-session", "-t", SessionName}
		}
	} else {
		// Create session detached first so we can configure it before attaching
		createArgs := []string{"tmux", "new-session", "-d", "-s", SessionName, "-n", TUIWindowName, "conductor", "tui"}
		if err := exec.Command(createArgs[0], createArgs[1:]...).Run(); err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}
		configureSession(SessionName, useCC)
		_ = exec.Command("tmux", "select-pane", "-t", SessionName+":"+TUIWindowName, "-T", "conductor").Run()
		if useCC {
			args = []string{"tmux", "-CC", "attach-session", "-t", SessionName}
		} else {
			args = []string{"tmux", "attach-session", "-t", SessionName}
		}
	}

	// Replace current process with tmux
	return syscall.Exec(tmuxPath, args, os.Environ())
}

// WindowName returns the window name for a worktree
// Uses "/" as separator since ":" is reserved in tmux target syntax (session:window.pane)
func WindowName(project, branch string) string {
	return fmt.Sprintf("%s/%s", project, branch)
}

// agentSystemPrompt returns the system prompt with tmux instructions for any coding agent
func agentSystemPrompt(devPaneID string) string {
	layout := fmt.Sprintf(`This workspace uses conductor with tmux panes:
- Left pane: Coding agent (you are here)
- Right pane: Dev server (pane ID: %s)`, devPaneID)

	return fmt.Sprintf(`## Conductor Tmux Integration

%s

### Dev Server Management
- To view dev server logs: tmux capture-pane -t %s -p | tail -50
- To kill the dev server: tmux send-keys -t %s C-c
- To restart the dev server: tmux send-keys -t %s 'conductor run' Enter
- IMPORTANT: Only run dev server commands in the dev pane, never in this pane`, layout, devPaneID, devPaneID, devPaneID)
}

// CreateCodingWindow creates a new window inside the conductor tmux session
// with split panes for coding: coding agent (left) + dev server (right).
func CreateCodingWindow(project, branch, worktreePath string, agent codingagent.Agent) error {
	windowName := WindowName(project, branch)
	windowTarget := fmt.Sprintf("%s:%s", SessionName, windowName)

	// Create new window with dev server first (will be on the right after split)
	devCmd := `trap '' INT; while true; do conductor run; ec=$?; echo ''; if [ $ec -eq 130 ]; then echo 'Dev server stopped. Press Enter to restart or type command...'; else echo 'Dev server exited. Press Enter to restart or type command...'; fi; read -r cmd; [ -n "$cmd" ] && eval "$cmd" || continue; done`
	cmd := exec.Command("tmux", "new-window",
		"-t", SessionName+":",
		"-n", windowName,
		"-c", worktreePath,
		"-P", "-F", "#{pane_id}",
		"bash", "-c", devCmd)
	devPaneIDBytes, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create tmux window: %w", err)
	}
	devPaneID := strings.TrimSpace(string(devPaneIDBytes))

	_ = exec.Command("tmux", "select-pane", "-t", devPaneID, "-T", "dev").Run()

	systemPrompt := agentSystemPrompt(devPaneID)

	if agent.UsesContextFile() {
		if err := codingagent.WriteContextFile(worktreePath, systemPrompt); err != nil {
			return fmt.Errorf("failed to write agent context file: %w", err)
		}
	}

	agentArgs := agent.InteractiveArgs(systemPrompt)
	splitArgs := []string{"split-window", "-t", windowTarget, "-hb", "-c", worktreePath}
	splitArgs = append(splitArgs, agentArgs...)
	if err := exec.Command("tmux", splitArgs...).Run(); err != nil {
		return fmt.Errorf("failed to split window: %w", err)
	}

	paneLabel := branch + " - " + agent.PaneLabel()
	_ = exec.Command("tmux", "select-pane", "-t", windowTarget+".{left}", "-T", paneLabel).Run()
	_ = exec.Command("tmux", "select-pane", "-t", windowTarget+".{left}").Run()

	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "automatic-rename", "off").Run()
	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "allow-rename", "off").Run()

	return nil
}

// CreateCodingWindowWithTask creates a new window inside the conductor tmux
// session with two panes and pre-loads the agent with a task prompt.
func CreateCodingWindowWithTask(project, branch, worktreePath, taskPrompt string, agent codingagent.Agent) error {
	windowName := WindowName(project, branch)
	windowTarget := fmt.Sprintf("%s:%s", SessionName, windowName)

	devCmd := `trap '' INT; while true; do conductor run; ec=$?; echo ''; if [ $ec -eq 130 ]; then echo 'Dev server stopped. Press Enter to restart or type command...'; else echo 'Dev server exited. Press Enter to restart or type command...'; fi; read -r cmd; [ -n "$cmd" ] && eval "$cmd" || continue; done`
	cmd := exec.Command("tmux", "new-window",
		"-t", SessionName+":",
		"-n", windowName,
		"-c", worktreePath,
		"-P", "-F", "#{pane_id}",
		"bash", "-c", devCmd)
	devPaneIDBytes, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create tmux window: %w", err)
	}
	devPaneID := strings.TrimSpace(string(devPaneIDBytes))

	_ = exec.Command("tmux", "select-pane", "-t", devPaneID, "-T", "dev").Run()

	systemPrompt := agentSystemPrompt(devPaneID)

	if agent.UsesContextFile() {
		if err := codingagent.WriteContextFile(worktreePath, systemPrompt); err != nil {
			return fmt.Errorf("failed to write agent context file: %w", err)
		}
	}

	agentArgs := agent.TaskArgs(systemPrompt, taskPrompt)
	splitArgs := []string{"split-window", "-t", windowTarget, "-hb", "-c", worktreePath}
	splitArgs = append(splitArgs, agentArgs...)
	if err := exec.Command("tmux", splitArgs...).Run(); err != nil {
		return fmt.Errorf("failed to split window: %w", err)
	}

	paneLabel := branch + " - " + agent.PaneLabel() + " (agent)"
	_ = exec.Command("tmux", "select-pane", "-t", windowTarget+".{left}", "-T", paneLabel).Run()
	_ = exec.Command("tmux", "select-pane", "-t", windowTarget+".{left}").Run()

	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "automatic-rename", "off").Run()
	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "allow-rename", "off").Run()

	return nil
}

// WindowExists checks if a worktree window exists in the conductor session.
func WindowExists(project, branch string) bool {
	windowName := WindowName(project, branch)
	cmd := exec.Command("tmux", "list-windows",
		"-t", SessionName,
		"-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == windowName {
			return true
		}
	}
	return false
}

// KillWindow kills a tmux window in the conductor session.
func KillWindow(project, branch string) error {
	windowName := WindowName(project, branch)
	return exec.Command("tmux", "kill-window",
		"-t", fmt.Sprintf("%s:%s", SessionName, windowName)).Run()
}

// FocusWindow switches to a worktree window in the conductor session.
func FocusWindow(project, branch string) error {
	windowName := WindowName(project, branch)
	return exec.Command("tmux", "select-window",
		"-t", fmt.Sprintf("%s:%s", SessionName, windowName)).Run()
}

// CreateWindowWithCommand creates a new tmux window running a specific command
// Used by mission system to launch opencode agents in dedicated windows

// WindowExistsByName checks if a window with the given name exists in the conductor session.
func WindowExistsByName(name string) bool {
	cmd := exec.Command("tmux", "list-windows",
		"-t", SessionName,
		"-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == name {
			return true
		}
	}
	return false
}

// KillOtherWindows kills all tmux windows in the conductor session except the current one.
// This allows the TUI to quit cleanly (closing the last window ends the session).
func KillOtherWindows() {
	// Get current window ID
	currentOut, err := exec.Command("tmux", "display-message", "-t", SessionName, "-p", "#{window_id}").Output()
	if err != nil {
		return
	}
	currentID := strings.TrimSpace(string(currentOut))

	// List all windows
	out, err := exec.Command("tmux", "list-windows", "-t", SessionName, "-F", "#{window_id}").Output()
	if err != nil {
		return
	}

	for _, wid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if wid != "" && wid != currentID {
			_ = exec.Command("tmux", "kill-window", "-t", wid).Run()
		}
	}
}

// DetachSession detaches from the conductor tmux session
func DetachSession() error {
	// Detach all clients from the session
	cmd := exec.Command("tmux", "detach-client", "-s", SessionName)
	return cmd.Run()
}

// PaneExists checks if a tmux pane exists by its ID (e.g., "%5")
func PaneExists(paneID string) bool {
	cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_id}")
	return cmd.Run() == nil
}

// GetPaneCommand returns the current command running in a tmux pane
func GetPaneCommand(paneID string) string {
	cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_current_command}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// StartAgentPane creates a detached window in the conductor session running the
// given argv, and returns the pane ID of the pane the agent runs in.
func StartAgentPane(windowName, workDir string, argv []string, paneTitle string) (string, error) {
	if WindowExistsByName(windowName) {
		return "", fmt.Errorf("window %q already exists", windowName)
	}

	args := []string{"new-window",
		"-t", SessionName + ":",
		"-n", windowName,
		"-c", workDir,
		"-P", "-F", "#{pane_id}"}
	args = append(args, argv...)

	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("failed to create tmux window %q: %w", windowName, err)
	}
	paneID := strings.TrimSpace(string(out))

	if paneTitle != "" {
		_ = exec.Command("tmux", "select-pane", "-t", paneID, "-T", paneTitle).Run()
	}

	windowTarget := fmt.Sprintf("%s:%s", SessionName, windowName)
	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "automatic-rename", "off").Run()
	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "allow-rename", "off").Run()

	return paneID, nil
}

// ListWindowNames returns the names of all windows in the conductor session.
func ListWindowNames() []string {
	out, err := exec.Command("tmux", "list-windows", "-t", SessionName, "-F", "#{window_name}").Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}
