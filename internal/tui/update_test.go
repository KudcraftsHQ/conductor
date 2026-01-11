package tui

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/stretchr/testify/assert"
)

// createTestModel creates a minimal test model with mock config
func createTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := &config.Config{
		Projects:        make(map[string]*config.Project),
		PortAllocations: make(map[string]*config.PortAlloc),
		Updates: config.UpdateSettings{
			AutoCheck:   false,
			NotifyInTUI: false,
		},
	}
	s := store.New(cfg, store.WithDisableSave())
	m := NewModelWithStore(cfg, s, "test")
	// Set ready state (normally happens on WindowSizeMsg)
	m.width = 80
	m.height = 24
	m.ready = true
	return m
}

// createTestModelWithProjects creates a model with test projects
func createTestModelWithProjects(t *testing.T, projects map[string]*config.Project) *Model {
	t.Helper()
	cfg := &config.Config{
		Projects:        projects,
		PortAllocations: make(map[string]*config.PortAlloc),
		Updates: config.UpdateSettings{
			AutoCheck:   false,
			NotifyInTUI: false,
		},
	}
	s := store.New(cfg, store.WithDisableSave())
	m := NewModelWithStore(cfg, s, "test")
	m.width = 80
	m.height = 24
	m.ready = true
	m.refreshProjectList()
	return m
}

// TestUpdate_WindowSize tests that WindowSizeMsg sets dimensions and ready state
func TestUpdate_WindowSize(t *testing.T) {
	cfg := &config.Config{
		Projects:        make(map[string]*config.Project),
		PortAllocations: make(map[string]*config.PortAlloc),
	}
	s := store.New(cfg, store.WithDisableSave())
	m := NewModelWithStore(cfg, s, "test")

	// Initially not ready
	assert.False(t, m.ready)

	// Send window size message
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated := newModel.(*Model)

	assert.True(t, updated.ready)
	assert.Equal(t, 120, updated.width)
	assert.Equal(t, 40, updated.height)
}

// TestUpdate_Navigation tests basic navigation state transitions
func TestUpdate_Navigation(t *testing.T) {
	tests := []struct {
		name         string
		initialView  View
		key          string
		expectedView View
		setup        func(*Model)
	}{
		{
			name:         "press 1 switches to projects view",
			initialView:  ViewWorktrees,
			key:          "1",
			expectedView: ViewProjects,
		},
		{
			name:         "press 2 switches to worktrees view when project selected",
			initialView:  ViewProjects,
			key:          "2",
			expectedView: ViewWorktrees,
			setup: func(m *Model) {
				m.selectedProject = "test-project"
			},
		},
		{
			name:         "press 2 stays in projects view when no project selected",
			initialView:  ViewProjects,
			key:          "2",
			expectedView: ViewProjects,
		},
		{
			name:         "esc in create dialog returns to worktrees",
			initialView:  ViewCreateWorktree,
			key:          "esc",
			expectedView: ViewWorktrees,
		},
		{
			name:         "? opens help",
			initialView:  ViewWorktrees,
			key:          "?",
			expectedView: ViewHelp,
		},
		{
			name:         "? in help closes help",
			initialView:  ViewHelp,
			key:          "?",
			expectedView: ViewWorktrees,
			setup: func(m *Model) {
				m.prevView = ViewWorktrees
			},
		},
		{
			name:         "esc in help closes help",
			initialView:  ViewHelp,
			key:          "esc",
			expectedView: ViewWorktrees,
			setup: func(m *Model) {
				m.prevView = ViewWorktrees
			},
		},
		{
			name:         "q opens quit dialog",
			initialView:  ViewWorktrees,
			key:          "q",
			expectedView: ViewQuit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := createTestModel(t)
			m.currentView = tt.initialView
			if tt.setup != nil {
				tt.setup(m)
			}

			keyMsg := keyMsgFromString(tt.key)
			newModel, _ := m.Update(keyMsg)
			updated := newModel.(*Model)

			assert.Equal(t, tt.expectedView, updated.currentView, "view should be %v", tt.expectedView)
		})
	}
}

