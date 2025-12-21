package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/opener"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// Update handles messages and updates state
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case SetupCompleteMsg:
		m.refreshWorktreeList()
		if msg.Success {
			m.setStatus("Setup complete: "+msg.WorktreeName, false)
		} else {
			m.setStatus("Setup failed: "+msg.WorktreeName+" (press 'l' to view logs)", true)
		}
		return m, nil

	case WorktreeArchivedMsg:
		if msg.Err != nil {
			m.setStatus("Error: "+msg.Err.Error(), true)
		} else {
			m.setStatus("Archived worktree: "+msg.WorktreeName, false)
			m.refreshWorktreeList()
			if m.cursor >= len(m.worktreeNames) {
				m.cursor = len(m.worktreeNames) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
		return m, nil

	case ConfigSavedMsg:
		if msg.Err != nil {
			m.setStatus("Error saving config: "+msg.Err.Error(), true)
		}
		return m, nil

	case OpenedMsg:
		if msg.Err != nil {
			m.setStatus("Error opening: "+msg.Err.Error(), true)
		} else {
			m.setStatus("", false) // Clear status on success
		}
		return m, nil

	case RefreshMsg:
		cfg, err := config.Load()
		if err != nil {
			m.setStatus("Error reloading config: "+err.Error(), true)
		} else {
			m.config = cfg
			m.refreshProjectList()
			if m.selectedProject != "" {
				m.refreshWorktreeList()
			}
			m.setStatus("Refreshed", false)
		}
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle filter mode
	if m.filterMode {
		return m.handleFilterInput(msg)
	}

	// Handle create worktree modal
	if m.currentView == ViewCreateWorktree {
		return m.handleCreateWorktreeInput(msg)
	}

	// Handle confirm delete
	if m.currentView == ViewConfirmDelete {
		return m.handleConfirmDelete(msg)
	}

	// Handle help modal
	if m.currentView == ViewHelp {
		if msg.Type == tea.KeyEsc || msg.String() == "?" || msg.String() == "q" {
			m.currentView = m.prevView
			return m, nil
		}
		return m, nil
	}

	// Handle logs view
	if m.currentView == ViewLogs {
		return m.handleLogsView(msg)
	}

	// Global keys
	switch {
	case key.Matches(msg, m.keyMap.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keyMap.Help):
		m.prevView = m.currentView
		m.currentView = ViewHelp
		return m, nil

	case key.Matches(msg, m.keyMap.Filter):
		m.filterMode = true
		m.filter = ""
		return m, nil

	case key.Matches(msg, m.keyMap.Refresh):
		return m, func() tea.Msg { return RefreshMsg{} }

	// Tab navigation with number keys
	case msg.String() == "1":
		if m.currentView != ViewProjects {
			m.currentView = ViewProjects
			m.cursor = 0
			m.offset = 0
		}
		return m, nil

	case msg.String() == "2":
		if m.selectedProject != "" && m.currentView != ViewWorktrees {
			m.currentView = ViewWorktrees
			m.cursor = 0
			m.offset = 0
		}
		return m, nil
	}

	// View-specific keys
	switch m.currentView {
	case ViewProjects:
		return m.handleProjectsView(msg)
	case ViewWorktrees:
		return m.handleWorktreesView(msg)
	case ViewPorts:
		return m.handlePortsView(msg)
	}

	return m, nil
}

func (m *Model) handleProjectsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.cursor < len(m.projectNames)-1 {
			m.cursor++
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Enter):
		if m.cursor >= 0 && m.cursor < len(m.projectNames) {
			m.selectedProject = m.projectNames[m.cursor]
			m.refreshWorktreeList()
			m.cursor = 0
			m.offset = 0
			m.prevView = ViewProjects
			m.currentView = ViewWorktrees
		}

	case key.Matches(msg, m.keyMap.Ports):
		m.prevView = ViewProjects
		m.currentView = ViewPorts
		m.cursor = 0
		m.offset = 0

	case key.Matches(msg, m.keyMap.Delete):
		if m.cursor >= 0 && m.cursor < len(m.projectNames) {
			m.deleteTarget = m.projectNames[m.cursor]
			m.deleteTargetType = "project"
			m.prevView = ViewProjects
			m.currentView = ViewConfirmDelete
		}
	}

	return m, nil
}

