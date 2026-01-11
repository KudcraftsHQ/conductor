// Package testing provides test utilities for the TUI
package testing

import (
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// MockStore implements a minimal store interface for testing
type MockStore struct {
	mu           sync.RWMutex
	config       *config.Config
	statusCalls  []StatusCall
	tunnelStates map[string]*config.TunnelState
}

// StatusCall records a status update call
type StatusCall struct {
	ProjectName  string
	WorktreeName string
	Status       config.SetupStatus
}

// NewMockStore creates a new mock store with the given config
func NewMockStore(cfg *config.Config) *MockStore {
	if cfg == nil {
		cfg = &config.Config{
			Projects:        make(map[string]*config.Project),
			PortAllocations: make(map[string]*config.PortAlloc),
		}
	}
	return &MockStore{
		config:       cfg,
		statusCalls:  make([]StatusCall, 0),
		tunnelStates: make(map[string]*config.TunnelState),
	}
}

// GetProject returns a project by name
func (m *MockStore) GetProject(name string) (*config.Project, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.config.Projects[name]
	return p, ok
}

// GetWorktree returns a worktree by project and name
func (m *MockStore) GetWorktree(projectName, worktreeName string) (*config.Worktree, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.config.Projects[projectName]
	if !ok {
		return nil, false
	}
	wt, ok := p.Worktrees[worktreeName]
	return wt, ok
}

// GetConfigSnapshot returns a copy of the config
func (m *MockStore) GetConfigSnapshot() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// IsWorktreeArchived checks if a worktree is archived
func (m *MockStore) IsWorktreeArchived(projectName, worktreeName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.config.Projects[projectName]
	if !ok {
		return false
	}
	wt, ok := p.Worktrees[worktreeName]
	if !ok {
		return false
	}
	return wt.Archived
}

// SetWorktreeStatus updates worktree status
func (m *MockStore) SetWorktreeStatus(projectName, worktreeName string, status config.SetupStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusCalls = append(m.statusCalls, StatusCall{projectName, worktreeName, status})

	if p, ok := m.config.Projects[projectName]; ok {
		if wt, ok := p.Worktrees[worktreeName]; ok {
			wt.SetupStatus = status
		}
	}
	return nil
}

// SetWorktreeArchiveStatus updates worktree archive status
func (m *MockStore) SetWorktreeArchiveStatus(projectName, worktreeName string, status config.ArchiveStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.config.Projects[projectName]; ok {
		if wt, ok := p.Worktrees[worktreeName]; ok {
			wt.ArchiveStatus = status
		}
	}
	return nil
}

// SetTunnelState sets the tunnel state for a worktree
func (m *MockStore) SetTunnelState(projectName, worktreeName string, state *config.TunnelState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := projectName + ":" + worktreeName
	m.tunnelStates[key] = state

	if p, ok := m.config.Projects[projectName]; ok {
		if wt, ok := p.Worktrees[worktreeName]; ok {
			wt.Tunnel = state
		}
	}
	return nil
}

// ClearTunnelState clears the tunnel state for a worktree
func (m *MockStore) ClearTunnelState(projectName, worktreeName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := projectName + ":" + worktreeName
	delete(m.tunnelStates, key)

	if p, ok := m.config.Projects[projectName]; ok {
		if wt, ok := p.Worktrees[worktreeName]; ok {
			wt.Tunnel = nil
		}
	}
	return nil
}

// RestoreTunnelStates restores tunnel states (no-op for mock)
func (m *MockStore) RestoreTunnelStates(states map[string]*config.TunnelState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range states {
		m.tunnelStates[k] = v
	}
}

// CleanupStaleTunnels cleans up stale tunnels (no-op for mock)
func (m *MockStore) CleanupStaleTunnels(activeTunnels map[string]bool) {
	// No-op for mock
}

// RemoveProject removes a project
func (m *MockStore) RemoveProject(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.config.Projects, name)
	return nil
}

// SetLastVersion sets the last version (no-op for mock)
func (m *MockStore) SetLastVersion(version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Updates.LastVersion = version
}

// Close closes the store (no-op for mock)
func (m *MockStore) Close() (bool, error) {
	return false, nil
}

// GetStatusCalls returns all recorded status calls
func (m *MockStore) GetStatusCalls() []StatusCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]StatusCall{}, m.statusCalls...)
}

// AddProject adds a project to the mock store
func (m *MockStore) AddProject(name string, project *config.Project) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Projects[name] = project
}

// AddWorktree adds a worktree to a project
func (m *MockStore) AddWorktree(projectName, worktreeName string, worktree *config.Worktree) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.config.Projects[projectName]; ok {
		if p.Worktrees == nil {
			p.Worktrees = make(map[string]*config.Worktree)
		}
		p.Worktrees[worktreeName] = worktree
	}
}

// CreateTestConfig creates a minimal test config
func CreateTestConfig() *config.Config {
	return &config.Config{
		Projects:        make(map[string]*config.Project),
		PortAllocations: make(map[string]*config.PortAlloc),
		Updates: config.UpdateSettings{
			AutoCheck:   false,
			NotifyInTUI: false,
		},
	}
}

// CreateTestProject creates a test project with optional worktrees
func CreateTestProject(path string, worktrees map[string]*config.Worktree) *config.Project {
	if worktrees == nil {
		worktrees = make(map[string]*config.Worktree)
	}
	return &config.Project{
		Path:                    path,
		Worktrees:               worktrees,
		DefaultPortsPerWorktree: 2,
		AddedAt:                 time.Now(),
	}
}

// CreateTestWorktree creates a test worktree
func CreateTestWorktree(branch, path string, ports []int, status config.SetupStatus) *config.Worktree {
	return &config.Worktree{
		Branch:      branch,
		Path:        path,
		Ports:       ports,
		SetupStatus: status,
		CreatedAt:   time.Now(),
	}
}
