package session

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PaneInfo holds raw tmux pane information
type PaneInfo struct {
	SessionName string
	WindowName  string
	PaneID      string
	PanePID     int
	Command     string
	Title       string
}

// ScanPanes lists all panes in the conductor tmux session and detects agent processes
func ScanPanes(tmuxSession string) []PaneInfo {
	cmd := exec.Command("tmux", "list-panes", "-s", "-t", tmuxSession,
		"-F", "#{session_name}|#{window_name}|#{pane_id}|#{pane_pid}|#{pane_current_command}|#{pane_title}")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var panes []PaneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 6 {
			continue
		}
		pid, _ := strconv.Atoi(parts[3])
		panes = append(panes, PaneInfo{
			SessionName: parts[0],
			WindowName:  parts[1],
			PaneID:      parts[2],
			PanePID:     pid,
			Command:     parts[4],
			Title:       parts[5],
		})
	}
	return panes
}

// DetectAgent checks if a pane is running a known coding agent by walking the process tree
func DetectAgent(pane PaneInfo) (AgentType, bool) {
	// Check pane title first (set by conductor when creating coding windows)
	titleLower := strings.ToLower(pane.Title)
	if strings.Contains(titleLower, "claude") {
		return AgentClaudeCode, true
	}
	if strings.Contains(titleLower, "opencode") {
		return AgentOpenCode, true
	}
	if strings.Contains(titleLower, "codex") {
		return AgentCodex, true
	}

	// Check current command
	cmdLower := strings.ToLower(pane.Command)
	if cmdLower == "claude" || strings.Contains(cmdLower, "claude-code") {
		return AgentClaudeCode, true
	}
	if cmdLower == "opencode" {
		return AgentOpenCode, true
	}
	if cmdLower == "codex" {
		return AgentCodex, true
	}

	// Walk process tree (up to 3 levels deep) to find agent process
	if pane.PanePID > 0 {
		if agent, ok := walkProcessTree(pane.PanePID, 3); ok {
			return agent, true
		}
	}

	return AgentUnknown, false
}

// walkProcessTree walks child processes looking for agent binaries
func walkProcessTree(pid int, maxDepth int) (AgentType, bool) {
	if maxDepth <= 0 || pid <= 0 {
		return AgentUnknown, false
	}

	// Get child processes
	cmd := exec.Command("pgrep", "-P", fmt.Sprintf("%d", pid))
	out, err := cmd.Output()
	if err != nil {
		return AgentUnknown, false
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		childPID, err := strconv.Atoi(line)
		if err != nil {
			continue
		}

		// Get the command name of this child
		psCmd := exec.Command("ps", "-p", fmt.Sprintf("%d", childPID), "-o", "comm=")
		psOut, err := psCmd.Output()
		if err != nil {
			continue
		}
		comm := strings.TrimSpace(string(psOut))
		commLower := strings.ToLower(comm)

		if commLower == "claude" || strings.Contains(commLower, "claude") {
			return AgentClaudeCode, true
		}
		if commLower == "opencode" {
			return AgentOpenCode, true
		}
		if commLower == "codex" {
			return AgentCodex, true
		}

		// Recurse into children
		if agent, ok := walkProcessTree(childPID, maxDepth-1); ok {
			return agent, true
		}
	}

	return AgentUnknown, false
}

// FindJSONLPath finds the most recent JSONL file for a Claude Code session in a directory
func FindJSONLPath(workDir string) string {
	// Claude Code stores JSONL at ~/.claude/projects/<encoded-path>/<session-id>.jsonl
	// The path encoding replaces "/" with "-" and prepends "-"
	encoded := encodeProjectPath(workDir)
	projectDir := claudeProjectsDir() + "/" + encoded

	// Find the most recently modified JSONL file
	cmd := exec.Command("ls", "-t", projectDir)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasSuffix(line, ".jsonl") {
			return projectDir + "/" + line
		}
	}
	return ""
}

// encodeProjectPath encodes a directory path the way Claude Code does
func encodeProjectPath(dir string) string {
	// Replace "/" with "-" (Claude Code convention)
	return strings.ReplaceAll(dir, "/", "-")
}

// claudeProjectsDir returns the path to Claude Code's project data
func claudeProjectsDir() string {
	home, _ := exec.Command("sh", "-c", "echo $HOME").Output()
	return strings.TrimSpace(string(home)) + "/.claude/projects"
}

// GetGitBranch returns the current git branch for a directory
func GetGitBranch(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GetWorkingDir returns the working directory for a tmux pane
func GetWorkingDir(paneID string) string {
	cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_current_path}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
