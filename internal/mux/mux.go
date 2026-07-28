// Package mux abstracts the terminal multiplexer conductor drives.
//
// Conductor provisions worktrees, ports and databases; the multiplexer is only
// the rendering layer that hosts the coding-agent and dev-server panes. Two
// implementations exist: tmux (the default) and herdr (https://herdr.dev).
package mux

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/session"
)

// Kind identifies a multiplexer implementation.
type Kind string

const (
	KindTmux  Kind = "tmux"
	KindHerdr Kind = "herdr"
	// KindAuto resolves to herdr when conductor is running inside a herdr pane
	// or tmux is unavailable, and to tmux otherwise.
	KindAuto Kind = "auto"
)

// Multiplexer is the set of operations conductor needs from a terminal
// multiplexer. A "window" is the per-worktree container holding the coding
// agent pane and the dev server pane: a tmux window, or a herdr workspace.
type Multiplexer interface {
	// Kind returns the implementation identifier.
	Kind() Kind
	// CheckInstalled reports whether the multiplexer binary is available.
	CheckInstalled() error
	// InstallGuide returns human-readable install instructions.
	InstallGuide() string

	// IsInsideSession reports whether the current process runs inside any
	// session of this multiplexer.
	IsInsideSession() bool
	// IsInsideConductorSession reports whether the current process runs inside
	// conductor's own session.
	IsInsideConductorSession() bool
	// SessionName returns the name of conductor's session.
	SessionName() string
	// StartSession starts or attaches to conductor's session. On success it
	// replaces the current process and does not return.
	StartSession() error
	// DetachSession detaches all clients, leaving panes running.
	DetachSession() error

	// WindowName returns the window name used for a worktree.
	WindowName(project, branch string) string
	// WindowExists reports whether a worktree's window is currently open.
	WindowExists(project, branch string) bool
	// ListWindowNames returns the names of all open windows in the session.
	ListWindowNames() []string
	// CreateCodingWindow opens a worktree window with an agent pane and a dev
	// server pane.
	CreateCodingWindow(project, branch, worktreePath string, agent codingagent.Agent) error
	// CreateCodingWindowWithTask is CreateCodingWindow with the agent pre-loaded
	// with a task prompt.
	CreateCodingWindowWithTask(project, branch, worktreePath, taskPrompt string, agent codingagent.Agent) error
	// KillWindow closes a worktree's window.
	KillWindow(project, branch string) error
	// FocusWindow brings a worktree's window to the foreground.
	FocusWindow(project, branch string) error
	// KillOtherWindows closes every window except the current one, so the TUI
	// can quit cleanly.
	KillOtherWindows()

	// StartAgentPane opens a detached window running argv and returns the ID of
	// the pane the process runs in.
	StartAgentPane(windowName, workDir string, argv []string, paneTitle string) (string, error)
	// PaneExists reports whether a pane is still alive.
	PaneExists(paneID string) bool
	// GetPaneCommand returns the foreground command running in a pane, or "" if
	// it cannot be determined.
	GetPaneCommand(paneID string) string

	// UpdateTabTitles annotates window names with per-agent status icons.
	// Implementations whose UI already surfaces agent status may no-op.
	UpdateTabTitles(sessions []*session.Session)
	// TracksAgentStatus reports whether the multiplexer surfaces agent status
	// natively. When true, conductor does not run its own session tracker.
	TracksAgentStatus() bool
}

// FromConfig returns the Multiplexer selected by cfg. The CONDUCTOR_MUX
// environment variable overrides the config value.
func FromConfig(cfg *config.Config) Multiplexer {
	if env := os.Getenv("CONDUCTOR_MUX"); env != "" {
		return Resolve(ParseKind(env))
	}
	if cfg == nil {
		return auto()
	}
	return Resolve(ParseKind(cfg.Defaults.Multiplexer))
}

// Current loads the global config and returns the selected Multiplexer.
func Current() Multiplexer {
	cfg, _ := config.Load()
	return FromConfig(cfg)
}

// Resolve returns the Multiplexer for the given kind. KindAuto and any
// unrecognized value fall back to the automatic policy.
func Resolve(kind Kind) Multiplexer {
	switch kind {
	case KindTmux:
		return Tmux()
	case KindHerdr:
		return Herdr()
	default:
		return auto()
	}
}

// ParseKind converts a config string to a Kind, defaulting to KindAuto.
func ParseKind(s string) Kind {
	switch Kind(s) {
	case KindTmux:
		return KindTmux
	case KindHerdr:
		return KindHerdr
	default:
		return KindAuto
	}
}

// auto picks herdr when conductor is already running inside a herdr pane, or
// when herdr is installed and tmux is not. Otherwise it picks tmux.
func auto() Multiplexer {
	if os.Getenv("HERDR_PANE_ID") != "" {
		return Herdr()
	}
	_, tmuxErr := exec.LookPath("tmux")
	_, herdrErr := exec.LookPath("herdr")
	if tmuxErr != nil && herdrErr == nil {
		return Herdr()
	}
	return Tmux()
}

// ErrUnsupported reports an operation a multiplexer does not implement.
type ErrUnsupported struct {
	Kind Kind
	Op   string
}

func (e *ErrUnsupported) Error() string {
	return fmt.Sprintf("%s does not support %s", e.Kind, e.Op)
}
