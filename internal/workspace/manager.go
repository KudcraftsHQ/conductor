package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/github"
	"github.com/hammashamzah/conductor/internal/tmux"
)

// Manager handles worktree operations
type Manager struct {
	config *config.Config
}

// NewManager creates a new workspace manager
func NewManager(cfg *config.Config) *Manager {
	return &Manager{config: cfg}
}

// CreateWorktree creates a new git worktree synchronously (for CLI use)
func (m *Manager) CreateWorktree(projectName, branch string, portCount int) (string, *config.Worktree, error) {
	name, worktree, err := m.PrepareWorktree(projectName, branch, portCount)
	if err != nil {
		return "", nil, err
	}

	project, _ := m.config.GetProject(projectName)

	// Create worktree directory
	if err := os.MkdirAll(filepath.Dir(worktree.Path), 0755); err != nil {
		m.config.FreeWorktreePorts(projectName, name)
		delete(project.Worktrees, name)
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
		m.config.FreeWorktreePorts(projectName, name)
		delete(project.Worktrees, name)
		return "", nil, fmt.Errorf("failed to create git worktree: %w", gitErr)
	}

	worktree.SetupStatus = config.SetupStatusDone
	return name, worktree, nil
}

// PrepareWorktree allocates ports and creates the worktree entry optimistically
// Returns the worktree name and entry, but does NOT create the git worktree yet
// Call CreateWorktreeAsync to actually create the git worktree in background
func (m *Manager) PrepareWorktree(projectName, branch string, portCount int) (string, *config.Worktree, error) {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return "", nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Get existing worktree names
	existingNames := make([]string, 0, len(project.Worktrees))
	for name := range project.Worktrees {
		existingNames = append(existingNames, name)
	}

	// Generate unique city name
	name := RandomCityExcluding(existingNames)

	// Check if name already exists
	if _, exists := project.Worktrees[name]; exists {
		return "", nil, fmt.Errorf("worktree '%s' already exists", name)
	}

	// Use project default if port count not specified
	if portCount <= 0 {
		portCount = project.DefaultPortsPerWorktree
	}

	// Allocate ports
	ports, err := m.config.AllocatePorts(projectName, name, portCount)
	if err != nil {
		return "", nil, fmt.Errorf("failed to allocate ports: %w", err)
	}

	// Get worktree path in ~/.conductor/<project>/<worktree>
	worktreePath, err := config.WorktreePath(projectName, name)
	if err != nil {
		m.config.FreePorts(ports)
		return "", nil, fmt.Errorf("failed to get worktree path: %w", err)
	}

	// Determine branch name - use worktree name if not specified
	if branch == "" {
		branch = name
	}

	// Create worktree entry with "creating" status
	worktree := config.NewWorktree(worktreePath, branch, false, ports)
	worktree.SetupStatus = config.SetupStatusCreating
	project.Worktrees[name] = worktree

	return name, worktree, nil
}

// CreateWorktreeAsync creates the git worktree in background
// Call this after PrepareWorktree and saving the config
func (m *Manager) CreateWorktreeAsync(projectName, worktreeName string, onComplete func(success bool, err error)) error {
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
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
	project, ok := m.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, exists := project.Worktrees[worktreeName]
	if !exists {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	GetSetupManager().RunSetupAsync(project, projectName, worktreeName, worktree, onComplete)
	return nil
}

// ArchiveWorktree marks a worktree as archived, removes git worktree and frees ports
// Runs archive script first (if exists), then removes worktree regardless of script result
// The worktree entry remains in config so logs can still be viewed
func (m *Manager) ArchiveWorktree(projectName, worktreeName string) error {
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

	// Kill tmux window if it exists
	_ = tmux.KillWindow(projectName, worktree.Branch)

	// Remove git worktree
	if err := GitWorktreeRemove(project.Path, worktree.Path); err != nil {
		// Try to remove directory manually
		_ = os.RemoveAll(worktree.Path)
	}

	// Delete the branch (ignore error - branch may not exist)
	_ = GitBranchDelete(project.Path, worktree.Branch)

	// Free ports
	m.config.FreeWorktreePorts(projectName, worktreeName)

	// Mark as archived (keep in config for log viewing)
	worktree.Archived = true
	worktree.ArchivedAt = time.Now()
	worktree.Ports = nil // Clear ports since they're freed

	return nil
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

	// Remove from config
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
			m.config.FreeWorktreePorts(projectName, name)
			delete(project.Worktrees, name)
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
		project.GitHubOwner = owner
		project.GitHubRepo = repo
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
	worktree.PRs = prs

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
		project.Worktrees[worktreeName].PRs = prs
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

