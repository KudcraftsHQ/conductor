package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// NamedTunnelManager handles named tunnels with shared tunnel per project
type NamedTunnelManager struct {
	mu           sync.RWMutex
	projectName  string
	tunnelID     string
	tunnelName   string
	domain       string
	configPath   string
	credsPath    string
	process      *NamedTunnelProcess
	cfClient     *CloudflareClient
	activeRoutes map[string]*RouteInfo // key: worktreeName
}

// NamedTunnelProcess represents a running named tunnel cloudflared process
type NamedTunnelProcess struct {
	PID       int
	Cmd       *exec.Cmd
	Cancel    context.CancelFunc
	LogBuffer *LogBuffer
	StartedAt time.Time
}

// RouteInfo tracks an active route (ingress + DNS)
type RouteInfo struct {
	WorktreeName string
	Hostname     string
	Port         int
	DNSRecordID  string
	CreatedAt    time.Time
}

// NewNamedTunnelManager creates a new named tunnel manager for a project
func NewNamedTunnelManager(
	projectName string,
	tunnelID string,
	tunnelName string,
	domain string,
	cfClient *CloudflareClient,
) (*NamedTunnelManager, error) {
	configPath, err := ConfigPath(projectName)
	if err != nil {
		return nil, err
	}

	credsPath, err := CredentialsPath(projectName, tunnelID)
	if err != nil {
		return nil, err
	}

	return &NamedTunnelManager{
		projectName:  projectName,
		tunnelID:     tunnelID,
		tunnelName:   tunnelName,
		domain:       domain,
		configPath:   configPath,
		credsPath:    credsPath,
		cfClient:     cfClient,
		activeRoutes: make(map[string]*RouteInfo),
	}, nil
}

// EnsureTunnel ensures the tunnel exists, creating it if necessary
func (m *NamedTunnelManager) EnsureTunnel() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if tunnel exists
	if m.tunnelID != "" {
		_, err := m.cfClient.GetTunnel(m.tunnelID)
		if err == nil {
			return nil // Tunnel exists
		}
		// Tunnel doesn't exist, will create new one
	}

	// Create new tunnel
	tunnelName := fmt.Sprintf("conductor-%s", m.projectName)
	tunnel, creds, err := m.cfClient.CreateTunnel(tunnelName)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}

	m.tunnelID = tunnel.ID
	m.tunnelName = tunnel.Name

	// Save credentials file
	if err := m.saveCredentials(creds); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}

	// Initialize config file
	cfg := NewTunnelConfig(m.tunnelID, m.credsPath)
	if err := SaveConfig(m.projectName, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// AddRoute adds a route for a worktree port
func (m *NamedTunnelManager) AddRoute(worktreeName string, port int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	hostname := GenerateHostname(worktreeName, port, m.domain)
	if hostname == "" {
		return "", fmt.Errorf("no domain configured")
	}

	// Load current config
	cfg, err := LoadConfig(m.projectName)
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		cfg = NewTunnelConfig(m.tunnelID, m.credsPath)
	}

	// Add ingress rule
	service := GenerateService(port)
	cfg.AddIngress(hostname, service)

	// Save config
	if err := SaveConfig(m.projectName, cfg); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}

	// Create DNS record
	record, err := m.cfClient.CreateDNSRecord(hostname, m.tunnelID)
	if err != nil {
		// Try to find existing record
		existing, findErr := m.cfClient.FindDNSRecord(hostname)
		if findErr != nil || existing == nil {
			return "", fmt.Errorf("failed to create DNS record: %w", err)
		}
		record = existing
	}

	// Track the route
	m.activeRoutes[worktreeName] = &RouteInfo{
		WorktreeName: worktreeName,
		Hostname:     hostname,
		Port:         port,
		DNSRecordID:  record.ID,
		CreatedAt:    time.Now(),
	}

	// Reload tunnel if running
	if m.process != nil && IsProcessRunning(m.process.PID) {
		if err := m.reloadTunnel(); err != nil {
			// Log but don't fail - tunnel will pick up changes on restart
			m.process.LogBuffer.Write(fmt.Sprintf("Warning: failed to reload tunnel: %v", err))
		}
	}

	return fmt.Sprintf("https://%s", hostname), nil
}