// TestUpdate_CursorMovement tests up/down navigation
func TestUpdate_CursorMovement(t *testing.T) {
	// Create model with 5 projects
	projects := make(map[string]*config.Project)
	for i := 0; i < 5; i++ {
		projects[fmt.Sprintf("project-%d", i)] = &config.Project{
			Path:      fmt.Sprintf("/path/to/project-%d", i),
			Worktrees: make(map[string]*config.Worktree),
		}
	}

	tests := []struct {
		name           string
		initialCursor  int
		key            string
		expectedCursor int
	}{
		{
			name:           "j moves cursor down",
			initialCursor:  0,
			key:            "j",
			expectedCursor: 1,
		},
		{
			name:           "down arrow moves cursor down",
			initialCursor:  0,
			key:            "down",
			expectedCursor: 1,
		},
		{
			name:           "k moves cursor up",
			initialCursor:  2,
			key:            "k",
			expectedCursor: 1,
		},
		{
			name:           "up arrow moves cursor up",
			initialCursor:  2,
			key:            "up",
			expectedCursor: 1,
		},
		{
			name:           "k at top stays at 0",
			initialCursor:  0,
			key:            "k",
			expectedCursor: 0,
		},
		{
			name:           "j at bottom stays at max",
			initialCursor:  4,
			key:            "j",
			expectedCursor: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := createTestModelWithProjects(t, projects)
			m.currentView = ViewProjects
			m.cursor = tt.initialCursor

			keyMsg := keyMsgFromString(tt.key)
			newModel, _ := m.Update(keyMsg)
			updated := newModel.(*Model)

			assert.Equal(t, tt.expectedCursor, updated.cursor)
		})
	}
}

// TestUpdate_FilterMode tests filter mode activation/deactivation
func TestUpdate_FilterMode(t *testing.T) {
	t.Run("/ enters filter mode", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewWorktrees
		m.filterMode = false

		newModel, _ := m.Update(keyMsgFromString("/"))
		updated := newModel.(*Model)

		assert.True(t, updated.filterMode)
		assert.Empty(t, updated.filter)
	})

	t.Run("typing in filter mode updates filter", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewWorktrees
		m.filterMode = true
		m.filter = ""

		// Type "test"
		for _, r := range "test" {
			newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			m = newModel.(*Model)
		}

		assert.Equal(t, "test", m.filter)
	})

	t.Run("esc exits filter mode and clears filter", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewWorktrees
		m.filterMode = true
		m.filter = "some filter"

		newModel, _ := m.Update(keyMsgFromString("esc"))
		updated := newModel.(*Model)

		assert.False(t, updated.filterMode)
		assert.Empty(t, updated.filter)
	})

	t.Run("enter exits filter mode but keeps filter", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewWorktrees
		m.filterMode = true
		m.filter = "some filter"

		newModel, _ := m.Update(keyMsgFromString("enter"))
		updated := newModel.(*Model)

		assert.False(t, updated.filterMode)
		assert.Equal(t, "some filter", updated.filter)
	})

	t.Run("backspace removes last character", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewWorktrees
		m.filterMode = true
		m.filter = "test"

		newModel, _ := m.Update(keyMsgFromString("backspace"))
		updated := newModel.(*Model)

		assert.Equal(t, "tes", updated.filter)
	})
}

