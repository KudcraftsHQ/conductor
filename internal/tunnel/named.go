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
	cli          *CloudflaredCLI
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
	CreatedAt    time.Time
}

// NewNamedTunnelManager creates a new named tunnel manager for a project
func NewNamedTunnelManager(
	projectName string,
	tunnelID string,
	tunnelName string,
	domain string,
	cli *CloudflaredCLI,
) (*NamedTunnelManager, error) {
	configPath, err := ConfigPath(projectName)
	if err != nil {
		return nil, err
	}

	// Credentials path will be set when we know the tunnel ID
	credsPath := ""
	if tunnelID != "" {
		credsPath = cli.GetCredentialsPath(tunnelID)
	}

	return &NamedTunnelManager{
		projectName:  projectName,
		tunnelID:     tunnelID,
		tunnelName:   tunnelName,
		domain:       domain,
		configPath:   configPath,
		credsPath:    credsPath,
		cli:          cli,
		activeRoutes: make(map[string]*RouteInfo),
	}, nil
}

// EnsureTunnel ensures the tunnel exists, creating it if necessary
func (m *NamedTunnelManager) EnsureTunnel() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if cloudflared is authenticated
	if err := m.cli.ValidateAuth(); err != nil {
		return err
	}

	// Generate tunnel name if not set
	if m.tunnelName == "" {
		m.tunnelName = fmt.Sprintf("conductor-%s", m.projectName)
	}

	// Check if tunnel already exists by ID or name
	if m.tunnelID != "" {
		// Verify tunnel still exists
		tunnel, err := m.cli.FindTunnel(m.tunnelName)
		if err == nil && tunnel != nil && tunnel.ID == m.tunnelID {
			// Update credentials path
			m.credsPath = m.cli.GetCredentialsPath(m.tunnelID)
			return nil
		}
		// Tunnel doesn't exist anymore, will create new one
		m.tunnelID = ""
	}

	// Try to find existing tunnel by name
	tunnel, err := m.cli.FindTunnel(m.tunnelName)
	if err != nil {
		return fmt.Errorf("failed to check existing tunnels: %w", err)
	}

	if tunnel != nil {
		// Use existing tunnel
		m.tunnelID = tunnel.ID
		m.credsPath = m.cli.GetCredentialsPath(m.tunnelID)
		return nil
	}

	// Create new tunnel using CLI
	tunnelID, err := m.cli.CreateTunnel(m.tunnelName)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}

	m.tunnelID = tunnelID
	m.credsPath = m.cli.GetCredentialsPath(m.tunnelID)

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

	// Route DNS using cloudflared CLI
	if err := m.cli.RouteDNS(m.tunnelName, hostname); err != nil {
		return "", fmt.Errorf("failed to route DNS: %w", err)
	}

	// Track the route
	m.activeRoutes[worktreeName] = &RouteInfo{
		WorktreeName: worktreeName,
		Hostname:     hostname,
		Port:         port,
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

	// Note: We don't delete DNS records - they're managed by cloudflared
	// and will be cleaned up when the tunnel is deleted

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
	if !m.cli.IsInstalled() {
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

// GetLogs returns the tunnel process logs
func (m *NamedTunnelManager) GetLogs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.process == nil {
		return nil
	}
	return m.process.LogBuffer.Lines()
}

// GetTunnelID returns the tunnel ID
func (m *NamedTunnelManager) GetTunnelID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tunnelID
}

// GetTunnelName returns the tunnel name
func (m *NamedTunnelManager) GetTunnelName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tunnelName
}
