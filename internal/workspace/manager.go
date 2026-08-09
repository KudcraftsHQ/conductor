package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/database"
	"github.com/hammashamzah/conductor/internal/github"
	"github.com/hammashamzah/conductor/internal/mux"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/tmux"
	"github.com/hammashamzah/conductor/internal/tunnel"
	_ "github.com/lib/pq"
)

// Manager handles worktree operations
type Manager struct {
	config *config.Config
	store  *store.Store
}

// NewManager creates a new workspace manager
func NewManager(cfg *config.Config) *Manager {
	return &Manager{config: cfg}
}

// NewManagerWithStore creates a new workspace manager with a store
func NewManagerWithStore(cfg *config.Config, s *store.Store) *Manager {
	return &Manager{config: cfg, store: s}
}

// SetStore sets the store for the manager
func (m *Manager) SetStore(s *store.Store) {
	m.store = s
}

// CreateWorktree creates a new git worktree synchronously (for CLI use)
func (m *Manager) CreateWorktree(projectName, branch string, portCount int) (string, *config.Worktree, error) {
	name, worktree, err := m.PrepareWorktree(projectName, branch, portCount)
	if err != nil {
		return "", nil, err
	}

	project, _ := m.config.GetProject(projectName)

	// Helper to cleanup on error
	cleanup := func() {
		if m.store != nil {
			m.store.FreeWorktreePorts(projectName, name)
			_ = m.store.RemoveWorktree(projectName, name)
		} else {
			m.config.FreeWorktreePorts(projectName, name)
			delete(project.Worktrees, name)
		}
	}

	// Create worktree directory
	if err := os.MkdirAll(filepath.Dir(worktree.Path), 0755); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to create worktrees directory: %w", err)
	}

	// Create git worktree
	var gitErr error
	if GitBranchExists(project.Path, worktree.Branch) {
		gitErr = GitWorktreeAddExisting(project.Path, worktree.Path, worktree.Branch)
	} else {
		gitErr = GitWorktreeAdd(project.Path, worktree.Path, worktree.Branch)
	}

	if gitErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to create git worktree: %w", gitErr)
	}

	// Run setup script synchronously (for CLI use)
	if m.store != nil {
		_ = m.store.SetWorktreeStatus(projectName, name, config.SetupStatusRunning)
	} else {
		worktree.SetupStatus = config.SetupStatusRunning
	}

	setupErr := runSetupSync(project, projectName, name, worktree)
	if setupErr != nil {
		if m.store != nil {
			_ = m.store.SetWorktreeStatus(projectName, name, config.SetupStatusFailed)
		} else {
			worktree.SetupStatus = config.SetupStatusFailed
		}
		// Don't cleanup - worktree was created, just setup failed
		return name, worktree, fmt.Errorf("setup script failed: %w", setupErr)
	}

	if m.store != nil {
		_ = m.store.SetWorktreeStatus(projectName, name, config.SetupStatusDone)
	} else {
		worktree.SetupStatus = config.SetupStatusDone
	}
	return name, worktree, nil
}

// PrepareWorktree allocates ports and creates the worktree entry optimistically
// Returns the worktree name and entry, but does NOT create the git worktree yet
// Call CreateWorktreeAsync to actually create the git worktree in background
func (m *Manager) PrepareWorktree(projectName, branch string, portCount int) (string, *config.Worktree, error) {
	name, err := m.NextWorktreeName(projectName)
	if err != nil {
		return "", nil, err
	}

	// Conductor-created worktrees live in ~/.conductor/<project>/<worktree>.
	// Adopted ones do not, which is why the path is a parameter below.
	worktreePath, err := config.WorktreePath(projectName, name)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get worktree path: %w", err)
	}

	worktree, err := m.RegisterWorktree(projectName, name, branch, worktreePath, portCount)
	if err != nil {
		return "", nil, err
	}
	return name, worktree, nil
}