func (m *Model) handleWorktreesView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.cursor < len(m.worktreeNames)-1 {
			m.cursor++
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Back):
		m.currentView = ViewProjects
		m.selectedProject = ""
		m.cursor = 0
		m.offset = 0
		m.refreshProjectList()

	case key.Matches(msg, m.keyMap.Create):
		m.createInput.Reset()
		m.createPortInput.Reset()
		m.createFocused = 0
		m.createError = "" // Clear any previous error
		m.createInput.Focus()
		m.prevView = ViewWorktrees
		m.currentView = ViewCreateWorktree

	case key.Matches(msg, m.keyMap.Archive), key.Matches(msg, m.keyMap.Delete):
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			project := m.config.Projects[m.selectedProject]
			if wt := project.Worktrees[wtName]; wt != nil && !wt.IsRoot {
				m.deleteTarget = wtName
				m.deleteTargetType = "worktree"
				m.prevView = ViewWorktrees
				m.currentView = ViewConfirmDelete
			} else {
				m.setStatus("Cannot archive root worktree", true)
			}
		}

	case key.Matches(msg, m.keyMap.Open), key.Matches(msg, m.keyMap.OpenTerminal):
		return m.openWorktree(opener.TerminalITerm)

	case key.Matches(msg, m.keyMap.OpenCursor):
		return m.openWorktreeIDE(opener.IDECursor)

	case key.Matches(msg, m.keyMap.OpenVSCode):
		return m.openWorktreeIDE(opener.IDEVSCode)

	case key.Matches(msg, m.keyMap.Ports):
		m.prevView = ViewWorktrees
		m.currentView = ViewPorts

	case msg.String() == "l":
		// View logs for selected worktree
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			m.logsWorktree = m.worktreeNames[m.cursor]
			m.logsScroll = 0
			m.prevView = ViewWorktrees
			m.currentView = ViewLogs
		}
		m.cursor = 0
		m.offset = 0

	case key.Matches(msg, m.keyMap.Enter):
		// Open in terminal on enter
		return m.openWorktree(opener.TerminalITerm)
	}

	return m, nil
}

func (m *Model) handlePortsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	portInfo := m.config.GetAllPortInfo()

	switch {
	case key.Matches(msg, m.keyMap.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.cursor < len(portInfo)-1 {
			m.cursor++
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Back):
		m.currentView = m.prevView
		m.cursor = 0
		m.offset = 0
	}

	return m, nil
}

func (m *Model) handleFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.filterMode = false
		m.filter = ""
	case tea.KeyEnter:
		m.filterMode = false
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.filter += string(msg.Runes)
		}
	}
	return m, nil
}

func (m *Model) handleCreateWorktreeInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.currentView = ViewWorktrees
		return m, nil

	case tea.KeyTab:
		m.createFocused = (m.createFocused + 1) % 2
		if m.createFocused == 0 {
			m.createInput.Focus()
			m.createPortInput.Blur()
		} else {
			m.createInput.Blur()
			m.createPortInput.Focus()
		}
		return m, nil

	case tea.KeyEnter:
		return m.createWorktree()

	default:
		var cmd tea.Cmd
		if m.createFocused == 0 {
			m.createInput, cmd = m.createInput.Update(msg)
		} else {
			m.createPortInput, cmd = m.createPortInput.Update(msg)
		}
		return m, cmd
	}
}

func (m *Model) handleConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m.executeDelete()
	case "n", "N", "esc":
		m.currentView = m.prevView
		m.deleteTarget = ""
		m.deleteTargetType = ""
	}
	return m, nil
}