// TestUpdate_MessageHandling tests async message processing
func TestUpdate_MessageHandling(t *testing.T) {
	t.Run("WorktreeCreatedMsg success updates status", func(t *testing.T) {
		m := createTestModel(t)
		m.selectedProject = "test-project"

		msg := WorktreeCreatedMsg{
			ProjectName:  "test-project",
			WorktreeName: "tokyo",
			Worktree: &config.Worktree{
				Path:   "/path/to/tokyo",
				Branch: "feature/test",
			},
			Success: true,
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Contains(t, updated.statusMessage, "tokyo")
		assert.False(t, updated.statusIsError)
	})

	t.Run("WorktreeCreatedMsg with error shows error status", func(t *testing.T) {
		m := createTestModel(t)
		m.selectedProject = "test-project"

		msg := WorktreeCreatedMsg{
			ProjectName:  "test-project",
			WorktreeName: "tokyo",
			Success:      false,
			Err:          fmt.Errorf("git worktree add failed"),
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.True(t, updated.statusIsError)
		assert.Contains(t, updated.statusMessage, "Failed")
	})

	t.Run("SetupCompleteMsg success shows ready", func(t *testing.T) {
		m := createTestModel(t)

		msg := SetupCompleteMsg{
			ProjectName:  "test-project",
			WorktreeName: "tokyo",
			Success:      true,
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Contains(t, updated.statusMessage, "complete")
		assert.False(t, updated.statusIsError)
	})

	t.Run("SetupCompleteMsg failure shows error", func(t *testing.T) {
		m := createTestModel(t)

		msg := SetupCompleteMsg{
			ProjectName:  "test-project",
			WorktreeName: "tokyo",
			Success:      false,
			Err:          fmt.Errorf("setup script failed"),
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Contains(t, updated.statusMessage, "failed")
		assert.True(t, updated.statusIsError)
	})

	t.Run("WorktreeArchivedMsg success updates status", func(t *testing.T) {
		m := createTestModel(t)

		msg := WorktreeArchivedMsg{
			ProjectName:  "test-project",
			WorktreeName: "tokyo",
			Err:          nil,
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Contains(t, updated.statusMessage, "Archived")
		assert.False(t, updated.statusIsError)
	})

	t.Run("WorktreeArchivedMsg error shows error", func(t *testing.T) {
		m := createTestModel(t)

		msg := WorktreeArchivedMsg{
			ProjectName:  "test-project",
			WorktreeName: "tokyo",
			Err:          fmt.Errorf("failed to archive"),
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Contains(t, updated.statusMessage, "Error")
		assert.True(t, updated.statusIsError)
	})

	t.Run("UpdateCheckMsg with update shows notification", func(t *testing.T) {
		m := createTestModel(t)
		m.config.Updates.NotifyInTUI = true

		msg := UpdateCheckMsg{
			UpdateAvailable: true,
			LatestVersion:   "1.2.0",
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.True(t, updated.updateAvailable)
		assert.Equal(t, "1.2.0", updated.latestVersion)
	})

	t.Run("StatesRecoveredMsg shows recovery message", func(t *testing.T) {
		m := createTestModel(t)

		msg := StatesRecoveredMsg{
			RecoveredCount: 3,
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Contains(t, updated.statusMessage, "Recovered")
		assert.Contains(t, updated.statusMessage, "3")
	})

	t.Run("TunnelStartedMsg success updates status", func(t *testing.T) {
		m := createTestModel(t)

		msg := TunnelStartedMsg{
			ProjectName:  "test-project",
			WorktreeName: "tokyo",
			URL:          "https://example.trycloudflare.com",
			Port:         3100,
			Mode:         "quick",
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Contains(t, updated.statusMessage, "Tunnel active")
		assert.Contains(t, updated.statusMessage, "trycloudflare.com")
		assert.False(t, updated.statusIsError)
		assert.False(t, updated.tunnelStarting)
	})

	t.Run("TunnelStartedMsg error shows error", func(t *testing.T) {
		m := createTestModel(t)
		m.tunnelStarting = true

		msg := TunnelStartedMsg{
			ProjectName:  "test-project",
			WorktreeName: "tokyo",
			Err:          fmt.Errorf("cloudflared not found"),
		}

		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Contains(t, updated.statusMessage, "Tunnel failed")
		assert.True(t, updated.statusIsError)
		assert.False(t, updated.tunnelStarting)
	})
}

// TestUpdate_StatusTimeout tests status message timeout handling
func TestUpdate_StatusTimeout(t *testing.T) {
	t.Run("StatusTimeoutMsg clears matching status", func(t *testing.T) {
		m := createTestModel(t)
		m.statusMessage = "Test message"
		m.statusIsError = false
		setAt := time.Now()
		m.statusSetAt = setAt

		msg := StatusTimeoutMsg{SetAt: setAt}
		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Empty(t, updated.statusMessage)
		assert.False(t, updated.statusIsError)
	})

	t.Run("StatusTimeoutMsg ignores if message changed", func(t *testing.T) {
		m := createTestModel(t)
		m.statusMessage = "New message"
		m.statusIsError = false
		m.statusSetAt = time.Now()

		// Use old timestamp
		msg := StatusTimeoutMsg{SetAt: time.Now().Add(-10 * time.Second)}
		newModel, _ := m.Update(msg)
		updated := newModel.(*Model)

		assert.Equal(t, "New message", updated.statusMessage)
	})
}

// TestUpdate_ProjectSelection tests selecting a project
func TestUpdate_ProjectSelection(t *testing.T) {
	projects := map[string]*config.Project{
		"project-a": {
			Path: "/path/to/project-a",
			Worktrees: map[string]*config.Worktree{
				"tokyo": {Branch: "main", Path: "/path/to/tokyo"},
			},
		},
	}

	t.Run("enter on project navigates to worktrees", func(t *testing.T) {
		m := createTestModelWithProjects(t, projects)
		m.currentView = ViewProjects
		m.cursor = 0

		newModel, cmd := m.Update(keyMsgFromString("enter"))
		updated := newModel.(*Model)

		assert.Equal(t, ViewWorktrees, updated.currentView)
		assert.Equal(t, "project-a", updated.selectedProject)
		assert.NotNil(t, cmd, "should return commands for PR sync and git status")
	})
}

// TestUpdate_ConfirmDelete tests delete confirmation dialog
func TestUpdate_ConfirmDelete(t *testing.T) {
	t.Run("y confirms delete", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewConfirmDelete
		m.deleteTarget = "tokyo"
		m.deleteTargetType = "worktree"
		m.prevView = ViewWorktrees
		m.selectedProject = "test-project"

		newModel, cmd := m.Update(keyMsgFromString("y"))
		updated := newModel.(*Model)

		assert.Equal(t, ViewWorktrees, updated.currentView)
		assert.NotNil(t, cmd, "should return archive command")
	})

	t.Run("n cancels delete", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewConfirmDelete
		m.deleteTarget = "tokyo"
		m.deleteTargetType = "worktree"
		m.prevView = ViewWorktrees

		newModel, cmd := m.Update(keyMsgFromString("n"))
		updated := newModel.(*Model)

		assert.Equal(t, ViewWorktrees, updated.currentView)
		assert.Empty(t, updated.deleteTarget)
		assert.Nil(t, cmd)
	})

	t.Run("esc cancels delete", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewConfirmDelete
		m.deleteTarget = "tokyo"
		m.deleteTargetType = "worktree"
		m.prevView = ViewWorktrees

		newModel, cmd := m.Update(keyMsgFromString("esc"))
		updated := newModel.(*Model)

		assert.Equal(t, ViewWorktrees, updated.currentView)
		assert.Empty(t, updated.deleteTarget)
		assert.Nil(t, cmd)
	})
}

// TestUpdate_QuitDialog tests quit dialog behavior
func TestUpdate_QuitDialog(t *testing.T) {
	t.Run("esc cancels quit", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewQuit
		m.prevView = ViewWorktrees

		newModel, _ := m.Update(keyMsgFromString("esc"))
		updated := newModel.(*Model)

		assert.Equal(t, ViewWorktrees, updated.currentView)
	})

	t.Run("j/k navigates options", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewQuit
		m.quitFocused = 0

		newModel, _ := m.Update(keyMsgFromString("j"))
		updated := newModel.(*Model)
		assert.Equal(t, 1, updated.quitFocused)

		newModel, _ = updated.Update(keyMsgFromString("k"))
		updated = newModel.(*Model)
		assert.Equal(t, 0, updated.quitFocused)
	})
}

// TestUpdate_LogsView tests logs view navigation
func TestUpdate_LogsView(t *testing.T) {
	t.Run("k scrolls up", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewLogs
		m.logsScroll = 5
		m.logsAutoScroll = true

		// Scroll up
		newModel, _ := m.Update(keyMsgFromString("k"))
		updated := newModel.(*Model)
		assert.Equal(t, 4, updated.logsScroll)
		assert.False(t, updated.logsAutoScroll, "scrolling up should disable auto-scroll")
	})

	t.Run("j scrolls down within bounds", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewLogs
		m.logsScroll = 0
		m.logsAutoScroll = false

		// Scroll down - note: j only scrolls if there's content
		// With no logs, maxScroll is 0, so scroll won't increase
		newModel, _ := m.Update(keyMsgFromString("j"))
		updated := newModel.(*Model)
		// Without actual logs, maxScroll is 0
		assert.GreaterOrEqual(t, updated.logsScroll, 0)
	})

	t.Run("g goes to top", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewLogs
		m.logsScroll = 100
		m.logsAutoScroll = true

		newModel, _ := m.Update(keyMsgFromString("g"))
		updated := newModel.(*Model)
		assert.Equal(t, 0, updated.logsScroll)
		assert.False(t, updated.logsAutoScroll)
	})

	t.Run("a toggles auto-scroll", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewLogs
		m.logsAutoScroll = false

		newModel, _ := m.Update(keyMsgFromString("a"))
		updated := newModel.(*Model)
		assert.True(t, updated.logsAutoScroll)

		newModel, _ = updated.Update(keyMsgFromString("a"))
		updated = newModel.(*Model)
		assert.False(t, updated.logsAutoScroll)
	})

	t.Run("esc returns to previous view", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewLogs
		m.prevView = ViewWorktrees
		m.logsWorktree = "tokyo"

		newModel, _ := m.Update(keyMsgFromString("esc"))
		updated := newModel.(*Model)
		assert.Equal(t, ViewWorktrees, updated.currentView)
		assert.Empty(t, updated.logsWorktree)
	})
}

