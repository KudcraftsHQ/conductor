package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
)

func init() {
	// Ensure consistent output across environments (no ANSI colors in tests)
	lipgloss.SetColorProfile(termenv.Ascii)
}

// TestView_NotReady tests view output when model is not ready
func TestView_NotReady(t *testing.T) {
	cfg := &config.Config{
		Projects:        make(map[string]*config.Project),
		PortAllocations: make(map[string]*config.PortAlloc),
	}
	s := store.New(cfg, store.WithDisableSave())
	m := NewModelWithStore(cfg, s, "test")
	// Don't set ready

	view := m.View()
	assert.Equal(t, "Loading...", view)
}

// TestView_ProjectsList tests the projects view rendering
func TestView_ProjectsList(t *testing.T) {
	projects := map[string]*config.Project{
		"project-a": {
			Path: "/path/to/project-a",
			Worktrees: map[string]*config.Worktree{
				"tokyo":  {Branch: "main", Path: "/wt/tokyo"},
				"paris":  {Branch: "feature/a", Path: "/wt/paris"},
				"london": {Branch: "feature/b", Path: "/wt/london"},
			},
		},
		"project-b": {
			Path: "/path/to/project-b",
			Worktrees: map[string]*config.Worktree{
				"root": {Branch: "main", Path: "/path/to/project-b", IsRoot: true},
			},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.currentView = ViewProjects
	m.width = 80
	m.height = 24

	view := m.View()

	// Assert key content is present (semantic checks)
	assert.Contains(t, view, "project-a")
	assert.Contains(t, view, "project-b")
	assert.Contains(t, view, "CONDUCTOR", "should show header")
}

// TestView_WorktreesList tests worktrees view with different states
func TestView_WorktreesList(t *testing.T) {
	tests := []struct {
		name      string
		worktrees map[string]*config.Worktree
		asserts   []string // strings that should be in the view
	}{
		{
			name: "mixed statuses",
			worktrees: map[string]*config.Worktree{
				"tokyo":  {Branch: "feature/a", Path: "/wt/tokyo", SetupStatus: config.SetupStatusDone, Ports: []int{3100}},
				"paris":  {Branch: "feature/b", Path: "/wt/paris", SetupStatus: config.SetupStatusRunning},
				"london": {Branch: "feature/c", Path: "/wt/london", SetupStatus: config.SetupStatusFailed},
			},
			asserts: []string{"tokyo", "paris", "london", "feature/a", "feature/b", "feature/c"},
		},
		{
			name:      "empty list",
			worktrees: map[string]*config.Worktree{},
			asserts:   []string{"No worktrees"},
		},
		{
			name: "with archived worktree",
			worktrees: map[string]*config.Worktree{
				"tokyo":  {Branch: "feature/a", Path: "/wt/tokyo", SetupStatus: config.SetupStatusDone},
				"paris":  {Branch: "feature/b", Path: "/wt/paris", Archived: true, ArchivedAt: time.Now()},
			},
			asserts: []string{"tokyo", "paris", "archived"},
		},
		{
			name: "with tunnel active",
			worktrees: map[string]*config.Worktree{
				"tokyo": {
					Branch:      "feature/a",
					Path:        "/wt/tokyo",
					SetupStatus: config.SetupStatusDone,
					Tunnel: &config.TunnelState{
						Active: true,
						URL:    "https://example.trycloudflare.com",
						Mode:   config.TunnelModeQuick,
					},
				},
			},
			asserts: []string{"tokyo", "tunnel"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Projects: map[string]*config.Project{
					"test-project": {
						Path:      "/path/to/project",
						Worktrees: tt.worktrees,
					},
				},
				PortAllocations: make(map[string]*config.PortAlloc),
				Updates:         config.UpdateSettings{AutoCheck: false},
			}
			s := store.New(cfg, store.WithDisableSave())
			m := NewModelWithStore(cfg, s, "test")
			m.width = 80
			m.height = 24
			m.ready = true
			m.currentView = ViewWorktrees
			m.selectedProject = "test-project"
			// Directly set worktreeNames instead of calling refreshWorktreeList
			// which tries to reload config from disk
			m.worktreeNames = make([]string, 0, len(tt.worktrees))
			for name := range tt.worktrees {
				m.worktreeNames = append(m.worktreeNames, name)
			}

			view := strings.ToLower(m.View())

			for _, expected := range tt.asserts {
				assert.Contains(t, view, strings.ToLower(expected), "view should contain %q", expected)
			}
		})
	}
}

// TestView_HelpModal tests the help modal rendering
func TestView_HelpModal(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewHelp
	m.prevView = ViewWorktrees
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	// Verify key bindings are documented
	keybindings := []string{
		"create",
		"archive",
		"navigation",
		"actions",
	}

	for _, kb := range keybindings {
		assert.Contains(t, view, kb, "help should document: %s", kb)
	}
}

// TestView_StatusMessage tests status message rendering
func TestView_StatusMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		isError  bool
		expected string
	}{
		{
			name:     "success message",
			message:  "Worktree tokyo created",
			isError:  false,
			expected: "tokyo created",
		},
		{
			name:     "error message",
			message:  "Failed to create worktree",
			isError:  true,
			expected: "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := createTestModel(t)
			m.currentView = ViewWorktrees
			m.statusMessage = tt.message
			m.statusIsError = tt.isError
			m.width = 80
			m.height = 24

			view := strings.ToLower(m.View())
			assert.Contains(t, view, strings.ToLower(tt.expected))
		})
	}
}

