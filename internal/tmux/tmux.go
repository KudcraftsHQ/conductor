package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
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

// configureSession sets up session options for iTerm2 integration
func configureSession(session string) {
	// Disable status bar - iTerm2 shows tmux windows as native tabs
	_ = exec.Command("tmux", "set-option", "-t", session, "status", "off").Run()
	// Disable automatic window renaming so our names stick
	_ = exec.Command("tmux", "set-option", "-t", session, "allow-rename", "off").Run()
	_ = exec.Command("tmux", "set-option", "-t", session, "automatic-rename", "off").Run()
	// Enable set-titles so iTerm2 uses tmux window names for window title
	// Use -g for global option as iTerm2 -CC mode may not respect session-level settings
	_ = exec.Command("tmux", "set-option", "-g", "set-titles", "on").Run()
	// Use window name (#W) so each tab shows its actual name
	_ = exec.Command("tmux", "set-option", "-g", "set-titles-string", "#W").Run()
}

// StartSession starts or attaches to the conductor tmux session
func StartSession() error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	var args []string

	if IsITerm2() {
		// iTerm2 control mode - native tab integration
		// iTerm2 shows tmux windows as native tabs, so disable tmux status bar
		if SessionExists() {
			// Configure session in case it was created without iTerm2
			configureSession(SessionName)
			args = []string{"tmux", "-CC", "attach-session", "-t", SessionName}
		} else {
			// Create session, then configure it
			createArgs := []string{"tmux", "new-session", "-d", "-s", SessionName, "-n", TUIWindowName, "conductor", "tui"}
			if err := exec.Command(createArgs[0], createArgs[1:]...).Run(); err != nil {
				return fmt.Errorf("failed to create session: %w", err)
			}
			// Disable status bar and automatic window renaming
			configureSession(SessionName)
			// Set pane title for the TUI window
			_ = exec.Command("tmux", "select-pane", "-t", SessionName+":"+TUIWindowName, "-T", "conductor").Run()
			// Attach with -CC
			args = []string{"tmux", "-CC", "attach-session", "-t", SessionName}
		}
	} else {
		// Non-iTerm2: regular tmux
		if SessionExists() {
			args = []string{"tmux", "attach-session", "-t", SessionName}
		} else {
			args = []string{"tmux", "new-session", "-s", SessionName, "-n", TUIWindowName, "conductor", "tui"}
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

// claudeSystemPrompt returns the system prompt for Claude with tmux instructions
func claudeSystemPrompt(devPaneID string) string {
	return fmt.Sprintf(`## Conductor Tmux Integration

This workspace uses conductor with tmux panes:
- Left pane: Claude Code (you are here)
- Right pane: Dev server (pane ID: %s)

### Dev Server Management
- To view dev server logs: tmux capture-pane -t %s -p | tail -50
- To kill the dev server: tmux send-keys -t %s C-c
- To restart the dev server: tmux send-keys -t %s 'conductor run' Enter
- IMPORTANT: Only run dev server commands in the dev pane, never in this pane`, devPaneID, devPaneID, devPaneID, devPaneID)
}

// CreateCodingWindow creates a new window with split panes for coding
// Left: claude CLI, Right: dev server
func CreateCodingWindow(project, branch, worktreePath string) error {
	windowName := WindowName(project, branch)
	windowTarget := fmt.Sprintf("%s:%s", SessionName, windowName)

	// Create new window with dev server first (will be on the right after split)
	// Use bash with trap to catch Ctrl+C and keep the pane open
	devCmd := `trap '' INT; while true; do conductor run; ec=$?; echo ''; if [ $ec -eq 130 ]; then echo 'Dev server stopped. Press Enter to restart or type command...'; else echo 'Dev server exited. Press Enter to restart or type command...'; fi; read -r cmd; [ -n "$cmd" ] && eval "$cmd" || continue; done`
	cmd := exec.Command("tmux", "new-window",
		"-t", SessionName+":",
		"-n", windowName,
		"-c", worktreePath,
		"-P", "-F", "#{pane_id}", // Print the pane ID
		"bash", "-c", devCmd)
	devPaneIDBytes, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create tmux window: %w", err)
	}
	devPaneID := strings.TrimSpace(string(devPaneIDBytes))

	// Set dev pane title
	_ = exec.Command("tmux", "select-pane", "-t", devPaneID, "-T", "dev").Run()

	// Split horizontally and run claude in left pane (split creates pane to the left)
	cmd = exec.Command("tmux", "split-window",
		"-t", windowTarget,
		"-hb", // horizontal split, before (left of) current pane
		"-c", worktreePath,
		"claude", "--dangerously-skip-permissions",
		"--append-system-prompt", claudeSystemPrompt(devPaneID))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to split window: %w", err)
	}

	// Set pane titles for iTerm2 tab display
	// Left pane (claude) - includes branch for context
	_ = exec.Command("tmux", "select-pane", "-t", windowTarget+".{left}", "-T", branch+" - claude").Run()

	// Focus left pane (claude)
	_ = exec.Command("tmux", "select-pane", "-t", windowTarget+".{left}").Run()

	// Disable automatic window renaming for this window so our name sticks
	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "automatic-rename", "off").Run()
	_ = exec.Command("tmux", "set-option", "-t", windowTarget, "allow-rename", "off").Run()

	return nil
}

// WindowExists checks if a window exists
func WindowExists(project, branch string) bool {
	windowName := WindowName(project, branch)
	cmd := exec.Command("tmux", "list-windows",
		"-t", SessionName,
		"-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// Check if window name is in output
	for _, line := range strings.Split(string(out), "\n") {
		if line == windowName {
			return true
		}
	}
	return false
}

// KillWindow kills a tmux window
func KillWindow(project, branch string) error {
	windowName := WindowName(project, branch)
	cmd := exec.Command("tmux", "kill-window",
		"-t", fmt.Sprintf("%s:%s", SessionName, windowName))
	return cmd.Run() // Ignore error if window doesn't exist
}

// FocusWindow switches to an existing window
func FocusWindow(project, branch string) error {
	windowName := WindowName(project, branch)
	cmd := exec.Command("tmux", "select-window",
		"-t", fmt.Sprintf("%s:%s", SessionName, windowName))
	return cmd.Run()
}

// KillSession kills the entire conductor tmux session
func KillSession() error {
	cmd := exec.Command("tmux", "kill-session", "-t", SessionName)
	return cmd.Run()
}

// DetachSession detaches from the conductor tmux session
func DetachSession() error {
	// Detach all clients from the session
	cmd := exec.Command("tmux", "detach-client", "-s", SessionName)
	return cmd.Run()
}