// TestUpdate_StatusHistory tests status history view
func TestUpdate_StatusHistory(t *testing.T) {
	t.Run("H opens status history when not empty", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewWorktrees
		m.statusHistory = []StatusHistoryItem{
			{Message: "Test message", IsError: false, Timestamp: time.Now()},
		}

		newModel, _ := m.Update(keyMsgFromString("H"))
		updated := newModel.(*Model)
		assert.Equal(t, ViewStatusHistory, updated.currentView)
	})

	t.Run("H shows message when history empty", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewWorktrees
		m.statusHistory = nil

		newModel, _ := m.Update(keyMsgFromString("H"))
		updated := newModel.(*Model)
		assert.Equal(t, ViewWorktrees, updated.currentView, "should stay in worktrees view")
		assert.Contains(t, updated.statusMessage, "No message history")
	})

	t.Run("c clears history and returns to previous view", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewStatusHistory
		m.prevView = ViewWorktrees
		m.statusHistory = []StatusHistoryItem{
			{Message: "Test message", IsError: false, Timestamp: time.Now()},
		}

		newModel, _ := m.Update(keyMsgFromString("c"))
		updated := newModel.(*Model)
		// Note: clearing history adds a "History cleared" message
		// So we check it only has that message
		assert.Equal(t, 1, len(updated.statusHistory))
		assert.Contains(t, updated.statusHistory[0].Message, "cleared")
		assert.Equal(t, ViewWorktrees, updated.currentView)
	})
}

