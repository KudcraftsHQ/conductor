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

// WorktreeCreatedMsg indicates a worktree git creation completed (async)
type WorktreeCreatedMsg struct {
	ProjectName  string
	WorktreeName string
	Worktree     *config.Worktree
	Success      bool
	Err          error
}

// WorktreeArchivedMsg indicates a worktree was archived
type WorktreeArchivedMsg struct {
	ProjectName  string
	WorktreeName string
	Err          error
}

// WorktreeDeletedMsg indicates a worktree was permanently deleted
type WorktreeDeletedMsg struct {
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
	ViewQuit
	ViewPRs
)

// ArchiveStartedMsg indicates archiving has started for a worktree
type ArchiveStartedMsg struct {
	ProjectName  string
	WorktreeName string
}

// PRsFetchedMsg indicates PRs have been fetched for a worktree
type PRsFetchedMsg struct {
	ProjectName  string
	WorktreeName string
	PRs          []config.PRInfo
	Err          error
}

// PROpenedMsg indicates a PR was opened in browser
type PROpenedMsg struct {
	URL string
	Err error
}

// AllPRsSyncedMsg indicates all PRs have been synced for a project
type AllPRsSyncedMsg struct {
	ProjectName string
	Err         error
}

// UpdateCheckMsg indicates an update check has completed
type UpdateCheckMsg struct {
	UpdateAvailable bool
	LatestVersion   string
	Err             error
}

// UpdateInstalledMsg indicates an update has been installed
type UpdateInstalledMsg struct {
	Version string
	Err     error
}

// AutoSetupClaudePRsMsg indicates Claude PRs auto-setup has completed
type AutoSetupClaudePRsMsg struct {
	ProjectName    string
	NewWorktrees   []string
	ExistingBranch []string
	Errors         []string
	Err            error
	IsManual       bool // true if triggered by user, false if periodic
}

// ClaudePRScanTickMsg triggers a periodic scan for Claude PRs
type ClaudePRScanTickMsg struct{}

// RetrySetupMsg indicates a retry of failed setup has completed
type RetrySetupMsg struct {
	ProjectName  string
	WorktreeName string
	Success      bool
	Err          error
}
