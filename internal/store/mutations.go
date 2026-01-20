package store

import (
	"fmt"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// ============================================================================
// Project Mutations
// ============================================================================

// AddProject adds a new project to the store
func (s *Store) AddProject(name string, project *config.Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.config.Projects[name]; exists {
		return fmt.Errorf("project '%s' already exists", name)
	}

	s.config.Projects[name] = project
	s.markDirty()
	return nil
}

// RemoveProject removes a project from the store
func (s *Store) RemoveProject(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.config.Projects[name]; !exists {
		return fmt.Errorf("project '%s' not found", name)
	}

	delete(s.config.Projects, name)
	s.markDirty()
	return nil
}

// SetGitHubConfig sets the GitHub owner and repo for a project
func (s *Store) SetGitHubConfig(projectName, owner, repo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	project.GitHubOwner = owner
	project.GitHubRepo = repo
	s.markDirty()
	return nil
}

// ============================================================================
// Worktree Mutations
// ============================================================================

// AddWorktree adds a new worktree to a project
func (s *Store) AddWorktree(projectName, worktreeName string, worktree *config.Worktree) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	if _, exists := project.Worktrees[worktreeName]; exists {
		return fmt.Errorf("worktree '%s' already exists", worktreeName)
	}

	project.Worktrees[worktreeName] = worktree
	s.markDirty()
	return nil
}

// RemoveWorktree removes a worktree from a project
func (s *Store) RemoveWorktree(projectName, worktreeName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	if _, exists := project.Worktrees[worktreeName]; !exists {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	delete(project.Worktrees, worktreeName)
	s.markDirty()
	return nil
}

// SetWorktreeStatus sets the setup status for a worktree
func (s *Store) SetWorktreeStatus(projectName, worktreeName string, status config.SetupStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	wt.SetupStatus = status
	s.markDirty()
	return nil
}

// SetWorktreeArchiveStatus sets the archive status for a worktree
func (s *Store) SetWorktreeArchiveStatus(projectName, worktreeName string, status config.ArchiveStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	wt.ArchiveStatus = status
	s.markDirty()
	return nil
}

// ArchiveWorktree marks a worktree as archived
func (s *Store) ArchiveWorktree(projectName, worktreeName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	wt.Archived = true
	wt.ArchivedAt = time.Now()
	wt.ArchiveStatus = config.ArchiveStatusNone
	s.markDirty()
	return nil
}

// SetWorktreePorts sets the ports for a worktree
func (s *Store) SetWorktreePorts(projectName, worktreeName string, ports []int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	wt.Ports = ports
	s.markDirty()
	return nil
}

// ClearWorktreePorts clears the ports for a worktree (used when archiving)
func (s *Store) ClearWorktreePorts(projectName, worktreeName string) error {
	return s.SetWorktreePorts(projectName, worktreeName, nil)
}

// SetWorktreePRs sets the PRs for a worktree
func (s *Store) SetWorktreePRs(projectName, worktreeName string, prs []config.PRInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	wt.PRs = prs
	s.markDirty()
	return nil
}

// ============================================================================
// Tunnel Mutations
// ============================================================================

// SetTunnelState sets the tunnel state for a worktree
func (s *Store) SetTunnelState(projectName, worktreeName string, state *config.TunnelState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	wt.Tunnel = state
	s.markDirty()
	return nil
}

// ClearTunnelState clears the tunnel state for a worktree
func (s *Store) ClearTunnelState(projectName, worktreeName string) error {
	return s.SetTunnelState(projectName, worktreeName, nil)
}

// ============================================================================
// Port Allocation Mutations
// ============================================================================

// AllocatePorts allocates ports for a worktree
// Returns the allocated ports
func (s *Store) AllocatePorts(projectName, worktreeName string, count int) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ports, err := s.config.AllocatePorts(projectName, worktreeName, count)
	if err != nil {
		return nil, err
	}

	s.markDirty()
	return ports, nil
}

// FreePorts frees the specified ports
func (s *Store) FreePorts(ports []int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.FreePorts(ports)
	s.markDirty()
}

// FreeWorktreePorts frees all ports for a worktree
func (s *Store) FreeWorktreePorts(projectName, worktreeName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.FreeWorktreePorts(projectName, worktreeName)
	s.markDirty()
}