func (m *Model) createWorktree() (tea.Model, tea.Cmd) {
	branch := m.createInput.Value()

	portCount := 0
	if m.createPortInput.Value() != "" {
		var err error
		portCount, err = strconv.Atoi(m.createPortInput.Value())
		if err != nil {
			m.createError = "Invalid port count"
			return m, nil
		}
	}

	// Create worktree synchronously so we can show errors in dialog
	name, _, err := m.wsManager.CreateWorktree(m.selectedProject, branch, portCount)
	if err != nil {
		m.createError = err.Error()
		return m, nil
	}

	// Save config
	if err := config.Save(m.config); err != nil {
		m.createError = "Failed to save config: " + err.Error()
		return m, nil
	}

	// Success - close dialog and show worktree list
	m.createError = ""
	m.currentView = ViewWorktrees
	m.refreshWorktreeList()
	m.setStatus("Created worktree: "+name+" (setting up...)", false)

	// Start setup in background
	projectName := m.selectedProject
	worktreeName := name
	return m, func() tea.Msg {
		done := make(chan SetupCompleteMsg)
		m.wsManager.RunSetupAsync(projectName, worktreeName, func(success bool, setupErr error) {
			done <- SetupCompleteMsg{
				ProjectName:  projectName,
				WorktreeName: worktreeName,
				Success:      success,
				Err:          setupErr,
			}
		})
		return <-done
	}
}

func (m *Model) executeDelete() (tea.Model, tea.Cmd) {
	if m.deleteTargetType == "worktree" {
		projectName := m.selectedProject
		wtName := m.deleteTarget

		m.currentView = ViewWorktrees
		m.deleteTarget = ""
		m.deleteTargetType = ""

		return m, func() tea.Msg {
			err := m.wsManager.ArchiveWorktree(projectName, wtName)
			if err != nil {
				return WorktreeArchivedMsg{Err: err}
			}

			if err := config.Save(m.config); err != nil {
				return WorktreeArchivedMsg{Err: err}
			}

			return WorktreeArchivedMsg{
				ProjectName:  projectName,
				WorktreeName: wtName,
			}
		}
	} else if m.deleteTargetType == "project" {
		projectName := m.deleteTarget
		m.currentView = ViewProjects
		m.deleteTarget = ""
		m.deleteTargetType = ""

		return m, func() tea.Msg {
			err := m.config.RemoveProject(projectName)
			if err != nil {
				return ErrorMsg{Err: err}
			}

			if err := config.Save(m.config); err != nil {
				return ErrorMsg{Err: err}
			}

			return RefreshMsg{}
		}
	}

	m.currentView = m.prevView
	return m, nil
}

func (m *Model) openWorktree(termType opener.TerminalType) (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.worktreeNames) {
		return m, nil
	}

	wtName := m.worktreeNames[m.cursor]
	project := m.config.Projects[m.selectedProject]
	wt := project.Worktrees[wtName]

	m.setStatus("Opening "+wtName+"...", false)

	return m, func() tea.Msg {
		err := opener.OpenInITermSplit(wt.Path, "", "conductor run")
		return OpenedMsg{Path: wt.Path, Err: err}
	}
}

func (m *Model) openWorktreeIDE(ideType opener.IDEType) (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.worktreeNames) {
		return m, nil
	}

	wtName := m.worktreeNames[m.cursor]
	project := m.config.Projects[m.selectedProject]
	wt := project.Worktrees[wtName]

	m.setStatus("Opening "+wtName+" in "+string(ideType)+"...", false)

	return m, func() tea.Msg {
		err := opener.OpenInIDE(wt.Path, ideType)
		return OpenedMsg{Path: wt.Path, Err: err}
	}
}

func (m *Model) handleLogsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	logs := workspace.GetSetupManager().GetLogs(m.selectedProject, m.logsWorktree)
	lines := strings.Split(logs, "\n")
	maxScroll := len(lines) - m.tableHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}

	switch {
	case msg.Type == tea.KeyEsc || msg.String() == "q":
		m.currentView = m.prevView
		m.logsWorktree = ""
		m.logsScroll = 0

	case key.Matches(msg, m.keyMap.Up) || msg.String() == "k":
		if m.logsScroll > 0 {
			m.logsScroll--
		}

	case key.Matches(msg, m.keyMap.Down) || msg.String() == "j":
		if m.logsScroll < maxScroll {
			m.logsScroll++
		}

	case msg.String() == "g":
		m.logsScroll = 0

	case msg.String() == "G":
		m.logsScroll = maxScroll
	}

	return m, nil
}
