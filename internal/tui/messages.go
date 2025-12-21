package tui

import "github.com/hammashamzah/conductor/internal/config"

// View represents the current screen
type View int

const (
	ViewProjects View = iota
	ViewWorktrees
	ViewPorts
	ViewCreateWorktree
	ViewConfirmDelete
	ViewHelp
)

// Messages for async operations

// ConfigLoadedMsg indicates config has been loaded
type ConfigLoadedMsg struct {
	Config *config.Config
	Err    error
}

// ConfigSavedMsg indicates config has been saved
type ConfigSavedMsg struct {
	Err error
}

// WorktreeCreatedMsg indicates a worktree was created
type WorktreeCreatedMsg struct {
	ProjectName  string
	WorktreeName string
	Worktree     *config.Worktree
	Err          error
}

// WorktreeArchivedMsg indicates a worktree was archived
type WorktreeArchivedMsg struct {
	ProjectName  string
	WorktreeName string
	Err          error
}

// ProjectAddedMsg indicates a project was added
type ProjectAddedMsg struct {
	Name string
	Err  error
}

// ProjectRemovedMsg indicates a project was removed
type ProjectRemovedMsg struct {
	Name string
	Err  error
}

// OpenedMsg indicates something was opened
type OpenedMsg struct {
	Path string
	Err  error
}

// ErrorMsg represents an error
type ErrorMsg struct {
	Err error
}

// RefreshMsg triggers a config reload
type RefreshMsg struct{}

// SetupCompleteMsg indicates setup has completed
type SetupCompleteMsg struct {
	ProjectName  string
	WorktreeName string
	Success      bool
	Err          error
}

// ViewLogs views for log display
const (
	ViewLogs View = iota + 100
)