// NextWorktreeName picks a city name that no project is using.
//
// Remote dev databases are named dev_<city> on a shared server, so a city
// already taken by any project — not just this one — would collide on the
// clone. The name therefore stays globally unique even when the directory on
// disk is named after a branch instead.
func (m *Manager) NextWorktreeName(projectName string) (string, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return "", fmt.Errorf("project '%s' not found", projectName)
	}

	existingNames := make([]string, 0)
	for _, p := range m.config.Projects {
		if p == nil {
			continue
		}
		for name := range p.Worktrees {
			existingNames = append(existingNames, name)
		}
	}

	name := RandomCityExcluding(existingNames)
	if _, exists := project.Worktrees[name]; exists {
		return "", fmt.Errorf("worktree '%s' already exists", name)
	}
	return name, nil
}

// RegisterWorktree allocates ports and records a worktree entry for a directory
// at worktreePath. It does not create the directory, so it serves both
// PrepareWorktree (which is about to make one) and adopt (where T3 Code already
// did). Provision is what fills the directory in afterwards.
func (m *Manager) RegisterWorktree(projectName, name, branch, worktreePath string, portCount int) (*config.Worktree, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}
	if _, exists := project.Worktrees[name]; exists {
		return nil, fmt.Errorf("worktree '%s' already exists", name)
	}

	// Use project default if port count not specified
	if portCount <= 0 {
		portCount = project.DefaultPortsPerWorktree
	}

	ports, err := m.allocatePorts(projectName, name, portCount)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate ports: %w", err)
	}

	// Determine branch name - use worktree name if not specified
	if branch == "" {
		branch = name
	}

	// Create worktree entry with "creating" status
	worktree := config.NewWorktree(worktreePath, branch, false, ports)
	worktree.SetupStatus = config.SetupStatusCreating
	m.assignDatabase(projectName, project, worktree)

	// Add worktree (use store if available for persistence)
	if m.store != nil {
		if err := m.store.AddWorktree(projectName, name, worktree); err != nil {
			m.store.FreePorts(ports)
			return nil, fmt.Errorf("failed to add worktree: %w", err)
		}
	} else {
		project.Worktrees[name] = worktree
	}

	return worktree, nil
}

// allocatePorts reserves ports through the store when there is one, so the
// allocation is persisted, and falls back to the in-memory config otherwise.
func (m *Manager) allocatePorts(projectName, name string, portCount int) ([]int, error) {
	if m.store != nil {
		return m.store.AllocatePorts(projectName, name, portCount)
	}
	return m.config.AllocatePorts(projectName, name, portCount)
}

// assignDatabase names the worktree's local database from its first port.
//
// Remote databases are named later, by the clone in runSetupSync, because their
// name comes from the worktree name rather than a port. That is also why a
// woken worktree keeps its remote database name but gets a new local one: the
// ports it is named after have changed.
func (m *Manager) assignDatabase(projectName string, project *config.Project, worktree *config.Worktree) {
	if project.Database == nil || m.config.Defaults.LocalPostgresURL == "" || len(worktree.Ports) == 0 {
		return
	}
	pattern := project.Database.DBNamePattern
	if pattern == "" {
		pattern = "{project}-{port}"
	}
	worktree.DatabaseName = generateDBName(projectName, worktree.Ports[0], pattern)
	worktree.DatabaseURL = buildWorktreeDBURL(m.config.Defaults.LocalPostgresURL, worktree.DatabaseName)
}

