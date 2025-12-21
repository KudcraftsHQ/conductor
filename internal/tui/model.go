package tui

import (
	"sort"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/tui/keys"
	"github.com/hammashamzah/conductor/internal/tui/styles"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// Model is the main TUI state
type Model struct {
	// Config
	config *config.Config

	// Styles
	styles *styles.Styles

	// UI state
	width  int
	height int
	ready  bool

	// Navigation
	currentView View
	prevView    View

	// List state
	cursor          int
	offset          int // For scrolling
	projectNames    []string
	worktreeNames   []string
	selectedProject string

	// Filter
	filter     string
	filterMode bool

	// Help
	help    help.Model
	keyMap  keys.KeyMap
	showHelp bool

	// Create worktree modal
	createInput     textinput.Model
	createPortInput textinput.Model
	createFocused   int    // 0 = branch, 1 = ports
	createError     string // Error message to show in dialog

	// Confirm delete
	deleteTarget     string
	deleteTargetType string // "project" or "worktree"

	// Status message
	statusMessage string
	statusIsError bool

	// Workspace manager
	wsManager *workspace.Manager

	// Logs view
	logsWorktree string // worktree name for logs view
	logsScroll   int    // scroll offset for logs

	// Spinner for setup status
	spinner spinner.Model
}

// NewModel creates a new TUI model
func NewModel(cfg *config.Config) *Model {
	ti := textinput.New()
	ti.Placeholder = "branch-name"
	ti.CharLimit = 100
	ti.Width = 40

	pi := textinput.New()
	pi.Placeholder = "1"
	pi.CharLimit = 2
	pi.Width = 10

	h := help.New()
	h.ShowAll = false

	s := spinner.New()
	s.Spinner = spinner.Dot

	m := &Model{
		config:          cfg,
		styles:          styles.DefaultStyles(),
		currentView:     ViewProjects,
		help:            h,
		keyMap:          keys.DefaultKeyMap(),
		createInput:     ti,
		createPortInput: pi,
		wsManager:       workspace.NewManager(cfg),
		spinner:         s,
	}

	m.refreshProjectList()
	return m
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *Model) refreshProjectList() {
	m.projectNames = make([]string, 0, len(m.config.Projects))
	for name := range m.config.Projects {
		m.projectNames = append(m.projectNames, name)
	}
	sort.Strings(m.projectNames)
}

func (m *Model) refreshWorktreeList() {
	if m.selectedProject == "" {
		m.worktreeNames = nil
		return
	}

	project, ok := m.config.Projects[m.selectedProject]
	if !ok {
		m.worktreeNames = nil
		return
	}

	m.worktreeNames = make([]string, 0, len(project.Worktrees))
	for name, wt := range project.Worktrees {
		// Skip root - it's not a worktree
		if wt.IsRoot {
			continue
		}
		m.worktreeNames = append(m.worktreeNames, name)
	}

	sort.Strings(m.worktreeNames)
}

func (m *Model) currentListLength() int {
	switch m.currentView {
	case ViewProjects:
		return len(m.projectNames)
	case ViewWorktrees:
		return len(m.worktreeNames)
	case ViewPorts:
		return len(m.config.GetAllPortInfo())
	default:
		return 0
	}
}

func (m *Model) selectedProjectName() string {
	if m.cursor >= 0 && m.cursor < len(m.projectNames) {
		return m.projectNames[m.cursor]
	}
	return ""
}

func (m *Model) selectedWorktreeName() string {
	if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
		return m.worktreeNames[m.cursor]
	}
	return ""
}

func (m *Model) setStatus(msg string, isError bool) {
	m.statusMessage = msg
	m.statusIsError = isError
}

func (m *Model) clearStatus() {
	m.statusMessage = ""
	m.statusIsError = false
}

// tableHeight returns available height for table content
func (m *Model) tableHeight() int {
	// Header (1) + tabs (1) + title bar (1) + footer (2) + borders (2)
	return m.height - 7
}

// ensureCursorVisible adjusts offset to keep cursor visible
func (m *Model) ensureCursorVisible() {
	tableHeight := m.tableHeight()
	if tableHeight <= 0 {
		return
	}

	if m.cursor < m.offset {
		m.offset = m.cursor
	} else if m.cursor >= m.offset+tableHeight {
		m.offset = m.cursor - tableHeight + 1
	}
}
