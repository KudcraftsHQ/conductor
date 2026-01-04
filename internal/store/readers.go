package store

import (
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// ============================================================================
// Project Readers
// ============================================================================

// GetProject returns a deep copy of a project by name
func (s *Store) GetProject(name string) (*config.Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[name]
	if !ok {
		return nil, false
	}
	return s.copyProject(project), true
}

// GetProjectPath returns the path for a project
func (s *Store) GetProjectPath(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[name]
	if !ok {
		return "", false
	}
	return project.Path, true
}

// ListProjects returns a list of all project names
func (s *Store) ListProjects() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.config.Projects))
	for name := range s.config.Projects {
		names = append(names, name)
	}
	return names
}

// GetAllProjects returns deep copies of all projects
func (s *Store) GetAllProjects() map[string]*config.Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*config.Project, len(s.config.Projects))
	for name, project := range s.config.Projects {
		result[name] = s.copyProject(project)
	}
	return result
}

// ProjectExists checks if a project exists
func (s *Store) ProjectExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.config.Projects[name]
	return ok
}

// ============================================================================
// Worktree Readers
// ============================================================================

// GetWorktree returns a deep copy of a worktree
func (s *Store) GetWorktree(projectName, worktreeName string) (*config.Worktree, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return nil, false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return nil, false
	}
	return s.copyWorktree(wt), true
}

// GetWorktreePath returns the path for a worktree
func (s *Store) GetWorktreePath(projectName, worktreeName string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return "", false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return "", false
	}
	return wt.Path, true
}

// GetWorktreeStatus returns the setup status for a worktree
func (s *Store) GetWorktreeStatus(projectName, worktreeName string) (config.SetupStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return "", false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return "", false
	}
	return wt.SetupStatus, true
}

// ListWorktrees returns a list of worktree names for a project
func (s *Store) ListWorktrees(projectName string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return nil
	}

	names := make([]string, 0, len(project.Worktrees))
	for name := range project.Worktrees {
		names = append(names, name)
	}
	return names
}

// GetAllWorktrees returns deep copies of all worktrees for a project
func (s *Store) GetAllWorktrees(projectName string) map[string]*config.Worktree {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return nil
	}

	result := make(map[string]*config.Worktree, len(project.Worktrees))
	for name, wt := range project.Worktrees {
		result[name] = s.copyWorktree(wt)
	}
	return result
}

// WorktreeExists checks if a worktree exists
func (s *Store) WorktreeExists(projectName, worktreeName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return false
	}
	_, ok = project.Worktrees[worktreeName]
	return ok
}

// ============================================================================
// Tunnel Readers
// ============================================================================

// GetTunnelState returns a deep copy of tunnel state for a worktree
func (s *Store) GetTunnelState(projectName, worktreeName string) (*config.TunnelState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return nil, false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return nil, false
	}
	if wt.Tunnel == nil {
		return nil, true
	}
	return s.copyTunnelState(wt.Tunnel), true
}

// IsTunnelActive checks if a tunnel is active for a worktree
func (s *Store) IsTunnelActive(projectName, worktreeName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return false
	}
	return wt.Tunnel != nil && wt.Tunnel.Active
}

// ============================================================================
// Port Readers
// ============================================================================

// GetWorktreePorts returns the ports allocated to a worktree
func (s *Store) GetWorktreePorts(projectName, worktreeName string) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return nil
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return nil
	}

	// Return a copy
	ports := make([]int, len(wt.Ports))
	copy(ports, wt.Ports)
	return ports
}

// GetAllPortAllocations returns a copy of all port allocations
func (s *Store) GetAllPortAllocations() map[string]*config.PortAlloc {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*config.PortAlloc, len(s.config.PortAllocations))
	for port, alloc := range s.config.PortAllocations {
		result[port] = &config.PortAlloc{
			Project:  alloc.Project,
			Worktree: alloc.Worktree,
			Index:    alloc.Index,
		}
	}
	return result
}

// IsPortAvailable checks if a port is available
func (s *Store) IsPortAvailable(port int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.IsPortAvailable(port)
}

// ============================================================================
// Defaults Readers
// ============================================================================

// GetDefaults returns a copy of the default settings
func (s *Store) GetDefaults() config.Defaults {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Defaults
}

// GetDefaultPortsPerWorktree returns the default ports per worktree
func (s *Store) GetDefaultPortsPerWorktree() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Defaults.PortsPerWorktree
}

// ============================================================================
// Update Settings Readers
// ============================================================================

// GetUpdateSettings returns a copy of update settings
func (s *Store) GetUpdateSettings() config.UpdateSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Updates
}

// ============================================================================
// PRs Readers
// ============================================================================

// GetWorktreePRs returns a copy of PRs for a worktree
func (s *Store) GetWorktreePRs(projectName, worktreeName string) []config.PRInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return nil
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return nil
	}

	// Return a copy
	prs := make([]config.PRInfo, len(wt.PRs))
	copy(prs, wt.PRs)
	return prs
}

// ============================================================================
// GitHub Config Readers
// ============================================================================

// GetGitHubConfig returns the GitHub owner and repo for a project
func (s *Store) GetGitHubConfig(projectName string) (owner, repo string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return "", "", false
	}
	return project.GitHubOwner, project.GitHubRepo, true
}

// ============================================================================
// Deep Copy Helpers
// ============================================================================