// CreateWorktreeAsync creates the git worktree in background
// Call this after PrepareWorktree and saving the config
func (m *Manager) CreateWorktreeAsync(projectName, worktreeName string, onComplete func(success bool, err error)) error {
	// Use store if available (has latest state), otherwise fall back to config
	var project *config.Project
	var worktree *config.Worktree
	var ok bool

	if m.store != nil {
		project, ok = m.store.GetProject(projectName)
		if !ok {
			return fmt.Errorf("project '%s' not found", projectName)
		}
		worktree, ok = m.store.GetWorktree(projectName, worktreeName)
		if !ok {
			return fmt.Errorf("worktree '%s' not found", worktreeName)
		}
	} else {
		project, ok = m.config.GetProject(projectName)
		if !ok {
			return fmt.Errorf("project '%s' not found", projectName)
		}
		worktree, ok = project.Worktrees[worktreeName]
		if !ok {
			return fmt.Errorf("worktree '%s' not found", worktreeName)
		}
	}

	go func() {
		var success bool
		var createErr error

		defer func() {
			if onComplete != nil {
				onComplete(success, createErr)
			}
		}()

		// Create worktree directory
		if err := os.MkdirAll(filepath.Dir(worktree.Path), 0755); err != nil {
			createErr = fmt.Errorf("failed to create worktrees directory: %w", err)
			return
		}

		// Create git worktree
		if GitBranchExists(project.Path, worktree.Branch) {
			createErr = GitWorktreeAddExisting(project.Path, worktree.Path, worktree.Branch)
		} else {
			createErr = GitWorktreeAdd(project.Path, worktree.Path, worktree.Branch)
		}

		if createErr != nil {
			createErr = fmt.Errorf("failed to create git worktree: %w", createErr)
			return
		}

		success = true
	}()

	return nil
}

// RunSetupAsync starts the setup script in the background
func (m *Manager) RunSetupAsync(projectName, worktreeName string, onComplete func(success bool, err error)) error {
	// Use store if available (has latest state), otherwise fall back to config
	var project *config.Project
	var worktree *config.Worktree
	var ok bool

	if m.store != nil {
		project, ok = m.store.GetProject(projectName)
		if !ok {
			return fmt.Errorf("project '%s' not found", projectName)
		}
		worktree, ok = m.store.GetWorktree(projectName, worktreeName)
		if !ok {
			return fmt.Errorf("worktree '%s' not found", worktreeName)
		}
	} else {
		project, ok = m.config.GetProject(projectName)
		if !ok {
			return fmt.Errorf("project '%s' not found", projectName)
		}
		worktree, ok = project.Worktrees[worktreeName]
		if !ok {
			return fmt.Errorf("worktree '%s' not found", worktreeName)
		}
	}

	GetSetupManager().RunSetupAsync(project, projectName, worktreeName, worktree, onComplete)
	return nil
}

// ArchiveWorktree releases a worktree's resources and removes its tree.
//
// This is the destructive end of the lifecycle and it deletes the branch, which
// is why the T3-driven paths call ReleaseResources and RemoveTree separately
// instead: there, a thread being deleted must not be able to destroy commits.
// The worktree entry remains in config so logs can still be viewed.
func (m *Manager) ArchiveWorktree(projectName, worktreeName string) error {
	if err := m.ReleaseResources(projectName, worktreeName); err != nil {
		return err
	}
	return m.RemoveTree(projectName, worktreeName, true)
}

// ReleaseResources frees everything a worktree consumes — its archive script
// runs, its database is dropped, its tunnel and dev window are stopped and its
// ports are returned — and leaves the working tree and branch exactly as they
// are. This is hibernation: the worktree is woken again by Provision.
func (m *Manager) ReleaseResources(projectName, worktreeName string) error {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	if worktree.IsRoot {
		return fmt.Errorf("cannot archive root worktree")
	}

	if worktree.Archived {
		return fmt.Errorf("worktree '%s' is already archived", worktreeName)
	}

	// Run archive script first (logs are saved to file for debugging)
	// We ignore the error - archiving proceeds regardless
	_ = GetSetupManager().RunArchiveScript(project, projectName, worktreeName, worktree)

	// Drop database if one was created for this worktree
	if worktree.DatabaseName != "" {
		// Check if this is a remote database (dev_* prefix indicates remote mode)
		if strings.HasPrefix(worktree.DatabaseName, "dev_") && project.Database != nil && project.Database.SSHHost != "" {
			// Drop remote database via SSH
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = database.RemoteDropDatabase(ctx, project.Database.SSHHost, project.Database.DevURL, worktree.DatabaseName)
			cancel()
		} else if m.config.Defaults.LocalPostgresURL != "" {
			// Drop local database
			_ = dropWorktreeDB(m.config.Defaults.LocalPostgresURL, worktree.DatabaseName)
		}
	}

	// Stop tunnel if active
	if worktree.Tunnel != nil && worktree.Tunnel.Active {
		_ = tunnel.KillProcess(worktree.Tunnel.PID)
		_ = tunnel.DeletePIDFile(projectName, worktreeName)
		if m.store != nil {
			_ = m.store.ClearTunnelState(projectName, worktreeName)
		} else {
			worktree.Tunnel = nil
		}
	}

	// Kill tmux window if it exists
	_ = mux.Current().KillWindow(projectName, worktree.Branch)

	// Free the ports. The entry stays, marked hibernated, so the tree on disk
	// is still recognised as this worktree when it is woken.
	if m.store != nil {
		m.store.FreeWorktreePorts(projectName, worktreeName)
		_ = m.store.HibernateWorktree(projectName, worktreeName)
	} else {
		m.config.FreeWorktreePorts(projectName, worktreeName)
		worktree.Hibernated = true
		worktree.HibernatedAt = time.Now()
		worktree.Ports = nil // Clear ports since they're freed
	}

	return nil
}

