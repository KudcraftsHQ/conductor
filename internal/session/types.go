package session

import "time"

// AgentType identifies the coding agent
type AgentType string

const (
	AgentClaudeCode AgentType = "claude-code"
	AgentOpenCode   AgentType = "opencode"
	AgentCodex      AgentType = "codex"
	AgentUnknown    AgentType = "unknown"
)

// AgentStatus represents the current state of an agent session
type AgentStatus string

const (
	StatusIdle        AgentStatus = "idle"         // No activity / just launched
	StatusRunning     AgentStatus = "running"      // Model is thinking/generating
	StatusToolRunning AgentStatus = "tool-running" // Executing a tool (bash, edit, etc.)
	StatusDone        AgentStatus = "done"         // Completed turn, awaiting input
	StatusError       AgentStatus = "error"        // Error occurred
	StatusWaiting     AgentStatus = "waiting"      // Awaiting user approval (permission prompt)
	StatusInterrupted AgentStatus = "interrupted"  // User interrupted (Ctrl+C)
	StatusStale       AgentStatus = "stale"        // No file growth while supposedly running
)

// IsTerminal returns true if the status represents a completed state
func (s AgentStatus) IsTerminal() bool {
	switch s {
	case StatusDone, StatusError, StatusInterrupted, StatusStale:
		return true
	}
	return false
}

// IsActive returns true if the agent is actively working
func (s AgentStatus) IsActive() bool {
	switch s {
	case StatusRunning, StatusToolRunning:
		return true
	}
	return false
}

// Priority returns the display priority (higher = more important to show)
func (s AgentStatus) Priority() int {
	switch s {
	case StatusToolRunning:
		return 7
	case StatusRunning:
		return 6
	case StatusError:
		return 5
	case StatusStale:
		return 4
	case StatusInterrupted:
		return 3
	case StatusWaiting:
		return 2
	case StatusDone:
		return 1
	case StatusIdle:
		return 0
	}
	return 0
}

// Session represents a single AI agent session running in a tmux pane
type Session struct {
	// Identity
	Name       string    // Worktree/branch name (e.g., "feature-login")
	Agent      AgentType // Which agent is running
	WindowName string    // Tmux window name (e.g., "myproject/feature-x")
	PaneID     string    // Tmux pane ID (e.g., "%5")
	PanePID    int       // Process ID of the pane shell

	// Context
	Dir    string // Working directory
	Branch string // Git branch

	// Status
	Status    AgentStatus // Current agent status
	UpdatedAt time.Time   // Last status update time
	Alive     bool        // Is the pane process still alive

	// JSONL tracking (Claude Code)
	JSONLPath     string    // Path to the JSONL session file
	LastFileSize  int64     // Last known file size (for growth detection)
	LastGrowthAt  time.Time // Last time the file grew
	ToolUseSeenAt time.Time // When tool_use was last seen (for waiting detection)
}

// Icon returns a single-character icon for the status
func (s *Session) Icon() string {
	switch s.Status {
	case StatusRunning:
		return "⚡"
	case StatusToolRunning:
		return "⚙"
	case StatusDone:
		return "✓"
	case StatusError:
		return "✗"
	case StatusWaiting:
		return "◉"
	case StatusInterrupted:
		return "⚠"
	case StatusStale:
		return "⚠"
	case StatusIdle:
		return "○"
	}
	return "·"
}

// StatusText returns a short human-readable status string
func (s *Session) StatusText() string {
	switch s.Status {
	case StatusRunning:
		return "running"
	case StatusToolRunning:
		return "tool use"
	case StatusDone:
		return "done"
	case StatusError:
		return "error"
	case StatusWaiting:
		return "waiting"
	case StatusInterrupted:
		return "interrupted"
	case StatusStale:
		return "stale"
	case StatusIdle:
		return "idle"
	}
	return "unknown"
}