// TestView_CreateWorktreeModal tests create worktree modal rendering
func TestView_CreateWorktreeModal(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewCreateWorktree
	m.selectedProject = "test-project"
	m.width = 80
	m.height = 24
	m.createInput.SetValue("feature/test")

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "create", "should show create in title")
	assert.Contains(t, view, "branch", "should show branch field")
	assert.Contains(t, view, "port", "should show port field")
}

// TestView_CreateWorktreeModalWithError tests create modal with error
func TestView_CreateWorktreeModalWithError(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewCreateWorktree
	m.selectedProject = "test-project"
	m.createError = "Branch already exists"
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "branch already exists", "should show error message")
}

// TestView_ConfirmDeleteModal tests confirm delete modal rendering
func TestView_ConfirmDeleteModal(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewConfirmDelete
	m.prevView = ViewWorktrees
	m.deleteTarget = "tokyo"
	m.deleteTargetType = "worktree"
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "tokyo", "should show target name")
	assert.Contains(t, view, "y", "should show yes option")
	assert.Contains(t, view, "n", "should show no option")
}

// TestView_LogsView tests logs view rendering
func TestView_LogsView(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewLogs
	m.prevView = ViewWorktrees
	m.logsWorktree = "tokyo"
	m.logsType = "setup"
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "tokyo", "should show worktree name")
	assert.Contains(t, view, "logs", "should indicate logs view")
}

// TestView_QuitModal tests quit modal rendering
func TestView_QuitModal(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewQuit
	m.prevView = ViewWorktrees
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "quit", "should show quit in title")
	assert.Contains(t, view, "kill", "should show kill option")
	assert.Contains(t, view, "detach", "should show detach option")
}

// TestView_TunnelModal tests tunnel modal rendering
func TestView_TunnelModal(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path: "/path/to/project",
			Worktrees: map[string]*config.Worktree{
				"tokyo": {Branch: "main", Path: "/wt/tokyo", Ports: []int{3100}},
			},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.currentView = ViewTunnelModal
	m.prevView = ViewWorktrees
	m.selectedProject = "test-project"
	m.refreshWorktreeList()
	m.cursor = 0
	m.tunnelModalPort = 3100
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "tunnel", "should show tunnel in title")
	assert.Contains(t, view, "quick", "should show quick tunnel option")
	assert.Contains(t, view, "named", "should show named tunnel option")
}

