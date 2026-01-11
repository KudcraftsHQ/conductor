package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WorkflowHarness wraps the Model for workflow testing
type WorkflowHarness struct {
	t       *testing.T
	model   *Model
	history []string
}

// NewWorkflowHarness creates a new workflow test harness
func NewWorkflowHarness(t *testing.T, m *Model) *WorkflowHarness {
	t.Helper()
	return &WorkflowHarness{
		t:       t,
		model:   m,
		history: make([]string, 0),
	}
}

// SendKey sends a key and records the view
func (h *WorkflowHarness) SendKey(key string) tea.Cmd {
	h.t.Helper()
	keyMsg := keyMsgFromString(key)
	newModel, cmd := h.model.Update(keyMsg)
	h.model = newModel.(*Model)
	h.history = append(h.history, h.model.View())
	return cmd
}

// SendMessage sends a message and records the view
func (h *WorkflowHarness) SendMessage(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	newModel, cmd := h.model.Update(msg)
	h.model = newModel.(*Model)
	h.history = append(h.history, h.model.View())
	return cmd
}

// View returns the current view
func (h *WorkflowHarness) View() string {
	return h.model.View()
}

// GetModel returns the current model
func (h *WorkflowHarness) GetModel() *Model {
	return h.model
}

// AssertView asserts the current view
func (h *WorkflowHarness) AssertView(expected View) {
	h.t.Helper()
	assert.Equal(h.t, expected, h.model.currentView, "expected view %v but got %v", expected, h.model.currentView)
}

// AssertStatus asserts the status message contains text
func (h *WorkflowHarness) AssertStatus(contains string) {
	h.t.Helper()
	assert.Contains(h.t, h.model.statusMessage, contains, "status should contain %q", contains)
}

// TestWorkflow_NavigateProjectsToWorktrees tests navigating from projects to worktrees
func TestWorkflow_NavigateProjectsToWorktrees(t *testing.T) {
	projects := map[string]*config.Project{
		"my-project": {
			Path: "/path/to/project",
			Worktrees: map[string]*config.Worktree{
				"tokyo": {Branch: "main", Path: "/wt/tokyo", SetupStatus: config.SetupStatusDone},
				"paris": {Branch: "feature/a", Path: "/wt/paris", SetupStatus: config.SetupStatusDone},
			},
		},
	}

	m := createTestModelWithProjects(t, projects)
	h := NewWorkflowHarness(t, m)

	// Start in projects view
	h.AssertView(ViewProjects)

	// Press enter to select project
	h.SendKey("enter")

	// Should be in worktrees view
	h.AssertView(ViewWorktrees)
	assert.Equal(t, "my-project", h.GetModel().selectedProject)
}

// TestWorkflow_NavigateWorktreesToProjectsAndBack tests back navigation
func TestWorkflow_NavigateWorktreesToProjectsAndBack(t *testing.T) {
	projects := map[string]*config.Project{
		"project-a": {
			Path:      "/path/to/a",
			Worktrees: map[string]*config.Worktree{},
		},
		"project-b": {
			Path:      "/path/to/b",
			Worktrees: map[string]*config.Worktree{},
		},
	}

	m := createTestModelWithProjects(t, projects)
	h := NewWorkflowHarness(t, m)

	// Navigate to worktrees for project-a (first in alphabetical order)
	h.SendKey("enter")
	h.AssertView(ViewWorktrees)
	assert.Equal(t, "project-a", h.GetModel().selectedProject)

	// Go back
	h.SendKey("esc")
	h.AssertView(ViewProjects)

	// Cursor should be on project-a (index 0)
	assert.Equal(t, 0, h.GetModel().cursor)
}

