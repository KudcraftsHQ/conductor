package tunnel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CloudflaredCLI wraps the cloudflared CLI for tunnel management
type CloudflaredCLI struct{}

// NewCloudflaredCLI creates a new CLI wrapper
func NewCloudflaredCLI() *CloudflaredCLI {
	return &CloudflaredCLI{}
}

// IsInstalled checks if cloudflared is installed
func (c *CloudflaredCLI) IsInstalled() bool {
	_, err := exec.LookPath("cloudflared")
	return err == nil
}

// IsAuthenticated checks if the user has run `cloudflared tunnel login`
// by checking for cert.pem in the default location
func (c *CloudflaredCLI) IsAuthenticated() bool {
	certPath := c.GetCertPath()
	_, err := os.Stat(certPath)
	return err == nil
}

// GetCertPath returns the path to the cloudflared cert.pem
func (c *CloudflaredCLI) GetCertPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cloudflared", "cert.pem")
}

// GetCloudflaredDir returns the cloudflared config directory
func (c *CloudflaredCLI) GetCloudflaredDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cloudflared")
}

// Login runs `cloudflared tunnel login` - opens browser for authentication
func (c *CloudflaredCLI) Login() error {
	cmd := exec.Command("cloudflared", "tunnel", "login")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// CLITunnelInfo represents tunnel info from `cloudflared tunnel list`
type CLITunnelInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at"`
	DeletedAt  string `json:"deleted_at,omitempty"`
	Connectors []struct {
		ID    string `json:"id"`
		RunAt string `json:"run_at"`
	} `json:"conns"`
}

// ListTunnels runs `cloudflared tunnel list --output json`
func (c *CloudflaredCLI) ListTunnels() ([]CLITunnelInfo, error) {
	cmd := exec.Command("cloudflared", "tunnel", "list", "--output", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list tunnels: %w", err)
	}

	var tunnels []CLITunnelInfo
	if err := json.Unmarshal(output, &tunnels); err != nil {
		return nil, fmt.Errorf("failed to parse tunnel list: %w", err)
	}

	return tunnels, nil
}

// FindTunnel finds a tunnel by name
func (c *CloudflaredCLI) FindTunnel(name string) (*CLITunnelInfo, error) {
	tunnels, err := c.ListTunnels()
	if err != nil {
		return nil, err
	}

	for _, t := range tunnels {
		// DeletedAt is "0001-01-01T00:00:00Z" (Go zero time) when not deleted
		if t.Name == name && (t.DeletedAt == "" || t.DeletedAt == "0001-01-01T00:00:00Z") {
			return &t, nil
		}
	}
	return nil, nil
}

// CreateTunnel runs `cloudflared tunnel create <name>`
// Returns the tunnel ID
func (c *CloudflaredCLI) CreateTunnel(name string) (string, error) {
	cmd := exec.Command("cloudflared", "tunnel", "create", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to create tunnel: %s", string(output))
	}

	// Parse tunnel ID from output
	// Output format: "Created tunnel <name> with id <UUID>"
	re := regexp.MustCompile(`with id ([a-f0-9-]{36})`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		// Try to find the tunnel by name if we can't parse the output
		tunnel, err := c.FindTunnel(name)
		if err != nil {
			return "", fmt.Errorf("tunnel created but couldn't get ID: %s", string(output))
		}
		if tunnel != nil {
			return tunnel.ID, nil
		}
		return "", fmt.Errorf("tunnel created but couldn't get ID: %s", string(output))
	}

	return matches[1], nil
}

// DeleteTunnel runs `cloudflared tunnel delete <name>`
func (c *CloudflaredCLI) DeleteTunnel(name string) error {
	cmd := exec.Command("cloudflared", "tunnel", "delete", "-f", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete tunnel: %s", string(output))
	}
	return nil
}

// RouteDNS runs `cloudflared tunnel route dns <tunnel> <hostname>`
func (c *CloudflaredCLI) RouteDNS(tunnelName, hostname string) error {
	cmd := exec.Command("cloudflared", "tunnel", "route", "dns", "-f", tunnelName, hostname)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if it's just a "already exists" error, which is fine
		if strings.Contains(string(output), "already exists") {
			return nil
		}
		return fmt.Errorf("failed to route DNS: %s", string(output))
	}
	return nil
}

// GetCredentialsPath returns the path to the tunnel credentials JSON file
// The file is created by `cloudflared tunnel create` at ~/.cloudflared/<UUID>.json
func (c *CloudflaredCLI) GetCredentialsPath(tunnelID string) string {
	return filepath.Join(c.GetCloudflaredDir(), tunnelID+".json")
}

// CredentialsExist checks if credentials file exists for a tunnel
func (c *CloudflaredCLI) CredentialsExist(tunnelID string) bool {
	_, err := os.Stat(c.GetCredentialsPath(tunnelID))
	return err == nil
}

// GetDomainFromCert attempts to extract the domain from cert.pem
// Returns empty string if unable to determine
func (c *CloudflaredCLI) GetDomainFromCert() string {
	certPath := c.GetCertPath()
	file, err := os.Open(certPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	// The cert.pem is a bundle, we can try to find the zone/domain in it
	// This is a best-effort attempt - the domain should be in the cert metadata
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Look for zone info in the cert (this is a heuristic)
		if strings.Contains(line, "zone") || strings.Contains(line, "domain") {
			// Extract domain-like patterns
			re := regexp.MustCompile(`[a-zA-Z0-9][-a-zA-Z0-9]*\.[a-zA-Z]{2,}`)
			if match := re.FindString(line); match != "" {
				return match
			}
		}
	}
	return ""
}

// ValidateAuth checks if cloudflared auth is valid by attempting to list tunnels
func (c *CloudflaredCLI) ValidateAuth() error {
	if !c.IsInstalled() {
		return fmt.Errorf("cloudflared not installed. Install with: brew install cloudflared")
	}

	if !c.IsAuthenticated() {
		return fmt.Errorf("cloudflared not authenticated. Run: cloudflared tunnel login")
	}

	// Try to list tunnels to verify the auth is still valid
	_, err := c.ListTunnels()
	if err != nil {
		return fmt.Errorf("cloudflared auth may be expired. Run: cloudflared tunnel login")
	}

	return nil
}

// EnsureTunnel ensures a tunnel with the given name exists, creating it if necessary
// Returns the tunnel ID
func (c *CloudflaredCLI) EnsureTunnel(name string) (string, error) {
	// Check if tunnel already exists
	tunnel, err := c.FindTunnel(name)
	if err != nil {
		return "", err
	}

	if tunnel != nil {
		return tunnel.ID, nil
	}

	// Create new tunnel
	return c.CreateTunnel(name)
}