// TestView_PRsList tests PR list view rendering
func TestView_PRsList(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewAllPRs
	m.selectedProject = "test-project"
	m.allPRList = []config.PRInfo{
		{Number: 1, Title: "First PR", HeadBranch: "feature/first", State: "open"},
		{Number: 2, Title: "Second PR", HeadBranch: "feature/second", State: "open"},
	}
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "first pr", "should show first PR")
	assert.Contains(t, view, "second pr", "should show second PR")
	assert.Contains(t, view, "feature/first", "should show branch name")
}

// TestView_PRsListEmpty tests empty PR list
func TestView_PRsListEmpty(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewAllPRs
	m.selectedProject = "test-project"
	m.allPRList = []config.PRInfo{}
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "no", "should indicate no PRs")
}

// TestView_PRsListLoading tests PR list loading state
func TestView_PRsListLoading(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewAllPRs
	m.selectedProject = "test-project"
	m.allPRList = nil
	m.allPRLoading = true
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	// Actual message is "fetching all prs from github"
	assert.Contains(t, view, "fetching", "should show loading state")
}

// TestView_ArchivedList tests archived worktrees list
func TestView_ArchivedList(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path: "/path/to/project",
			Worktrees: map[string]*config.Worktree{
				"tokyo": {Branch: "main", Path: "/wt/tokyo", Archived: true, ArchivedAt: time.Now()},
				"paris": {Branch: "feature/a", Path: "/wt/paris", Archived: true, ArchivedAt: time.Now().Add(-time.Hour)},
			},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.currentView = ViewArchivedList
	m.selectedProject = "test-project"
	m.archivedListMode = 0 // archived worktrees mode
	m.buildArchivedWorktreesList()
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "tokyo", "should show archived worktree")
	assert.Contains(t, view, "paris", "should show second archived worktree")
}

// TestView_StatusHistory tests status history view
func TestView_StatusHistory(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewStatusHistory
	m.prevView = ViewWorktrees
	m.statusHistory = []StatusHistoryItem{
		{Message: "First message", IsError: false, Timestamp: time.Now()},
		{Message: "Error message", IsError: true, Timestamp: time.Now().Add(-time.Minute)},
		{Message: "Info message", IsError: false, Timestamp: time.Now().Add(-2 * time.Minute)},
	}
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	assert.Contains(t, view, "first message", "should show first message")
	assert.Contains(t, view, "error message", "should show error message")
	assert.Contains(t, view, "info message", "should show info message")
}

// TestView_Tabs tests tab rendering
func TestView_Tabs(t *testing.T) {
	tests := []struct {
		name        string
		currentView View
		hasProject  bool
		asserts     []string
	}{
		{
			name:        "in projects view",
			currentView: ViewProjects,
			hasProject:  false,
			asserts:     []string{"projects"},
		},
		{
			name:        "in worktrees view",
			currentView: ViewWorktrees,
			hasProject:  true,
			asserts:     []string{"projects", "worktrees"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := createTestModel(t)
			m.currentView = tt.currentView
			if tt.hasProject {
				m.selectedProject = "test-project"
			}
			m.width = 80
			m.height = 24

			view := strings.ToLower(m.View())

			for _, expected := range tt.asserts {
				assert.Contains(t, view, expected, "tabs should show: %s", expected)
			}
		})
	}
}

// TestView_UpdateAvailable tests update notification rendering
func TestView_UpdateAvailable(t *testing.T) {
	m := createTestModel(t)
	m.updateAvailable = true
	m.latestVersion = "1.2.0"
	m.currentView = ViewProjects
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	// Shows version like "vtest → v1.2.0"
	assert.Contains(t, view, "1.2.0", "should show new version")
}

// TestView_Filter tests filter indicator rendering
func TestView_Filter(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewWorktrees
	m.filterMode = true
	m.filter = "test"
	m.width = 80
	m.height = 24

	view := strings.ToLower(m.View())

	// Filter is shown as "/test" in the title bar
	assert.Contains(t, view, "/test", "should show filter text in title")
}
