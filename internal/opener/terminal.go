package opener

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TerminalType represents different terminal emulators
type TerminalType string

const (
	TerminalITerm    TerminalType = "iterm"
	TerminalTerminal TerminalType = "terminal"
	TerminalWezterm  TerminalType = "wezterm"
)

// OpenInTerminal opens a path in the specified terminal
func OpenInTerminal(path string, terminalType TerminalType, command string) error {
	switch terminalType {
	case TerminalITerm:
		return openInITerm(path, command)
	case TerminalTerminal:
		return openInTerminalApp(path, command)
	case TerminalWezterm:
		return openInWezterm(path, command)
	default:
		return openInITerm(path, command)
	}
}

// openInITerm opens a new iTerm2 window with the given path
func openInITerm(path, command string) error {
	script := fmt.Sprintf(`
tell application "iTerm"
	activate
	set newWindow to (create window with default profile)
	tell current session of newWindow
		write text "cd %s"
		%s
	end tell
end tell
`, escapeAppleScript(path), formatCommand(command))

	return runAppleScript(script)
}

// openInITermSplit opens iTerm with split panes (like original conductor)
func OpenInITermSplit(path, leftCommand, rightCommand string) error {
	script := fmt.Sprintf(`
tell application "iTerm"
	activate
	set newWindow to (create window with default profile)
	tell current session of newWindow
		write text "cd %s"
		%s

		-- Create vertical split
		set rightPane to (split vertically with default profile)
		tell rightPane
			write text "cd %s"
			%s
		end tell
	end tell
end tell
`, escapeAppleScript(path), formatCommand(leftCommand),
		escapeAppleScript(path), formatCommand(rightCommand))

	return runAppleScript(script)
}

// openInTerminalApp opens macOS Terminal.app
func openInTerminalApp(path, command string) error {
	script := fmt.Sprintf(`
tell application "Terminal"
	activate
	do script "cd %s%s"
end tell
`, escapeAppleScript(path), formatShellCommand(command))

	return runAppleScript(script)
}

// openInWezterm opens WezTerm
func openInWezterm(path, command string) error {
	args := []string{"start", "--cwd", path}
	if command != "" {
		args = append(args, "--", "bash", "-c", command)
	}

	cmd := exec.Command("wezterm", args...)
	return cmd.Start()
}

// formatCommand formats a command for AppleScript write text
func formatCommand(command string) string {
	if command == "" {
		return ""
	}
	return fmt.Sprintf(`write text "%s"`, escapeAppleScript(command))
}

// formatShellCommand formats command for Terminal.app
func formatShellCommand(command string) string {
	if command == "" {
		return ""
	}
	return fmt.Sprintf(` && %s`, command)
}

// escapeAppleScript escapes a string for AppleScript
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// runAppleScript executes an AppleScript
func runAppleScript(script string) error {
	cmd := exec.Command("osascript", "-e", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// DetectTerminal tries to detect which terminal is running
func DetectTerminal() TerminalType {
	// Check TERM_PROGRAM environment variable
	termProgram := os.Getenv("TERM_PROGRAM")
	switch termProgram {
	case "iTerm.app":
		return TerminalITerm
	case "Apple_Terminal":
		return TerminalTerminal
	case "WezTerm":
		return TerminalWezterm
	}

	// Check if iTerm is installed
	if _, err := exec.LookPath("osascript"); err == nil {
		// Check if iTerm exists
		cmd := exec.Command("osascript", "-e", `id of application "iTerm"`)
		if cmd.Run() == nil {
			return TerminalITerm
		}
	}

	return TerminalTerminal
}
