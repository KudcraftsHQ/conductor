package mux

import (
	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/session"
	"github.com/hammashamzah/conductor/internal/tmux"
)

// tmuxMux adapts the internal/tmux package to the Multiplexer interface.
type tmuxMux struct{}

// Tmux returns the tmux-backed Multiplexer.
func Tmux() Multiplexer { return tmuxMux{} }

func (tmuxMux) Kind() Kind              { return KindTmux }
func (tmuxMux) CheckInstalled() error   { return tmux.CheckInstalled() }
func (tmuxMux) InstallGuide() string    { return tmux.TmuxInstallGuide() }
func (tmuxMux) IsInsideSession() bool   { return tmux.IsInsideTmux() }
func (tmuxMux) SessionName() string     { return tmux.SessionName }
func (tmuxMux) StartSession() error     { return tmux.StartSession() }
func (tmuxMux) DetachSession() error    { return tmux.DetachSession() }
func (tmuxMux) TracksAgentStatus() bool { return false }

func (tmuxMux) IsInsideConductorSession() bool { return tmux.IsInsideConductorSession() }

func (tmuxMux) WindowName(project, branch string) string {
	return tmux.WindowName(project, branch)
}

func (tmuxMux) WindowExists(project, branch string) bool {
	return tmux.WindowExists(project, branch)
}

func (tmuxMux) ListWindowNames() []string { return tmux.ListWindowNames() }

func (tmuxMux) CreateCodingWindow(project, branch, worktreePath string, agent codingagent.Agent) error {
	return tmux.CreateCodingWindow(project, branch, worktreePath, agent)
}

func (tmuxMux) CreateCodingWindowWithTask(project, branch, worktreePath, taskPrompt string, agent codingagent.Agent) error {
	return tmux.CreateCodingWindowWithTask(project, branch, worktreePath, taskPrompt, agent)
}

func (tmuxMux) KillWindow(project, branch string) error {
	return tmux.KillWindow(project, branch)
}

func (tmuxMux) FocusWindow(project, branch string) error {
	return tmux.FocusWindow(project, branch)
}

func (tmuxMux) KillOtherWindows() { tmux.KillOtherWindows() }

func (tmuxMux) StartAgentPane(windowName, workDir string, argv []string, paneTitle string) (string, error) {
	return tmux.StartAgentPane(windowName, workDir, argv, paneTitle)
}

func (tmuxMux) PaneExists(paneID string) bool { return tmux.PaneExists(paneID) }

func (tmuxMux) GetPaneCommand(paneID string) string { return tmux.GetPaneCommand(paneID) }

func (tmuxMux) UpdateTabTitles(sessions []*session.Session) { tmux.UpdateTabTitles(sessions) }
