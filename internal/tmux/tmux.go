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

// CreateCodingWindow creates a new window with split panes for coding
// Left: claude CLI, Right: dev server
func CreateCodingWindow(project, branch, worktreePath string) error {
	windowName := WindowName(project, branch)

	// Create new window with left pane (claude)
	// Use "session:" format to create window at next available index
	cmd := exec.Command("tmux", "new-window",
		"-t", SessionName+":",
		"-n", windowName,
		"-c", worktreePath,
		"claude", "--dangerously-skip-permissions")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create tmux window: %w", err)
	}

	// Split horizontally and run dev server in right pane
	cmd = exec.Command("tmux", "split-window",
		"-t", fmt.Sprintf("%s:%s", SessionName, windowName),
		"-h", // horizontal split
		"-c", worktreePath,
		"conductor", "run")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to split window: %w", err)
	}

	windowTarget := fmt.Sprintf("%s:%s", SessionName, windowName)

	// Set pane titles for iTerm2 tab display (format: "branch - role")
	// Left pane (claude)
	_ = exec.Command("tmux", "select-pane", "-t", windowTarget+".{left}", "-T", branch+" - claude").Run()
	// Right pane (dev server)
	_ = exec.Command("tmux", "select-pane", "-t", windowTarget+".{right}", "-T", branch+" - dev").Run()

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