// RemoveTree removes the working tree from disk and marks the entry archived,
// so it survives as a tombstone whose logs can still be read.
//
// deleteBranch must be false on anything driven by T3 Code. Deleting a thread
// there is a routine act, and T3's own worktree removal leaves the branch
// alone; matching that means a stray deletion costs a tree that can be
// recreated rather than commits that cannot.
func (m *Manager) RemoveTree(projectName, worktreeName string, deleteBranch bool) error {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	if worktree.IsRoot {
		return fmt.Errorf("cannot remove root worktree")
	}

	// Remove git worktree. A tree that is already gone — T3 removes its own
	// after deleting the last thread bound to it, racing us — is success, not
	// failure.
	if err := GitWorktreeRemove(project.Path, worktree.Path); err != nil {
		// Try to remove directory manually
		_ = os.RemoveAll(worktree.Path)
	}

	if deleteBranch {
		// Delete the branch (ignore error - branch may not exist)
		_ = GitBranchDelete(project.Path, worktree.Branch)
	}

	if m.store != nil {
		m.store.FreeWorktreePorts(projectName, worktreeName)
		_ = m.store.ArchiveWorktree(projectName, worktreeName)
	} else {
		m.config.FreeWorktreePorts(projectName, worktreeName)
		worktree.Archived = true
		worktree.ArchivedAt = time.Now()
		worktree.Hibernated = false
		worktree.Ports = nil // Clear ports since they're freed
	}

	return nil
}

// Provision fills in a worktree whose directory already exists: it allocates
// ports if it has none, rebuilds its database, runs the project's setup script
// and starts its dev server.
//
// It is the wake half of hibernation and the second half of adopt, and it is
// idempotent — a setup hook that fires twice, or an adopt re-run by hand, must
// not produce a second port allocation or a second database.
func (m *Manager) Provision(projectName, worktreeName string) error {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	if _, err := os.Stat(worktree.Path); err != nil {
		return fmt.Errorf("worktree directory %s is missing: %w", worktree.Path, err)
	}

	// A woken worktree has no ports: they were returned when it hibernated, and
	// it gets new ones now. The local database is named after the first port,
	// so it is renamed to match; a remote dev_<city> database keeps its name.
	if len(worktree.Ports) == 0 {
		portCount := project.DefaultPortsPerWorktree
		ports, err := m.allocatePorts(projectName, worktreeName, portCount)
		if err != nil {
			return fmt.Errorf("failed to allocate ports: %w", err)
		}
		worktree.Ports = ports
		if !strings.HasPrefix(worktree.DatabaseName, "dev_") {
			m.assignDatabase(projectName, project, worktree)
		}
	}

	if m.store != nil {
		_ = m.store.SetWorktreeStatus(projectName, worktreeName, config.SetupStatusRunning)
	}
	worktree.SetupStatus = config.SetupStatusRunning

	// Clones the database and runs the project's setup script, which is what
	// regenerates any .env holding the ports that just changed.
	setupErr := runSetupSync(project, projectName, worktreeName, worktree)

	status := config.SetupStatusDone
	if setupErr != nil {
		status = config.SetupStatusFailed
	}
	if m.store != nil {
		_ = m.store.SetWorktreeStatus(projectName, worktreeName, status)
		_ = m.store.WakeWorktree(projectName, worktreeName, worktree.Ports, worktree.DatabaseName, worktree.DatabaseURL)
	} else {
		worktree.SetupStatus = status
		worktree.Hibernated = false
		worktree.HibernatedAt = time.Time{}
	}

	// The dev server goes in a tmux window rather than a T3 terminal: a T3
	// terminal belongs to one thread and dies with the T3 server, while a tmux
	// window is long-lived and shared by every thread bound to this worktree.
	// Under tmux and herdr the normal create path already opened one.
	if mux.Current().Kind() == mux.KindT3 && !tmux.WindowExists(projectName, worktree.Branch) {
		if err := tmux.CreateDevWindow(projectName, worktree.Branch, worktree.Path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not start the dev server: %v\n", err)
		}
	}

	return setupErr
}