// SetPortAllocation sets a specific port allocation
func (s *Store) SetPortAllocation(port int, alloc *config.PortAlloc) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.PortAllocations[fmt.Sprintf("%d", port)] = alloc
	s.markDirty()
}

// RemovePortAllocation removes a specific port allocation
func (s *Store) RemovePortAllocation(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.config.PortAllocations, fmt.Sprintf("%d", port))
	s.markDirty()
}

// ============================================================================
// Update Settings Mutations
// ============================================================================

// SetLastUpdateCheck sets the last update check time
func (s *Store) SetLastUpdateCheck(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.Updates.LastCheck = t
	s.markDirty()
}

// SetLastVersion sets the last known version
func (s *Store) SetLastVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.Updates.LastVersion = version
	s.markDirty()
}

// SetUpdateInfo sets both last check time and version
func (s *Store) SetUpdateInfo(lastCheck time.Time, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.Updates.LastCheck = lastCheck
	s.config.Updates.LastVersion = version
	s.markDirty()
}

// ============================================================================
// Batch Mutations (for complex operations that need atomicity)
// ============================================================================

// BatchMutate allows multiple mutations in a single locked operation.
// The function receives a mutable config reference.
// All changes are automatically marked dirty and saved.
// WARNING: Use specific mutation methods when possible for better tracking.
func (s *Store) BatchMutate(fn func(cfg *config.Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := fn(s.config); err != nil {
		return err
	}

	s.markDirty()
	return nil
}

// ============================================================================
// Recovery Mutations (for startup recovery)
// ============================================================================

// RecoverInterruptedWorktrees marks all creating/running worktrees as failed
// Returns the number of worktrees recovered
func (s *Store) RecoverInterruptedWorktrees() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	recovered := 0

	for _, project := range s.config.Projects {
		for _, wt := range project.Worktrees {
			if wt.Archived {
				continue
			}

			switch wt.SetupStatus {
			case config.SetupStatusCreating, config.SetupStatusRunning:
				wt.SetupStatus = config.SetupStatusFailed
				recovered++
			}
		}
	}

	if recovered > 0 {
		s.markDirty()
	}

	return recovered
}

// CleanupStaleTunnels clears tunnel state for tunnels that are no longer running
// activeTunnels is a map of "project/worktree" -> true for active tunnels
// Returns the number of stale tunnels cleaned
func (s *Store) CleanupStaleTunnels(activeTunnels map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cleaned := 0

	for projectName, project := range s.config.Projects {
		for worktreeName, wt := range project.Worktrees {
			key := projectName + "/" + worktreeName
			if wt.Tunnel != nil && wt.Tunnel.Active && !activeTunnels[key] {
				wt.Tunnel = nil
				cleaned++
			}
		}
	}

	if cleaned > 0 {
		s.markDirty()
	}

	return cleaned
}

// RestoreTunnelStates updates tunnel states for active tunnels
// tunnelStates is a map of "project/worktree" -> TunnelState
func (s *Store) RestoreTunnelStates(tunnelStates map[string]*config.TunnelState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for projectName, project := range s.config.Projects {
		for worktreeName, wt := range project.Worktrees {
			key := projectName + "/" + worktreeName
			if state, ok := tunnelStates[key]; ok {
				wt.Tunnel = state
			}
		}
	}

	if len(tunnelStates) > 0 {
		s.markDirty()
	}
}


// SetDatabaseConfig sets the database configuration for a project
func (s *Store) SetDatabaseConfig(projectName string, dbConfig *config.DatabaseConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project %q not found", projectName)
	}

	project.Database = dbConfig
	s.markDirty()
	return nil
}

// SetLocalPostgresURL sets the local PostgreSQL URL in defaults
func (s *Store) SetLocalPostgresURL(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.Defaults.LocalPostgresURL = url
	s.markDirty()
	return nil
}

// ClearDatabaseConfig removes database configuration from a project
func (s *Store) ClearDatabaseConfig(projectName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project %q not found", projectName)
	}

	project.Database = nil
	s.markDirty()
	return nil
}

// SetWorktreeDatabase sets the database info for a worktree
func (s *Store) SetWorktreeDatabase(projectName, worktreeName, dbName, dbURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return fmt.Errorf("project %q not found", projectName)
	}

	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return fmt.Errorf("worktree %q not found", worktreeName)
	}

	wt.DatabaseName = dbName
	wt.DatabaseURL = dbURL
	s.markDirty()
	return nil
}