// TestWorkflow_CreateWorktreeFlow tests the create worktree dialog flow
func TestWorkflow_CreateWorktreeFlow(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path:      "/path/to/project",
			Worktrees: map[string]*config.Worktree{},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	h := NewWorkflowHarness(t, m)

	// Press 'c' to open create dialog
	h.SendKey("c")
	h.AssertView(ViewCreateWorktree)
	assert.Equal(t, 0, h.GetModel().createFocused, "should focus branch input")

	// Type branch name
	for _, r := range "feature/new" {
		h.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// Tab to ports field
	h.SendKey("tab")
	assert.Equal(t, 1, h.GetModel().createFocused, "should focus port input")

	// Press esc to cancel
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_CancelCreateWorktree tests canceling create dialog
func TestWorkflow_CancelCreateWorktree(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path:      "/path/to/project",
			Worktrees: map[string]*config.Worktree{},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	h := NewWorkflowHarness(t, m)

	// Open create dialog
	h.SendKey("c")
	h.AssertView(ViewCreateWorktree)

	// Cancel with esc
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_FilterWorktrees tests filtering the worktree list
func TestWorkflow_FilterWorktrees(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path: "/path/to/project",
			Worktrees: map[string]*config.Worktree{
				"tokyo":  {Branch: "main", Path: "/wt/tokyo"},
				"paris":  {Branch: "feature/a", Path: "/wt/paris"},
				"london": {Branch: "feature/b", Path: "/wt/london"},
			},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	m.refreshWorktreeList()
	h := NewWorkflowHarness(t, m)

	// Enter filter mode
	h.SendKey("/")
	assert.True(t, h.GetModel().filterMode)

	// Type filter
	for _, r := range "tok" {
		h.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// Check filter is applied
	assert.Equal(t, "tok", h.GetModel().filter)

	// Exit filter mode
	h.SendKey("enter")
	assert.False(t, h.GetModel().filterMode)
	assert.Equal(t, "tok", h.GetModel().filter, "filter should persist")
}

// TestWorkflow_ViewLogs tests navigation to logs view
func TestWorkflow_ViewLogs(t *testing.T) {
	cfg := &config.Config{
		Projects: map[string]*config.Project{
			"test-project": {
				Path: "/path/to/project",
				Worktrees: map[string]*config.Worktree{
					"tokyo": {Branch: "main", Path: "/wt/tokyo", SetupStatus: config.SetupStatusDone},
				},
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
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	m.worktreeNames = []string{"tokyo"}

	h := NewWorkflowHarness(t, m)

	// Press 'l' to view logs
	h.SendKey("l")
	h.AssertView(ViewLogs)
	assert.Equal(t, "tokyo", h.GetModel().logsWorktree)
	assert.Equal(t, "setup", h.GetModel().logsType)

	// Navigate logs with j/k
	h.SendKey("j")
	h.SendKey("k")

	// Go back
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_QuitFlow tests the quit confirmation flow
func TestWorkflow_QuitFlow(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewWorktrees
	h := NewWorkflowHarness(t, m)

	// Press 'q' to quit
	h.SendKey("q")
	h.AssertView(ViewQuit)

	// Cancel with esc
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_HelpFlow tests opening and closing help
func TestWorkflow_HelpFlow(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewWorktrees
	h := NewWorkflowHarness(t, m)

	// Press '?' to open help
	h.SendKey("?")
	h.AssertView(ViewHelp)

	// Close with '?'
	h.SendKey("?")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_TabNavigation tests quick navigation with number keys
func TestWorkflow_TabNavigation(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path:      "/path/to/project",
			Worktrees: map[string]*config.Worktree{},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	h := NewWorkflowHarness(t, m)

	// Press '1' to go to projects
	h.SendKey("1")
	h.AssertView(ViewProjects)

	// Set selected project and press '2' to go to worktrees
	h.GetModel().selectedProject = "test-project"
	h.SendKey("2")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_ArchiveConfirmation tests archive confirmation flow
func TestWorkflow_ArchiveConfirmation(t *testing.T) {
	cfg := &config.Config{
		Projects: map[string]*config.Project{
			"test-project": {
				Path: "/path/to/project",
				Worktrees: map[string]*config.Worktree{
					"tokyo": {Branch: "main", Path: "/wt/tokyo", SetupStatus: config.SetupStatusDone},
				},
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
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	m.worktreeNames = []string{"tokyo"}

	h := NewWorkflowHarness(t, m)

	// Press 'a' to archive
	h.SendKey("a")
	h.AssertView(ViewConfirmDelete)
	assert.Equal(t, "tokyo", h.GetModel().deleteTarget)
	assert.Equal(t, "worktree", h.GetModel().deleteTargetType)

	// Cancel with 'n'
	h.SendKey("n")
	h.AssertView(ViewWorktrees)
	assert.Empty(t, h.GetModel().deleteTarget)
}

// TestWorkflow_CannotArchiveRoot tests that root worktree cannot be archived
func TestWorkflow_CannotArchiveRoot(t *testing.T) {
	cfg := &config.Config{
		Projects: map[string]*config.Project{
			"test-project": {
				Path: "/path/to/project",
				Worktrees: map[string]*config.Worktree{
					"root": {Branch: "main", Path: "/path/to/project", IsRoot: true},
				},
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
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	m.worktreeNames = []string{"root"}

	h := NewWorkflowHarness(t, m)

	// Try to archive root
	h.SendKey("a")

	// Should stay in worktrees view with error
	h.AssertView(ViewWorktrees)
	assert.True(t, h.GetModel().statusIsError)
	assert.Contains(t, h.GetModel().statusMessage, "Cannot archive root")
}

// TestWorkflow_AllPRsView tests viewing all PRs
func TestWorkflow_AllPRsView(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path:        "/path/to/project",
			GitHubOwner: "owner",
			GitHubRepo:  "repo",
			Worktrees:   map[string]*config.Worktree{},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	h := NewWorkflowHarness(t, m)

	// Press 'M' to view all PRs
	cmd := h.SendKey("M")
	h.AssertView(ViewAllPRs)
	assert.True(t, h.GetModel().allPRLoading)
	assert.NotNil(t, cmd, "should return command to fetch PRs")

	// Go back
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_StatusHistoryFlow tests status history view
func TestWorkflow_StatusHistoryFlow(t *testing.T) {
	m := createTestModel(t)
	m.currentView = ViewWorktrees
	m.statusHistory = []StatusHistoryItem{
		{Message: "Test message", IsError: false, Timestamp: time.Now()},
	}
	h := NewWorkflowHarness(t, m)

	// Press 'H' to view history
	h.SendKey("H")
	h.AssertView(ViewStatusHistory)

	// Go back
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_ArchivedListFlow tests archived worktrees list
func TestWorkflow_ArchivedListFlow(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path: "/path/to/project",
			Worktrees: map[string]*config.Worktree{
				"tokyo": {Branch: "main", Path: "/wt/tokyo", Archived: true, ArchivedAt: time.Now()},
			},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	h := NewWorkflowHarness(t, m)

	// Press 'D' to view archived list
	h.SendKey("D")
	h.AssertView(ViewArchivedList)
	assert.Equal(t, 0, h.GetModel().archivedListMode)

	// Tab to switch to orphaned branches
	h.SendKey("tab")
	assert.Equal(t, 1, h.GetModel().archivedListMode)

	// Tab back to archived worktrees
	h.SendKey("tab")
	assert.Equal(t, 0, h.GetModel().archivedListMode)

	// Go back
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_PortsView tests viewing ports
func TestWorkflow_PortsView(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path: "/path/to/project",
			Worktrees: map[string]*config.Worktree{
				"tokyo": {Branch: "main", Path: "/wt/tokyo", Ports: []int{3100, 3101}},
			},
		},
	}

	cfg := &config.Config{
		Projects: projects,
		PortAllocations: map[string]*config.PortAlloc{
			"3100": {Project: "test-project", Worktree: "tokyo", Index: 0},
			"3101": {Project: "test-project", Worktree: "tokyo", Index: 1},
		},
		Updates: config.UpdateSettings{AutoCheck: false},
	}
	s := store.New(cfg, store.WithDisableSave())
	m := NewModelWithStore(cfg, s, "test")
	m.width = 80
	m.height = 24
	m.ready = true
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees

	h := NewWorkflowHarness(t, m)

	// Press 'p' to view ports
	h.SendKey("p")
	h.AssertView(ViewPorts)

	// Go back
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_CursorPersistence tests that cursor is preserved
func TestWorkflow_CursorPersistence(t *testing.T) {
	projects := map[string]*config.Project{
		"project-a": {Path: "/path/to/a", Worktrees: map[string]*config.Worktree{}},
		"project-b": {Path: "/path/to/b", Worktrees: map[string]*config.Worktree{}},
		"project-c": {Path: "/path/to/c", Worktrees: map[string]*config.Worktree{}},
	}

	m := createTestModelWithProjects(t, projects)
	h := NewWorkflowHarness(t, m)

	// Move cursor to project-b (index 1)
	h.SendKey("j")
	assert.Equal(t, 1, h.GetModel().cursor)

	// Enter project-b
	h.SendKey("enter")
	h.AssertView(ViewWorktrees)
	assert.Equal(t, "project-b", h.GetModel().selectedProject)

	// Go back
	h.SendKey("esc")
	h.AssertView(ViewProjects)

	// Cursor should be back on project-b (index 1)
	assert.Equal(t, 1, h.GetModel().cursor)
}

// TestWorkflow_RefreshInWorktrees tests refresh in worktrees view
func TestWorkflow_RefreshInWorktrees(t *testing.T) {
	projects := map[string]*config.Project{
		"test-project": {
			Path:      "/path/to/project",
			Worktrees: map[string]*config.Worktree{},
		},
	}

	m := createTestModelWithProjects(t, projects)
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	h := NewWorkflowHarness(t, m)

	// Press 'r' to refresh
	cmd := h.SendKey("r")
	require.NotNil(t, cmd, "refresh should return a command")
}

// TestWorkflow_TunnelModalFlow tests tunnel modal navigation
func TestWorkflow_TunnelModalFlow(t *testing.T) {
	cfg := &config.Config{
		Projects: map[string]*config.Project{
			"test-project": {
				Path: "/path/to/project",
				Worktrees: map[string]*config.Worktree{
					"tokyo": {Branch: "main", Path: "/wt/tokyo", SetupStatus: config.SetupStatusDone, Ports: []int{3100}},
				},
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
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	m.worktreeNames = []string{"tokyo"}

	h := NewWorkflowHarness(t, m)

	// Press 'T' to open tunnel modal
	h.SendKey("T")
	h.AssertView(ViewTunnelModal)
	assert.Equal(t, 0, h.GetModel().tunnelModalMode, "should default to quick tunnel")
	assert.Equal(t, 3100, h.GetModel().tunnelModalPort)

	// Navigate to named tunnel
	h.SendKey("j")
	assert.Equal(t, 1, h.GetModel().tunnelModalMode)

	// Back to quick
	h.SendKey("k")
	assert.Equal(t, 0, h.GetModel().tunnelModalMode)

	// Cancel
	h.SendKey("esc")
	h.AssertView(ViewWorktrees)
}

// TestWorkflow_CannotDeleteNonArchived tests that non-archived worktrees can't be deleted
func TestWorkflow_CannotDeleteNonArchived(t *testing.T) {
	cfg := &config.Config{
		Projects: map[string]*config.Project{
			"test-project": {
				Path: "/path/to/project",
				Worktrees: map[string]*config.Worktree{
					"tokyo": {Branch: "main", Path: "/wt/tokyo", SetupStatus: config.SetupStatusDone},
				},
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
	m.selectedProject = "test-project"
	m.currentView = ViewWorktrees
	m.worktreeNames = []string{"tokyo"}

	h := NewWorkflowHarness(t, m)

	// Press 'd' to try to delete
	h.SendKey("d")

	// Should stay in worktrees view with error
	h.AssertView(ViewWorktrees)
	assert.True(t, h.GetModel().statusIsError)
	assert.Contains(t, h.GetModel().statusMessage, "archived first")
}