// RemoveRoute removes a route for a worktree
func (m *NamedTunnelManager) RemoveRoute(worktreeName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	route, exists := m.activeRoutes[worktreeName]
	if !exists {
		return nil
	}

	// Load current config
	cfg, err := LoadConfig(m.projectName)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		return nil
	}

	// Remove ingress rule
	cfg.RemoveIngress(route.Hostname)

	// Save config
	if err := SaveConfig(m.projectName, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Delete DNS record
	if route.DNSRecordID != "" {
		_ = m.cfClient.DeleteDNSRecord(route.DNSRecordID)
	}

	delete(m.activeRoutes, worktreeName)

	// Reload tunnel if running
	if m.process != nil && IsProcessRunning(m.process.PID) {
		_ = m.reloadTunnel()
	}

	return nil
}

// StartTunnel starts the cloudflared tunnel process
func (m *NamedTunnelManager) StartTunnel(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already running
	if m.process != nil && IsProcessRunning(m.process.PID) {
		return nil
	}

	// Check if cloudflared is installed
	if _, err := exec.LookPath("cloudflared"); err != nil {
		return fmt.Errorf("cloudflared not found. Install with: brew install cloudflared")
	}

	// Ensure config exists
	if _, err := os.Stat(m.configPath); os.IsNotExist(err) {
		return fmt.Errorf("tunnel config not found at %s", m.configPath)
	}

	// Create cancellable context
	tunnelCtx, cancel := context.WithCancel(ctx)

	// Build command
	cmd := exec.CommandContext(tunnelCtx, "cloudflared", "tunnel", "--config", m.configPath, "run")

	// Create pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start cloudflared: %w", err)
	}

	m.process = &NamedTunnelProcess{
		PID:       cmd.Process.Pid,
		Cmd:       cmd,
		Cancel:    cancel,
		LogBuffer: NewLogBuffer(200),
		StartedAt: time.Now(),
	}

	// Read output in goroutines
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			m.process.LogBuffer.Write(scanner.Text())
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			m.process.LogBuffer.Write(scanner.Text())
		}
	}()

	// Save PID file
	pidFile := &PIDFile{
		PID:          m.process.PID,
		ProjectName:  m.projectName,
		WorktreeName: "_named_tunnel",
		Mode:         config.TunnelModeNamed,
		Port:         0,
		URL:          "",
		StartedAt:    m.process.StartedAt,
	}
	_ = WritePIDFile(m.projectName, "_named_tunnel", pidFile)

	return nil
}

// StopTunnel stops the cloudflared tunnel process
func (m *NamedTunnelManager) StopTunnel() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.process == nil {
		return nil
	}

	if m.process.Cancel != nil {
		m.process.Cancel()
	}

	if m.process.Cmd != nil && m.process.Cmd.Process != nil {
		_ = KillProcess(m.process.PID)
	}

	_ = DeletePIDFile(m.projectName, "_named_tunnel")
	m.process = nil

	return nil
}

// IsRunning returns true if the tunnel process is running
func (m *NamedTunnelManager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.process == nil {
		return false
	}
	return IsProcessRunning(m.process.PID)
}

// GetRouteURL returns the URL for a worktree's route
func (m *NamedTunnelManager) GetRouteURL(worktreeName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if route, exists := m.activeRoutes[worktreeName]; exists {
		return fmt.Sprintf("https://%s", route.Hostname)
	}
	return ""
}

// reloadTunnel sends SIGHUP to reload config
func (m *NamedTunnelManager) reloadTunnel() error {
	if m.process == nil || m.process.Cmd == nil || m.process.Cmd.Process == nil {
		return nil
	}

	// cloudflared doesn't support SIGHUP for config reload
	// We need to restart the process
	if err := m.StopTunnel(); err != nil {
		return err
	}

	return m.StartTunnel(context.Background())
}

// saveCredentials saves the tunnel credentials to a JSON file
func (m *NamedTunnelManager) saveCredentials(creds *TunnelCredentials) error {
	configDir, err := ConfigDir(m.projectName)
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	// Write credentials file
	data := fmt.Sprintf(`{
  "AccountTag": "%s",
  "TunnelID": "%s",
  "TunnelName": "%s",
  "TunnelSecret": "%s"
}`, creds.AccountTag, creds.TunnelID, creds.TunnelName, creds.TunnelSecret)

	return os.WriteFile(m.credsPath, []byte(data), 0600)
}

// GetLogs returns the tunnel process logs
func (m *NamedTunnelManager) GetLogs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.process == nil {
		return nil
	}
	return m.process.LogBuffer.Lines()
}
