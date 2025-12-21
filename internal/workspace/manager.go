package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hammashamzah/conductor/internal/config"
)

// Manager handles worktree operations
type Manager struct {
	config *config.Config
}

// NewManager creates a new workspace manager
func NewManager(cfg *config.Config) *Manager {
	return &Manager{config: cfg}
}

// CreateWorktree creates a new git worktree for a project
func (m *Manager) CreateWorktree(projectName, branch string, portCount int) (string, *config.Worktree, error) {
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

	// Create worktree directory
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0755); err != nil {
		m.config.FreePorts(ports)
		return "", nil, fmt.Errorf("failed to create worktrees directory: %w", err)
	}

	// Determine branch name - use worktree name if not specified
	if branch == "" {
		branch = name
	}

	// Create git worktree
	if GitBranchExists(project.Path, branch) {
		err = GitWorktreeAddExisting(project.Path, worktreePath, branch)
	} else {
		err = GitWorktreeAdd(project.Path, worktreePath, branch)
	}

	if err != nil {
		m.config.FreePorts(ports)
		return "", nil, fmt.Errorf("failed to create git worktree: %w", err)
	}

	// Create worktree entry
	worktree := config.NewWorktree(worktreePath, branch, false, ports)
	project.Worktrees[name] = worktree

	return name, worktree, nil
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

	GetSetupManager().RunSetupAsync(project.Path, worktree.Path, projectName, worktreeName, worktree, onComplete)
	return nil
}

// ArchiveWorktree removes a worktree and frees its ports
// Runs archive script first (if exists), then removes worktree regardless of script result
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

	// Run archive script first (logs are saved to file for debugging)
	// We ignore the error - archiving proceeds regardless
	GetSetupManager().RunArchiveScript(project.Path, worktree.Path, projectName, worktreeName, worktree)

	// Remove git worktree
	if err := GitWorktreeRemove(project.Path, worktree.Path); err != nil {
		// Try to remove directory manually
		os.RemoveAll(worktree.Path)
	}

	// Delete the branch
	GitBranchDelete(project.Path, worktree.Branch)

	// Free ports
	m.config.FreeWorktreePorts(projectName, worktreeName)

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

