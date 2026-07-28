package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
)

// Agent represents a coding agent that can be used in conductor
type Agent string

const (
	ClaudeCode Agent = "claude"
	OpenCode   Agent = "opencode"
	Codex      Agent = "codex"
)

// ContextFileName is the file written to worktrees for agents that don't support --append-system-prompt
const ContextFileName = ".conductor-context.md"

// UsesContextFile reports whether the agent relies on a written context file
// for its system prompt (instead of a CLI flag like --append-system-prompt).
func (a Agent) UsesContextFile() bool {
	switch a {
	case OpenCode, Codex:
		return true
	default:
		return false
	}
}

// BinaryName returns the CLI binary name for the agent
func (a Agent) BinaryName() string {
	switch a {
	case OpenCode:
		return "opencode"
	case Codex:
		return "codex"
	default:
		return "claude"
	}
}

// Label returns a human-readable label for the agent
func (a Agent) Label() string {
	switch a {
	case OpenCode:
		return "OpenCode"
	case Codex:
		return "Codex"
	default:
		return "Claude Code"
	}
}

// PaneLabel returns a short label for tmux pane titles
func (a Agent) PaneLabel() string {
	switch a {
	case OpenCode:
		return "opencode"
	case Codex:
		return "codex"
	default:
		return "claude"
	}
}

// InteractiveArgs returns CLI args for launching the agent in interactive TUI mode.
// For Claude Code, the system prompt is passed via --append-system-prompt.
// For OpenCode and Codex, the system prompt must be written to a file beforehand (see WriteContextFile).
func (a Agent) InteractiveArgs(systemPrompt string) []string {
	switch a {
	case OpenCode:
		return []string{"opencode"}
	case Codex:
		return []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}
	default:
		return []string{
			"env", "CLAUDE_CODE_NO_FLICKER=1",
			"claude",
			"--dangerously-skip-permissions",
			"--append-system-prompt", systemPrompt,
		}
	}
}

// TaskArgs returns CLI args for launching the agent with an initial task prompt in interactive mode.
// For Claude Code, uses --print to send the task prompt.
// For OpenCode, uses --prompt to pre-fill the TUI with the task prompt.
// For Codex, passes the task prompt as a positional argument to seed the session.
func (a Agent) TaskArgs(systemPrompt, taskPrompt string) []string {
	switch a {
	case OpenCode:
		return []string{"opencode", "--prompt", taskPrompt}
	case Codex:
		return []string{"codex", "--dangerously-bypass-approvals-and-sandbox", taskPrompt}
	default:
		return []string{
			"env", "CLAUDE_CODE_NO_FLICKER=1",
			"claude",
			"--dangerously-skip-permissions",
			"--append-system-prompt", systemPrompt,
			"--print", taskPrompt,
		}
	}
}

// OneShotArgs returns CLI args for running a one-shot prompt (no TUI, just get a response).
func (a Agent) OneShotArgs(prompt string) []string {
	switch a {
	case OpenCode:
		return []string{"opencode", "run", prompt}
	case Codex:
		return []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", prompt}
	default:
		return []string{"claude", "--print", prompt}
	}
}

// WriteContextFile writes the system prompt to a markdown file in the worktree
// so agents that don't support --append-system-prompt can pick it up.
// This is used by OpenCode which reads project markdown files.
func WriteContextFile(worktreePath, systemPrompt string) error {
	filePath := filepath.Join(worktreePath, ContextFileName)
	content := fmt.Sprintf("# Conductor Context\n\n%s\n", systemPrompt)
	return os.WriteFile(filePath, []byte(content), 0644)
}

// CleanContextFile removes the context file from a worktree (e.g., on archive).
func CleanContextFile(worktreePath string) {
	filePath := filepath.Join(worktreePath, ContextFileName)
	_ = os.Remove(filePath)
}