// DeleteWorktree permanently removes a worktree from config
// Should only be called on archived worktrees
func (m *Manager) DeleteWorktree(projectName, worktreeName string) error {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	if worktree.IsRoot {
		return fmt.Errorf("cannot delete root worktree")
	}

	if !worktree.Archived {
		return fmt.Errorf("worktree '%s' must be archived before deletion", worktreeName)
	}

	// Remove from config using store (persists to disk)
	if m.store != nil {
		return m.store.RemoveWorktree(projectName, worktreeName)
	}

	// Fallback for cases without store (e.g., CLI)
	delete(project.Worktrees, worktreeName)

	return nil
}

// GetWorktree returns a worktree by name
func (m *Manager) GetWorktree(projectName, worktreeName string) (*config.Worktree, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return nil, fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	return worktree, nil
}

// ListWorktrees returns all worktrees for a project
func (m *Manager) ListWorktrees(projectName string) (map[string]*config.Worktree, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	return project.Worktrees, nil
}

// SyncWorktrees syncs config with actual git worktrees
func (m *Manager) SyncWorktrees(projectName string) error {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	// Get actual git worktrees
	gitWorktrees, err := GitWorktreeList(project.Path)
	if err != nil {
		return fmt.Errorf("failed to list git worktrees: %w", err)
	}

	// Create a set of git worktree paths
	gitSet := make(map[string]bool)
	for _, path := range gitWorktrees {
		gitSet[path] = true
	}

	// Remove worktrees that no longer exist in git
	for name, wt := range project.Worktrees {
		if !gitSet[wt.Path] && !wt.IsRoot {
			if m.store != nil {
				m.store.FreeWorktreePorts(projectName, name)
				_ = m.store.RemoveWorktree(projectName, name)
			} else {
				m.config.FreeWorktreePorts(projectName, name)
				delete(project.Worktrees, name)
			}
		}
	}

	return nil
}

// EnsureGitHubConfig detects GitHub owner/repo from git remote if not set
func (m *Manager) EnsureGitHubConfig(projectName string) error {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	if project.GitHubOwner == "" || project.GitHubRepo == "" {
		owner, repo, err := github.DetectRepoFromRemote(project.Path)
		if err != nil {
			return err
		}
		if m.store != nil {
			_ = m.store.SetGitHubConfig(projectName, owner, repo)
		} else {
			project.GitHubOwner = owner
			project.GitHubRepo = repo
		}
	}

	return nil
}

// SyncPRsForWorktree fetches latest PR info for a worktree's branch
func (m *Manager) SyncPRsForWorktree(projectName, worktreeName string) ([]config.PRInfo, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return nil, fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	// Ensure GitHub config is set
	if err := m.EnsureGitHubConfig(projectName); err != nil {
		return nil, fmt.Errorf("failed to detect GitHub repo: %w", err)
	}

	// Fetch PRs for this branch
	prs, err := github.GetPRsForBranch(project.GitHubOwner, project.GitHubRepo, worktree.Branch)
	if err != nil {
		return nil, err
	}

	// Update worktree with fetched PRs
	if m.store != nil {
		_ = m.store.SetWorktreePRs(projectName, worktreeName, prs)
	} else {
		worktree.PRs = prs
	}

	return prs, nil
}