// TestUpdate_AllPRsView tests all PRs view
func TestUpdate_AllPRsView(t *testing.T) {
	t.Run("esc returns to worktrees", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewAllPRs
		m.allPRList = []config.PRInfo{{Number: 1, Title: "Test PR"}}

		newModel, _ := m.Update(keyMsgFromString("esc"))
		updated := newModel.(*Model)
		assert.Equal(t, ViewWorktrees, updated.currentView)
		assert.Nil(t, updated.allPRList)
	})

	t.Run("j/k navigates PRs", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewAllPRs
		m.allPRList = []config.PRInfo{
			{Number: 1, Title: "PR 1"},
			{Number: 2, Title: "PR 2"},
			{Number: 3, Title: "PR 3"},
		}
		m.allPRCursor = 0

		newModel, _ := m.Update(keyMsgFromString("j"))
		updated := newModel.(*Model)
		assert.Equal(t, 1, updated.allPRCursor)

		newModel, _ = updated.Update(keyMsgFromString("j"))
		updated = newModel.(*Model)
		assert.Equal(t, 2, updated.allPRCursor)

		newModel, _ = updated.Update(keyMsgFromString("k"))
		updated = newModel.(*Model)
		assert.Equal(t, 1, updated.allPRCursor)
	})
}

// TestUpdate_TunnelModal tests tunnel modal behavior
func TestUpdate_TunnelModal(t *testing.T) {
	t.Run("esc closes tunnel modal", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewTunnelModal
		m.prevView = ViewWorktrees
		m.tunnelModalOpen = true

		newModel, _ := m.Update(keyMsgFromString("esc"))
		updated := newModel.(*Model)
		assert.Equal(t, ViewWorktrees, updated.currentView)
		assert.False(t, updated.tunnelModalOpen)
	})

	t.Run("j/k navigates tunnel modes", func(t *testing.T) {
		m := createTestModel(t)
		m.currentView = ViewTunnelModal
		m.tunnelModalMode = 0

		newModel, _ := m.Update(keyMsgFromString("j"))
		updated := newModel.(*Model)
		assert.Equal(t, 1, updated.tunnelModalMode)

		newModel, _ = updated.Update(keyMsgFromString("k"))
		updated = newModel.(*Model)
		assert.Equal(t, 0, updated.tunnelModalMode)
	})
}

// TestUpdate_ConfigReload tests config reload handling
func TestUpdate_ConfigReload(t *testing.T) {
	// Note: "ConfigFileChangedMsg triggers reload" test requires actual config file
	// and is covered by integration tests

	t.Run("ConfigFileChangedMsg debounces rapid reloads", func(t *testing.T) {
		m := createTestModel(t)
		m.lastConfigReload = time.Now() // Recent reload

		newModel, cmd := m.Update(ConfigFileChangedMsg{})
		updated := newModel.(*Model)

		assert.Nil(t, cmd, "should not return command when debouncing")
		assert.Empty(t, updated.statusMessage)
	})
}

// keyMsgFromString creates a tea.KeyMsg from a string
func keyMsgFromString(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	default:
		if len(s) == 1 {
			return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
		}
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}
