package tunnel

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// IngressRule represents a single ingress entry in cloudflared config
type IngressRule struct {
	Hostname string `yaml:"hostname,omitempty"`
	Service  string `yaml:"service"`
}

// TunnelConfigFile represents the cloudflared config.yaml structure
type TunnelConfigFile struct {
	Tunnel          string        `yaml:"tunnel"`
	CredentialsFile string        `yaml:"credentials-file"`
	Ingress         []IngressRule `yaml:"ingress"`
}

// ConfigDir returns the path to tunnel config directory for a project
func ConfigDir(projectName string) (string, error) {
	tunnelsDir, err := TunnelsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(tunnelsDir, projectName), nil
}

// ConfigPath returns the path to the cloudflared config file for a project
func ConfigPath(projectName string) (string, error) {
	configDir, err := ConfigDir(projectName)
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "config.yaml"), nil
}

// CredentialsPath returns the path to the tunnel credentials file
func CredentialsPath(projectName, tunnelID string) (string, error) {
	configDir, err := ConfigDir(projectName)
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, tunnelID+".json"), nil
}

// LoadConfig loads an existing tunnel config file
func LoadConfig(projectName string) (*TunnelConfigFile, error) {
	configPath, err := ConfigPath(projectName)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg TunnelConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &cfg, nil
}

// SaveConfig saves the tunnel config file
func SaveConfig(projectName string, cfg *TunnelConfigFile) error {
	configPath, err := ConfigPath(projectName)
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// NewTunnelConfig creates a new tunnel config with default catch-all rule
func NewTunnelConfig(tunnelID, credentialsFile string) *TunnelConfigFile {
	return &TunnelConfigFile{
		Tunnel:          tunnelID,
		CredentialsFile: credentialsFile,
		Ingress: []IngressRule{
			{Service: "http_status:404"}, // Catch-all rule (required)
		},
	}
}

// AddIngress adds an ingress rule to the config
// The rule is inserted before the catch-all rule
func (c *TunnelConfigFile) AddIngress(hostname, service string) {
	// Check if rule already exists
	for i, rule := range c.Ingress {
		if rule.Hostname == hostname {
			// Update existing rule
			c.Ingress[i].Service = service
			return
		}
	}

	// Insert before catch-all (last rule)
	newRule := IngressRule{
		Hostname: hostname,
		Service:  service,
	}

	if len(c.Ingress) > 0 {
		// Insert before the last rule (catch-all)
		c.Ingress = append(c.Ingress[:len(c.Ingress)-1], newRule, c.Ingress[len(c.Ingress)-1])
	} else {
		// No rules, add this one and catch-all
		c.Ingress = []IngressRule{
			newRule,
			{Service: "http_status:404"},
		}
	}
}

// RemoveIngress removes an ingress rule by hostname
func (c *TunnelConfigFile) RemoveIngress(hostname string) bool {
	for i, rule := range c.Ingress {
		if rule.Hostname == hostname {
			c.Ingress = append(c.Ingress[:i], c.Ingress[i+1:]...)
			return true
		}
	}
	return false
}

// HasIngress checks if an ingress rule exists for a hostname
func (c *TunnelConfigFile) HasIngress(hostname string) bool {
	for _, rule := range c.Ingress {
		if rule.Hostname == hostname {
			return true
		}
	}
	return false
}

// IngressCount returns the number of active ingress rules (excluding catch-all)
func (c *TunnelConfigFile) IngressCount() int {
	count := 0
	for _, rule := range c.Ingress {
		if rule.Hostname != "" {
			count++
		}
	}
	return count
}

// GenerateHostname creates the hostname for a named tunnel
// Format: <worktree>-<port>.<domain>
func GenerateHostname(worktreeName string, port int, domain string) string {
	if domain == "" {
		return ""
	}
	return fmt.Sprintf("%s-%d.%s", worktreeName, port, domain)
}

// GenerateService creates the service URL for local port
func GenerateService(port int) string {
	return fmt.Sprintf("http://localhost:%d", port)
}