// SyncAllPRs fetches PR info for all worktrees in a project
func (m *Manager) SyncAllPRs(projectName string) error {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	// Ensure GitHub config is set
	if err := m.EnsureGitHubConfig(projectName); err != nil {
		return fmt.Errorf("failed to detect GitHub repo: %w", err)
	}

	// Fetch PRs for each worktree
	for worktreeName, worktree := range project.Worktrees {
		if worktree.Archived {
			continue
		}
		prs, err := github.GetPRsForBranch(project.GitHubOwner, project.GitHubRepo, worktree.Branch)
		if err != nil {
			// Log error but continue with other worktrees
			continue
		}
		if m.store != nil {
			_ = m.store.SetWorktreePRs(projectName, worktreeName, prs)
		} else {
			project.Worktrees[worktreeName].PRs = prs
		}
	}

	return nil
}

// GetPRs returns cached PRs for a worktree
func (m *Manager) GetPRs(projectName, worktreeName string) ([]config.PRInfo, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return nil, fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	return worktree.PRs, nil
}

// FetchAllProjectPRs fetches all PRs for a project (not filtered by branch)
func (m *Manager) FetchAllProjectPRs(projectName string) ([]config.PRInfo, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Ensure GitHub config is set
	if err := m.EnsureGitHubConfig(projectName); err != nil {
		return nil, fmt.Errorf("failed to detect GitHub repo: %w", err)
	}

	// Fetch all PRs for the repo
	prs, err := github.GetAllPRs(project.GitHubOwner, project.GitHubRepo)
	if err != nil {
		return nil, err
	}

	return prs, nil
}

// CreateWorktreeFromPR creates a worktree for a PR's branch
func (m *Manager) CreateWorktreeFromPR(projectName string, pr config.PRInfo) (string, *config.Worktree, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return "", nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Check if worktree for this branch already exists
	for _, wt := range project.Worktrees {
		if wt.Branch == pr.HeadBranch && !wt.Archived {
			return "", nil, fmt.Errorf("worktree for branch '%s' already exists", pr.HeadBranch)
		}
	}

	// Create worktree for this PR
	name, worktree, err := m.PrepareWorktree(projectName, pr.HeadBranch, project.DefaultPortsPerWorktree)
	if err != nil {
		return "", nil, fmt.Errorf("failed to prepare worktree: %w", err)
	}

	// Store auto-saves, no need for explicit config.Save

	// Queue worktree creation
	GetWorktreeQueue().Enqueue(&WorktreeJob{
		ProjectName:  projectName,
		WorktreeName: name,
		Worktree:     worktree,
		Store:        m.store,
		Manager:      m,
		OnComplete:   nil,
	})

	return name, worktree, nil
}

// CreateWorktreeWithNewBranch creates a worktree for a PR but using a new branch name
// This is used when the original branch is already checked out in another worktree
// The new branch will be created based on the original branch from origin
func (m *Manager) CreateWorktreeWithNewBranch(projectName string, pr config.PRInfo, originalBranch, newBranch string) (string, *config.Worktree, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return "", nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Check if worktree for this new branch already exists
	for _, wt := range project.Worktrees {
		if wt.Branch == newBranch && !wt.Archived {
			return "", nil, fmt.Errorf("worktree for branch '%s' already exists", newBranch)
		}
	}

	// Create worktree with the new branch name
	name, worktree, err := m.PrepareWorktree(projectName, newBranch, project.DefaultPortsPerWorktree)
	if err != nil {
		return "", nil, fmt.Errorf("failed to prepare worktree: %w", err)
	}

	// Associate the PR with this worktree
	worktree.PRs = append(worktree.PRs, pr)
	if m.store != nil {
		_ = m.store.SetWorktreePRs(projectName, name, worktree.PRs)
	}

	// Queue worktree creation with special handling for new branch based on original
	GetWorktreeQueue().Enqueue(&WorktreeJob{
		ProjectName:  projectName,
		WorktreeName: name,
		Worktree:     worktree,
		Store:        m.store,
		Manager:      m,
		OnComplete:   nil,
		BaseBranch:   originalBranch, // Create new branch based on this
	})

	return name, worktree, nil
}

