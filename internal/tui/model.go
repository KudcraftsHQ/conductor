package tui

import (
	"os"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/tui/keys"
	"github.com/hammashamzah/conductor/internal/tui/styles"
	"github.com/hammashamzah/conductor/internal/tunnel"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// claudePRScanInterval is the interval between automatic Claude PR scans
const claudePRScanInterval = 30 * time.Second

// archivedWorktreeInfo contains info about an archived worktree for display
type archivedWorktreeInfo struct {
	Name         string
	Branch       string
	ArchivedAt   time.Time
	HasSetupLogs bool
	HasArchLogs  bool
	ArchiveError string // If archive failed, the error message
}

// StatusHistoryItem represents a single status message in history
type StatusHistoryItem struct {
	Message   string
	IsError   bool
	Timestamp time.Time
}

// statusTimeout is the default timeout for status messages
const statusTimeout = 5 * time.Second

// statusTimeoutError is the timeout for error messages (longer)
const statusTimeoutError = 8 * time.Second

// maxStatusHistory is the maximum number of status messages to keep
const maxStatusHistory = 50

// updateCheckInterval is the interval between automatic update checks
const updateCheckInterval = 6 * time.Hour

// configWatchInterval is the interval for polling config file changes (fallback for IPC)
const configWatchInterval = 5 * time.Second

// Model is the main TUI state
type Model struct {
	// Config and Store
	config *config.Config // Keep for reads during transition
	store  *store.Store   // Store for state mutations

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
	help       help.Model
	keyMap     keys.KeyMap
	helpScroll int // Scroll offset for help modal

	// Create worktree modal
	createInput     textinput.Model
	createPortInput textinput.Model
	createFocused   int    // 0 = branch, 1 = ports
	createError     string // Error message to show in dialog

	// Confirm delete
	deleteTarget     string
	deleteTargetType string // "project" or "worktree"

	// Status message with history and timeout
	statusMessage string
	statusIsError bool
	statusSetAt   time.Time           // When current status was set
	statusHistory []StatusHistoryItem // History of all messages

	// Status history view state
	statusHistoryCursor int
	statusHistoryOffset int

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

	// All PRs view state (project-level PR list)
	allPRList       []config.PRInfo // All PRs for current project
	allPRCursor     int             // Selected PR in all PRs view
	allPRLoading    bool            // Whether we're fetching all PRs
	allPRCreating   bool            // Whether we're creating a worktree from a PR

	// Claude PR auto-scan state
	claudePRScanning bool // Whether we're currently scanning for Claude PRs

	// Git status cache
	gitStatusCache   map[string]*workspace.GitStatusInfo
	gitStatusLoading bool

	// Tunnel state
	tunnelManager    *tunnel.Manager
	tunnelModalOpen  bool
	tunnelModalMode  int // 0 = quick, 1 = named
	tunnelModalPort  int // Which port to tunnel
	tunnelStarting   bool

	// Branch rename dialog state (when branch is already checked out)
	branchRenameInput    textinput.Model
	branchRenameOriginal string        // Original branch name
	branchRenamePR       config.PRInfo // PR info for creating worktree
	branchRenameConflict string        // Path where branch is already checked out

	// Archived worktrees list state
	archivedListCursor   int
	archivedListOffset   int
	archivedListMode     int                           // 0 = archived worktrees, 1 = orphaned branches
	orphanedBranches     []workspace.OrphanedBranchInfo // Cached orphaned branches
	orphanedLoading      bool                          // Loading orphaned branches
	archivedWorktrees    []archivedWorktreeInfo

	// Database view state
	databaseProjects []string          // List of projects with database config
	databaseCursor   int               // Selected project in database view
	databaseOffset   int               // Scroll offset for database view
	databaseSyncing  map[string]bool   // Projects currently syncing
	databaseProgress map[string]string // Current progress message per project

	// Database logs view state
	databaseLogs        map[string][]string // Logs per project (most recent sync)
	databaseLogsProject string              // Which project's logs are being viewed
	databaseLogsScroll  int                 // Scroll offset for logs view
	databaseLogsAuto    bool                // Auto-scroll to bottom

	// Config file watching (for CLI-to-TUI updates)
	configModTime    time.Time // Last known modification time of config file
	lastConfigReload time.Time // For debouncing rapid reloads
}

// NewModel creates a new TUI model
func NewModel(cfg *config.Config) *Model {
	return NewModelWithVersion(cfg, "dev")
}

// NewModelWithVersion creates a new TUI model with version info
func NewModelWithVersion(cfg *config.Config, version string) *Model {
	return NewModelWithStore(cfg, store.New(cfg), version)
}

// NewModelWithStore creates a new TUI model with an existing store (for testing)
func NewModelWithStore(cfg *config.Config, s *store.Store, version string) *Model {
	// Initialize the global setup manager with the store
	workspace.InitSetupManager(s)

	ti := textinput.New()
	ti.Placeholder = "branch-name"
	ti.CharLimit = 100
	ti.Width = 40

	pi := textinput.New()
	pi.Placeholder = "1"
	pi.CharLimit = 2
	pi.Width = 10

	bri := textinput.New()
	bri.Placeholder = "new-branch-name"
	bri.CharLimit = 100
	bri.Width = 50

	h := help.New()
	h.ShowAll = false

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := &Model{
		config:            cfg,
		store:             s,
		version:           version,
		styles:            styles.DefaultStyles(),
		currentView:       ViewProjects,
		help:              h,
		keyMap:            keys.DefaultKeyMap(),
		createInput:       ti,
		createPortInput:   pi,
		branchRenameInput: bri,
		wsManager:         workspace.NewManagerWithStore(cfg, s),
		spinner:           sp,
		gitStatusCache:    make(map[string]*workspace.GitStatusInfo),
		tunnelManager:     tunnel.NewManager(cfg),
		databaseSyncing:   make(map[string]bool),
		databaseProgress:  make(map[string]string),
		databaseLogs:      make(map[string][]string),
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
		m.restoreTunnels(),
		m.recoverInterruptedStates(),
		m.initConfigWatch(),
	)
}

// recoverInterruptedStates checks for worktrees that were left in creating/running state
// when the TUI was closed, and marks them as failed so users can retry
func (m *Model) recoverInterruptedStates() tea.Cmd {
	return func() tea.Msg {
		recovered := m.wsManager.RecoverInterruptedStates()
		return StatesRecoveredMsg{RecoveredCount: recovered}
	}
}

// restoreTunnels restores tunnel state from PID files on TUI startup
func (m *Model) restoreTunnels() tea.Cmd {
	return func() tea.Msg {
		restored, err := m.tunnelManager.RestoreTunnels()
		if err != nil {
			return TunnelRestoredMsg{Err: err}
		}

		// Restore active tunnel states using the store
		m.store.RestoreTunnelStates(restored)

		// Build map of active tunnels for cleanup
		activeTunnels := make(map[string]bool, len(restored))
		for key := range restored {
			activeTunnels[key] = true
		}

		// Clean up stale tunnels (marked active but no longer running)
		m.store.CleanupStaleTunnels(activeTunnels)

		return TunnelRestoredMsg{RestoredCount: len(restored)}
	}
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

// initConfigWatch initializes config watching by storing current mod time and starting the watch loop
func (m *Model) initConfigWatch() tea.Cmd {
	path, err := config.ConfigPath()
	if err == nil {
		if info, err := os.Stat(path); err == nil {
			m.configModTime = info.ModTime()
		}
	}
	return m.scheduleConfigWatch()
}

// scheduleConfigWatch returns a command that triggers a config check after interval
func (m *Model) scheduleConfigWatch() tea.Cmd {
	return tea.Tick(configWatchInterval, func(t time.Time) tea.Msg {
		return ConfigWatchTickMsg{}
	})
}

// checkConfigFile checks if config was modified and returns appropriate message
func (m *Model) checkConfigFile() tea.Msg {
	path, err := config.ConfigPath()
	if err != nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if info.ModTime().After(m.configModTime) {
		return ConfigFileChangedMsg{}
	}
	return nil
}

func (m *Model) refreshProjectList() {
	m.projectNames = make([]string, 0, len(m.config.Projects))
	for name := range m.config.Projects {
		m.projectNames = append(m.projectNames, name)
	}
	sort.Strings(m.projectNames)
}

func (m *Model) refreshWorktreeList() {
	// Get config from store (which has the latest in-memory state)
	// Don't reload from disk - store may not have saved yet due to debounce
	m.config = m.store.GetConfigSnapshot()

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
	m.statusSetAt = time.Now()

	// Add to history if message is not empty
	if msg != "" {
		item := StatusHistoryItem{
			Message:   msg,
			IsError:   isError,
			Timestamp: m.statusSetAt,
		}
		// Prepend to history (most recent first)
		m.statusHistory = append([]StatusHistoryItem{item}, m.statusHistory...)

		// Trim history to max size
		if len(m.statusHistory) > maxStatusHistory {
			m.statusHistory = m.statusHistory[:maxStatusHistory]
		}
	}
}

// setStatusWithTimeout sets a status message and returns a command to clear it after timeout
func (m *Model) setStatusWithTimeout(msg string, isError bool) tea.Cmd {
	m.setStatus(msg, isError)

	if msg == "" {
		return nil
	}

	// Use longer timeout for errors
	timeout := statusTimeout
	if isError {
		timeout = statusTimeoutError
	}

	setAt := m.statusSetAt
	return tea.Tick(timeout, func(t time.Time) tea.Msg {
		return StatusTimeoutMsg{SetAt: setAt}
	})
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

// ensureAllPRCursorVisible adjusts offset to keep all PR cursor visible
func (m *Model) ensureAllPRCursorVisible() {
	tableHeight := m.tableHeight()
	if tableHeight <= 0 {
		return
	}

	if m.allPRCursor < m.offset {
		m.offset = m.allPRCursor
	} else if m.allPRCursor >= m.offset+tableHeight {
		m.offset = m.allPRCursor - tableHeight + 1
	}
}
