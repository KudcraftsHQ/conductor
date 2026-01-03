package tunnel

import (
	"context"
	"fmt"
	"sync"

	"github.com/hammashamzah/conductor/internal/config"
)

// Manager handles tunnel operations for all worktrees
type Manager struct {
	config        *config.Config
	mu            sync.RWMutex
	activeTunnels map[string]*QuickTunnel        // key: "project/worktree"
	namedManagers map[string]*NamedTunnelManager // key: projectName
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewManager creates a new tunnel manager
func NewManager(cfg *config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		config:        cfg,
		activeTunnels: make(map[string]*QuickTunnel),
		namedManagers: make(map[string]*NamedTunnelManager),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// tunnelKey creates a unique key for a project/worktree combination
func tunnelKey(projectName, worktreeName string) string {
	return projectName + "/" + worktreeName
}

// StartQuickTunnel starts a quick tunnel for a worktree port
func (m *Manager) StartQuickTunnel(projectName, worktreeName string, port int) (*config.TunnelState, error) {
	key := tunnelKey(projectName, worktreeName)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already running
	if existing, ok := m.activeTunnels[key]; ok {
		if IsProcessRunning(existing.PID) {
			return existing.ToTunnelState(), nil
		}
		// Process died, clean up
		delete(m.activeTunnels, key)
	}

	// Start new tunnel
	tunnel, err := StartQuickTunnel(m.ctx, projectName, worktreeName, port)
	if err != nil {
		return nil, err
	}

	m.activeTunnels[key] = tunnel
	return tunnel.ToTunnelState(), nil
}

// StartNamedTunnel starts a named tunnel for a worktree port
func (m *Manager) StartNamedTunnel(projectName, worktreeName string, port int, projectConfig *config.ProjectConfig) (*config.TunnelState, error) {
	// Get domain from config
	domain := GetDomainForProject(m.config, projectConfig)
	if domain == "" {
		return nil, fmt.Errorf("no domain configured. Set tunnel.domain in conductor.json or global config")
	}

	// Get API token
	apiToken := GetAPIToken(m.config.Defaults.Tunnel.CloudflareToken)
	if apiToken == "" {
		return nil, fmt.Errorf("no Cloudflare API token. Set CLOUDFLARE_API_TOKEN env var or tunnel.cloudflareToken in config")
	}

	// Get account and zone IDs
	accountID := m.config.Defaults.Tunnel.AccountID
	zoneID := m.config.Defaults.Tunnel.ZoneID
	if accountID == "" || zoneID == "" {
		return nil, fmt.Errorf("cloudflare account ID and zone ID required: set tunnel.accountId and tunnel.zoneId in config")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Get or create named tunnel manager for this project
	namedMgr, exists := m.namedManagers[projectName]
	if !exists {
		// Get tunnel ID from project config
		tunnelID := ""
		tunnelName := ""
		if projectConfig != nil && projectConfig.Tunnel != nil {
			tunnelID = projectConfig.Tunnel.TunnelID
			tunnelName = projectConfig.Tunnel.TunnelName
		}

		// Create Cloudflare client
		cfClient := NewCloudflareClient(accountID, zoneID, apiToken)

		var err error
		namedMgr, err = NewNamedTunnelManager(projectName, tunnelID, tunnelName, domain, cfClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create named tunnel manager: %w", err)
		}

		m.namedManagers[projectName] = namedMgr
	}

	// Ensure tunnel exists
	if err := namedMgr.EnsureTunnel(); err != nil {
		return nil, fmt.Errorf("failed to ensure tunnel: %w", err)
	}

	// Add route for this worktree
	url, err := namedMgr.AddRoute(worktreeName, port)
	if err != nil {
		return nil, fmt.Errorf("failed to add route: %w", err)
	}

	// Start tunnel if not running
	if !namedMgr.IsRunning() {
		if err := namedMgr.StartTunnel(m.ctx); err != nil {
			return nil, fmt.Errorf("failed to start tunnel: %w", err)
		}
	}

	return &config.TunnelState{
		Active: true,
		Mode:   config.TunnelModeNamed,
		URL:    url,
		Port:   port,
	}, nil
}

// StopNamedTunnel stops a named tunnel route for a worktree
func (m *Manager) StopNamedTunnel(projectName, worktreeName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	namedMgr, exists := m.namedManagers[projectName]
	if !exists {
		return nil
	}

	return namedMgr.RemoveRoute(worktreeName)
}

// StopTunnel stops a running tunnel for a worktree
func (m *Manager) StopTunnel(projectName, worktreeName string) error {
	key := tunnelKey(projectName, worktreeName)

	m.mu.Lock()
	defer m.mu.Unlock()

	tunnel, ok := m.activeTunnels[key]
	if !ok {
		// Try to find by PID file
		pf, err := ReadPIDFile(projectName, worktreeName)
		if err != nil || pf == nil {
			return nil // No tunnel running
		}

		// Kill the process
		if IsProcessRunning(pf.PID) {
			if err := KillProcess(pf.PID); err != nil {
				return err
			}
		}
		return DeletePIDFile(projectName, worktreeName)
	}

	delete(m.activeTunnels, key)
	return tunnel.Stop()
}

// GetStatus returns the current tunnel status for a worktree
func (m *Manager) GetStatus(projectName, worktreeName string) *config.TunnelState {
	key := tunnelKey(projectName, worktreeName)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if tunnel, ok := m.activeTunnels[key]; ok {
		if IsProcessRunning(tunnel.PID) {
			return tunnel.ToTunnelState()
		}
	}

	return nil
}

// IsRunning checks if a tunnel is running for a worktree
func (m *Manager) IsRunning(projectName, worktreeName string) bool {
	key := tunnelKey(projectName, worktreeName)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if tunnel, ok := m.activeTunnels[key]; ok {
		return IsProcessRunning(tunnel.PID)
	}

	// Check PID file
	pf, _ := ReadPIDFile(projectName, worktreeName)
	if pf != nil {
		return IsProcessRunning(pf.PID)
	}

	return false
}

// GetURL returns the tunnel URL for a worktree, or empty string if not running
func (m *Manager) GetURL(projectName, worktreeName string) string {
	key := tunnelKey(projectName, worktreeName)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if tunnel, ok := m.activeTunnels[key]; ok {
		return tunnel.URL
	}

	// Check PID file
	pf, _ := ReadPIDFile(projectName, worktreeName)
	if pf != nil && IsProcessRunning(pf.PID) {
		return pf.URL
	}

	return ""
}

// RestoreTunnels restores tunnel state from PID files on TUI restart
// Returns a map of project/worktree -> TunnelState for tunnels that are still running
func (m *Manager) RestoreTunnels() (map[string]*config.TunnelState, error) {
	pidFiles, err := ListPIDFiles()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]*config.TunnelState)

	for _, pf := range pidFiles {
		if !IsProcessRunning(pf.PID) {
			// Process died, clean up
			_ = DeletePIDFile(pf.ProjectName, pf.WorktreeName)
			continue
		}

		key := tunnelKey(pf.ProjectName, pf.WorktreeName)

		// Create a minimal QuickTunnel entry (we don't have the cmd/cancel since we didn't start it)
		tunnel := &QuickTunnel{
			ProjectName:  pf.ProjectName,
			WorktreeName: pf.WorktreeName,
			Port:         pf.Port,
			URL:          pf.URL,
			PID:          pf.PID,
			LogBuffer:    NewLogBuffer(100),
			StartedAt:    pf.StartedAt,
		}

		m.activeTunnels[key] = tunnel

		result[key] = &config.TunnelState{
			Active:    true,
			Mode:      pf.Mode,
			URL:       pf.URL,
			Port:      pf.Port,
			PID:       pf.PID,
			StartedAt: pf.StartedAt,
		}
	}

	return result, nil
}

// StopAll stops all running tunnels
func (m *Manager) StopAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for key, tunnel := range m.activeTunnels {
		if err := tunnel.Stop(); err != nil {
			lastErr = err
		}
		delete(m.activeTunnels, key)
	}

	return lastErr
}

// Close shuts down the manager and all tunnels
func (m *Manager) Close() error {
	m.cancel()
	return m.StopAll()
}

// GetLogs returns the log buffer for a running tunnel
func (m *Manager) GetLogs(projectName, worktreeName string) []string {
	key := tunnelKey(projectName, worktreeName)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if tunnel, ok := m.activeTunnels[key]; ok {
		return tunnel.LogBuffer.Lines()
	}

	return nil
}

// GetDomainForProject returns the domain to use for a project's named tunnels
// It checks project config first, then falls back to global defaults
func GetDomainForProject(cfg *config.Config, projectConfig *config.ProjectConfig) string {
	// Check project-level config first
	if projectConfig != nil && projectConfig.Tunnel != nil && projectConfig.Tunnel.Domain != "" {
		return projectConfig.Tunnel.Domain
	}

	// Fall back to global defaults
	return cfg.Defaults.Tunnel.Domain
}

// GenerateTunnelHostname creates the hostname for a named tunnel
// Format: <worktree>-<port>.<domain>
func GenerateTunnelHostname(worktreeName string, port int, domain string) string {
	if domain == "" {
		return ""
	}
	return fmt.Sprintf("%s-%d.%s", worktreeName, port, domain)
}