// AutoSetupClaudePRsResult represents the result of auto-setup operation
type AutoSetupClaudePRsResult struct {
	TotalPRs       int
	ClaudePRs      int
	NewWorktrees   []string
	ExistingBranch []string
	Errors         []string
}

// AutoSetupClaudePRs fetches all PRs with "claude/*" prefix and auto-creates worktrees
func (m *Manager) AutoSetupClaudePRs(projectName string) (*AutoSetupClaudePRsResult, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	result := &AutoSetupClaudePRsResult{
		NewWorktrees:   make([]string, 0),
		ExistingBranch: make([]string, 0),
		Errors:         make([]string, 0),
	}

	// Ensure GitHub config is set
	if err := m.EnsureGitHubConfig(projectName); err != nil {
		return nil, fmt.Errorf("failed to detect GitHub repo: %w", err)
	}

	// Fetch all PRs
	allPRs, err := github.GetAllPRs(project.GitHubOwner, project.GitHubRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PRs: %w", err)
	}

	result.TotalPRs = len(allPRs)

	// Filter for claude/* PRs and only open ones
	for _, pr := range allPRs {
		// Skip if not a claude/* branch
		if !isClaudeBranch(pr.HeadBranch) {
			continue
		}

		// Skip if PR is not open (only process open PRs)
		if pr.State != "open" {
			continue
		}

		result.ClaudePRs++

		// Check if worktree for this branch already exists
		branchExists := false
		for _, wt := range project.Worktrees {
			if wt.Branch == pr.HeadBranch && !wt.Archived {
				branchExists = true
				result.ExistingBranch = append(result.ExistingBranch, pr.HeadBranch)
				break
			}
		}

		if branchExists {
			continue
		}

		// Create worktree for this PR
		name, worktree, err := m.PrepareWorktree(projectName, pr.HeadBranch, project.DefaultPortsPerWorktree)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to prepare worktree for %s: %v", pr.HeadBranch, err))
			continue
		}

		// Store auto-saves, no need for explicit config.Save

		// Queue worktree creation to avoid git lock conflicts when creating multiple worktrees
		GetWorktreeQueue().Enqueue(&WorktreeJob{
			ProjectName:  projectName,
			WorktreeName: name,
			Worktree:     worktree,
			Store:        m.store,
			Manager:      m,
			OnComplete:   nil, // We don't need individual callbacks for auto-setup
		})

		result.NewWorktrees = append(result.NewWorktrees, fmt.Sprintf("%s (%s)", name, pr.HeadBranch))
	}

	return result, nil
}

// isClaudeBranch checks if a branch name starts with "claude/"
func isClaudeBranch(branch string) bool {
	return strings.HasPrefix(branch, "claude/")
}

// GitStatusInfo holds git status information for a worktree
type GitStatusInfo struct {
	IsDirty       bool
	CommitsBehind int
}

// RecoverInterruptedStates checks all worktrees and fixes states that were interrupted
// (e.g., when TUI was closed during worktree creation or setup).
// Returns the number of worktrees that were recovered to failed state.
func (m *Manager) RecoverInterruptedStates() int {
	// If store is available, use its recovery method (handles locking and auto-save)
	if m.store != nil {
		return m.store.RecoverInterruptedWorktrees()
	}

	// Fallback for when store is not available
	recovered := 0

	for _, project := range m.config.Projects {
		for _, worktree := range project.Worktrees {
			// Skip archived worktrees
			if worktree.Archived {
				continue
			}

			// Check for interrupted states
			switch worktree.SetupStatus {
			case config.SetupStatusCreating:
				// Worktree was being created when TUI closed
				if !WorktreeExists(worktree.Path) {
					// Directory doesn't exist - creation was interrupted
					worktree.SetupStatus = config.SetupStatusFailed
					recovered++
				} else {
					// Directory exists - check if it's a valid git worktree
					gitWorktrees, err := GitWorktreeList(project.Path)
					if err != nil {
						// Can't verify, mark as failed to be safe
						worktree.SetupStatus = config.SetupStatusFailed
						recovered++
						continue
					}

					// Check if this path is in the git worktree list
					isValidWorktree := false
					for _, wtPath := range gitWorktrees {
						if wtPath == worktree.Path {
							isValidWorktree = true
							break
						}
					}

					if isValidWorktree {
						// Git worktree exists, but setup never ran - mark as failed so user can retry
						worktree.SetupStatus = config.SetupStatusFailed
						recovered++
					} else {
						// Directory exists but not a valid git worktree - mark as failed
						worktree.SetupStatus = config.SetupStatusFailed
						recovered++
					}
				}

			case config.SetupStatusRunning:
				// Setup was running when TUI closed - mark as failed so user can retry
				// The worktree exists but setup may be incomplete
				worktree.SetupStatus = config.SetupStatusFailed
				recovered++
			}
		}
	}

	return recovered
}