func (s *Store) copyProject(p *config.Project) *config.Project {
	if p == nil {
		return nil
	}
	cp := &config.Project{
		Path:                    p.Path,
		AddedAt:                 p.AddedAt,
		DefaultPortsPerWorktree: p.DefaultPortsPerWorktree,
		GitHubOwner:             p.GitHubOwner,
		GitHubRepo:              p.GitHubRepo,
		Worktrees:               make(map[string]*config.Worktree, len(p.Worktrees)),
	}
	for name, wt := range p.Worktrees {
		cp.Worktrees[name] = s.copyWorktree(wt)
	}
	return cp
}

func (s *Store) copyWorktree(wt *config.Worktree) *config.Worktree {
	if wt == nil {
		return nil
	}
	cp := &config.Worktree{
		Path:          wt.Path,
		Branch:        wt.Branch,
		IsRoot:        wt.IsRoot,
		CreatedAt:     wt.CreatedAt,
		Archived:      wt.Archived,
		ArchivedAt:    wt.ArchivedAt,
		SetupStatus:   wt.SetupStatus,
		ArchiveStatus: wt.ArchiveStatus,
		Tunnel:        s.copyTunnelState(wt.Tunnel),
	}

	// Copy ports
	if wt.Ports != nil {
		cp.Ports = make([]int, len(wt.Ports))
		copy(cp.Ports, wt.Ports)
	}

	// Copy PRs
	if wt.PRs != nil {
		cp.PRs = make([]config.PRInfo, len(wt.PRs))
		copy(cp.PRs, wt.PRs)
	}

	return cp
}

func (s *Store) copyTunnelState(t *config.TunnelState) *config.TunnelState {
	if t == nil {
		return nil
	}
	return &config.TunnelState{
		Active:    t.Active,
		Mode:      t.Mode,
		URL:       t.URL,
		Port:      t.Port,
		PID:       t.PID,
		StartedAt: t.StartedAt,
	}
}

// ============================================================================
// Full Config Access (for migrations/special cases)
// ============================================================================

// GetConfigSnapshot returns a deep copy of the entire config.
// Use sparingly - prefer specific getters for better encapsulation.
func (s *Store) GetConfigSnapshot() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := &config.Config{
		Version:         s.config.Version,
		Defaults:        s.config.Defaults,
		Updates:         s.config.Updates,
		PortAllocations: make(map[string]*config.PortAlloc, len(s.config.PortAllocations)),
		Projects:        make(map[string]*config.Project, len(s.config.Projects)),
	}

	// Copy port allocations
	for port, alloc := range s.config.PortAllocations {
		cfg.PortAllocations[port] = &config.PortAlloc{
			Project:  alloc.Project,
			Worktree: alloc.Worktree,
			Index:    alloc.Index,
		}
	}

	// Copy projects
	for name, project := range s.config.Projects {
		cfg.Projects[name] = s.copyProject(project)
	}

	// Copy tunnel defaults
	cfg.Defaults.Tunnel = config.TunnelDefaults{
		Domain:          s.config.Defaults.Tunnel.Domain,
		CloudflareToken: s.config.Defaults.Tunnel.CloudflareToken,
		AccountID:       s.config.Defaults.Tunnel.AccountID,
		ZoneID:          s.config.Defaults.Tunnel.ZoneID,
	}

	return cfg
}

// GetAllPortInfo returns port info for display (delegating to config method)
func (s *Store) GetAllPortInfo() []config.PortInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.GetAllPortInfo()
}

// TotalUsedPorts returns the total number of used ports
func (s *Store) TotalUsedPorts() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.TotalUsedPorts()
}

// GetPortRange returns the port range settings
func (s *Store) GetPortRange() (start, end int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Defaults.PortRangeStart, s.config.Defaults.PortRangeEnd
}

// GetProjectDefaultPorts returns the default ports for a project
func (s *Store) GetProjectDefaultPorts(projectName string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return s.config.Defaults.PortsPerWorktree
	}
	if project.DefaultPortsPerWorktree > 0 {
		return project.DefaultPortsPerWorktree
	}
	return s.config.Defaults.PortsPerWorktree
}

// GetTunnelDomain returns the tunnel domain for a project (or global default)
func (s *Store) GetTunnelDomain() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Defaults.Tunnel.Domain
}

// GetOpenWith returns the default open-with setting
func (s *Store) GetOpenWith() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Defaults.OpenWith
}

// GetIDECommand returns the default IDE command
func (s *Store) GetIDECommand() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Defaults.IDECommand
}

// IsWorktreeArchived checks if a worktree is archived
func (s *Store) IsWorktreeArchived(projectName, worktreeName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return false
	}
	return wt.Archived
}

// IsWorktreeRoot checks if a worktree is the root worktree
func (s *Store) IsWorktreeRoot(projectName, worktreeName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return false
	}
	return wt.IsRoot
}

// GetWorktreeBranch returns the branch for a worktree
func (s *Store) GetWorktreeBranch(projectName, worktreeName string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return "", false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return "", false
	}
	return wt.Branch, true
}

// GetWorktreeCreatedAt returns when a worktree was created
func (s *Store) GetWorktreeCreatedAt(projectName, worktreeName string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.config.Projects[projectName]
	if !ok {
		return time.Time{}, false
	}
	wt, ok := project.Worktrees[worktreeName]
	if !ok {
		return time.Time{}, false
	}
	return wt.CreatedAt, true
}
