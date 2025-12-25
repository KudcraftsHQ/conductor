package tui

import (
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/tui/keys"
	"github.com/hammashamzah/conductor/internal/tui/styles"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// claudePRScanInterval is the interval between automatic Claude PR scans
const claudePRScanInterval = 30 * time.Second

// updateCheckInterval is the interval between automatic update checks
const updateCheckInterval = 6 * time.Hour

// Model is the main TUI state
type Model struct {
	// Config
	config *config.Config

	// Version info
	version          string
	updateAvailable  bool
	latestVersion    string
	updateDownloaded bool

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
	help   help.Model
	keyMap keys.KeyMap

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
	logsWorktree   string // worktree name for logs view
	logsScroll     int    // scroll offset for logs
	logsAutoScroll bool   // auto-scroll to bottom
	logsType       string // "setup" or "archive"

	// Quit dialog
	quitFocused int // 0 = kill all, 1 = detach

	// Spinner for setup status
	spinner spinner.Model

	// PR view state
	prList       []config.PRInfo // PRs for current worktree
	prCursor     int             // Selected PR in modal
	prWorktree   string          // Which worktree's PRs we're viewing
	prLoading    bool            // Whether we're fetching PRs

	// Claude PR auto-scan state
	claudePRScanning bool // Whether we're currently scanning for Claude PRs

	// Git status cache
	gitStatusCache   map[string]*workspace.GitStatusInfo
	gitStatusLoading bool
}

// NewModel creates a new TUI model
func NewModel(cfg *config.Config) *Model {
	return NewModelWithVersion(cfg, "dev")
}

// NewModelWithVersion creates a new TUI model with version info
func NewModelWithVersion(cfg *config.Config, version string) *Model {
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
		version:         version,
		styles:          styles.DefaultStyles(),
		currentView:     ViewProjects,
		help:            h,
		keyMap:          keys.DefaultKeyMap(),
		createInput:     ti,
		createPortInput: pi,
		wsManager:       workspace.NewManager(cfg),
		spinner:         s,
		gitStatusCache:  make(map[string]*workspace.GitStatusInfo),
	}

	m.refreshProjectList()
	return m
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.checkForUpdates(),
		m.scheduleClaudePRScan(),
		m.scheduleUpdateCheck(),
	)
}

// checkForUpdates returns a command that checks for updates in the background
func (m *Model) checkForUpdates() tea.Cmd {
	// Skip if auto-check is disabled
	if !m.config.Updates.AutoCheck {
		return nil
	}

	// Skip if we checked recently (within the last 6 hours)
	if m.config.Updates.LastCheck.IsZero() || m.shouldCheckForUpdate() {
		return func() tea.Msg {
			// Import updater package and check for updates
			// We'll do this in a goroutine to not block startup
			return m.performUpdateCheck()
		}
	}

	// If we have cached update info, return it
	if m.config.Updates.LastVersion != "" && m.config.Updates.LastVersion != m.version {
		return func() tea.Msg {
			return UpdateCheckMsg{
				UpdateAvailable: true,
				LatestVersion:   m.config.Updates.LastVersion,
			}
		}
	}

	return nil
}

// shouldCheckForUpdate determines if we should check for updates
func (m *Model) shouldCheckForUpdate() bool {
	// Check if the interval has passed (default 6 hours)
	duration := 6 * time.Hour // Default interval
	return time.Since(m.config.Updates.LastCheck) > duration
}

// performUpdateCheck does the actual update check
func (m *Model) performUpdateCheck() UpdateCheckMsg {
	return m.performUpdateCheckImpl()
}

// scheduleClaudePRScan returns a command that triggers a Claude PR scan after the interval
func (m *Model) scheduleClaudePRScan() tea.Cmd {
	return tea.Tick(claudePRScanInterval, func(t time.Time) tea.Msg {
		return ClaudePRScanTickMsg{}
	})
}

// scheduleUpdateCheck returns a command that triggers an update check after the interval
func (m *Model) scheduleUpdateCheck() tea.Cmd {
	return tea.Tick(updateCheckInterval, func(t time.Time) tea.Msg {
		return UpdateCheckTickMsg{}
	})
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
	for name := range project.Worktrees {
		m.worktreeNames = append(m.worktreeNames, name)
	}

	sort.Strings(m.worktreeNames)
}

func (m *Model) setStatus(msg string, isError bool) {
	m.statusMessage = msg
	m.statusIsError = isError
}

// tableHeight returns available height for table content
func (m *Model) tableHeight() int {
	// Header (1) + tabs (1) + title bar (1) + footer (1)
	return m.height - 4
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

// ensurePRCursorVisible adjusts offset to keep PR cursor visible
func (m *Model) ensurePRCursorVisible() {
	tableHeight := m.tableHeight()
	if tableHeight <= 0 {
		return
	}

	if m.prCursor < m.offset {
		m.offset = m.prCursor
	} else if m.prCursor >= m.offset+tableHeight {
		m.offset = m.prCursor - tableHeight + 1
	}
}