// FetchGitStatusForProject fetches git status for all worktrees in a project
func (m *Manager) FetchGitStatusForProject(projectName string) (map[string]*GitStatusInfo, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Get the default branch for this project
	defaultBranch := GitGetDefaultBranch(project.Path)

	result := make(map[string]*GitStatusInfo)

	for worktreeName, worktree := range project.Worktrees {
		// Skip archived, creating, or failed worktrees
		if worktree.Archived ||
			worktree.SetupStatus == config.SetupStatusCreating ||
			worktree.SetupStatus == config.SetupStatusRunning ||
			worktree.SetupStatus == config.SetupStatusFailed {
			continue
		}

		// Check if worktree path exists
		if !WorktreeExists(worktree.Path) {
			continue
		}

		status := &GitStatusInfo{}

		// Check for uncommitted changes
		isDirty, err := GitHasUncommittedChanges(worktree.Path)
		if err == nil {
			status.IsDirty = isDirty
		}

		// Check commits behind
		behind, err := GitCommitsBehind(worktree.Path, defaultBranch)
		if err == nil {
			status.CommitsBehind = behind
		}

		result[worktreeName] = status
	}

	return result, nil
}

// generateDBName generates a database name from project and port using a pattern
func generateDBName(projectName string, port int, pattern string) string {
	name := pattern
	name = strings.ReplaceAll(name, "{project}", projectName)
	name = strings.ReplaceAll(name, "{port}", fmt.Sprintf("%d", port))
	return sanitizeDBName(name)
}

// sanitizeDBName sanitizes a database name to be a valid PostgreSQL identifier
func sanitizeDBName(name string) string {
	// Replace invalid characters with underscores
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		} else {
			result.WriteRune('_')
		}
	}
	name = result.String()

	// Ensure it starts with a letter or underscore
	if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}

	// Truncate to 63 characters (PostgreSQL limit)
	if len(name) > 63 {
		name = name[:63]
	}

	// Lowercase
	return strings.ToLower(name)
}

// buildWorktreeDBURL creates a connection URL for a worktree database
func buildWorktreeDBURL(localURL, dbName string) string {
	// Parse the local URL to replace the database name
	// Simple approach: find the last slash and replace everything after it
	lastSlash := strings.LastIndex(localURL, "/")
	if lastSlash == -1 {
		return localURL + "/" + dbName
	}

	// Check for query parameters
	baseURL := localURL[:lastSlash+1]
	remainder := localURL[lastSlash+1:]

	// If there are query params, preserve them
	queryIdx := strings.Index(remainder, "?")
	if queryIdx != -1 {
		return baseURL + dbName + remainder[queryIdx:]
	}

	return baseURL + dbName
}

// dropWorktreeDB drops a worktree database
func dropWorktreeDB(localURL, dbName string) error {
	// Connect to postgres database to drop
	adminURL := buildWorktreeDBURL(localURL, "postgres")

	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Terminate existing connections
	_, _ = db.ExecContext(ctx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`, dbName)

	// Drop database
	_, err = db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q", dbName))
	if err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}

	return nil
}
