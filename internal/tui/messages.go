package tui

import (
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/workspace"
)

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
	ViewAllPRs // View all PRs for a project (not just one worktree's branch)
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

// UpdateCheckTickMsg triggers a periodic update check
type UpdateCheckTickMsg struct{}

// RetrySetupMsg indicates a retry of failed setup has completed
type RetrySetupMsg struct {
	ProjectName  string
	WorktreeName string
	Success      bool
	Err          error
}

// GitStatusFetchedMsg indicates git status has been fetched for all worktrees in a project
type GitStatusFetchedMsg struct {
	ProjectName string
	Statuses    map[string]*workspace.GitStatusInfo
	Err         error
}

// AllProjectPRsFetchedMsg indicates all PRs have been fetched for a project
type AllProjectPRsFetchedMsg struct {
	ProjectName string
	PRs         []config.PRInfo
	Err         error
}

// WorktreeFromPRCreatedMsg indicates a worktree was created from a PR
type WorktreeFromPRCreatedMsg struct {
	ProjectName  string
	WorktreeName string
	PRNumber     int
	Branch       string
	Err          error
}

// TunnelStartedMsg indicates a tunnel was started for a worktree
type TunnelStartedMsg struct {
	ProjectName  string
	WorktreeName string
	URL          string
	Port         int
	Mode         string // "quick" or "named"
	Err          error
}

// TunnelStoppedMsg indicates a tunnel was stopped for a worktree
type TunnelStoppedMsg struct {
	ProjectName  string
	WorktreeName string
	Err          error
}

// TunnelRestoredMsg indicates tunnels were restored on TUI startup
type TunnelRestoredMsg struct {
	RestoredCount int
	Err           error
}

// ViewTunnelModal is the view for tunnel mode selection
const ViewTunnelModal View = iota + 200

// StatesRecoveredMsg indicates interrupted worktree states were recovered on startup
type StatesRecoveredMsg struct {
	RecoveredCount int
}

// ViewBranchRename is the view for branch rename dialog when branch is already checked out
const ViewBranchRename View = iota + 300

// BranchConflictMsg indicates the branch is already checked out in another worktree
type BranchConflictMsg struct {
	ProjectName    string
	OriginalBranch string
	ExistingPath   string
	PRNumber       int
	PR             config.PRInfo
}

// ViewArchivedList is the view for showing archived worktrees with their logs
const ViewArchivedList View = iota + 400

// OrphanedBranchesScannedMsg indicates orphaned branches have been scanned
type OrphanedBranchesScannedMsg struct {
	ProjectName string
	Branches    []workspace.OrphanedBranchInfo
	Err         error
}

// BranchDeletedMsg indicates a branch was deleted
type BranchDeletedMsg struct {
	ProjectName string
	Branch      string
	Err         error
}

// ViewStatusHistory is the view for showing status message history
const ViewStatusHistory View = iota + 500

// StatusTimeoutMsg indicates the status message should be cleared
type StatusTimeoutMsg struct {
	SetAt time.Time // The timestamp when the message was set (to verify it hasn't changed)
}

// ConfigWatchTickMsg triggers periodic config file check (fallback for external changes)
type ConfigWatchTickMsg struct{}

// ConfigFileChangedMsg indicates the config file was modified externally
type ConfigFileChangedMsg struct{}
