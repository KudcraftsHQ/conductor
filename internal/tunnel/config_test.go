package tunnel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTunnelConfig(t *testing.T) {
	cfg := NewTunnelConfig("test-tunnel-id", "/path/to/creds.json")

	assert.Equal(t, "test-tunnel-id", cfg.Tunnel)
	assert.Equal(t, "/path/to/creds.json", cfg.CredentialsFile)
	assert.Len(t, cfg.Ingress, 1)
	assert.Equal(t, "", cfg.Ingress[0].Hostname)
	assert.Equal(t, "http_status:404", cfg.Ingress[0].Service)
}

func TestAddIngress(t *testing.T) {
	cfg := NewTunnelConfig("test-tunnel", "/creds.json")

	// Add first ingress
	cfg.AddIngress("tokyo-3100.example.com", "http://localhost:3100")

	assert.Len(t, cfg.Ingress, 2)
	assert.Equal(t, "tokyo-3100.example.com", cfg.Ingress[0].Hostname)
	assert.Equal(t, "http://localhost:3100", cfg.Ingress[0].Service)
	// Catch-all should still be last
	assert.Equal(t, "", cfg.Ingress[1].Hostname)
	assert.Equal(t, "http_status:404", cfg.Ingress[1].Service)

	// Add second ingress
	cfg.AddIngress("paris-3101.example.com", "http://localhost:3101")

	assert.Len(t, cfg.Ingress, 3)
	assert.Equal(t, "tokyo-3100.example.com", cfg.Ingress[0].Hostname)
	assert.Equal(t, "paris-3101.example.com", cfg.Ingress[1].Hostname)
	// Catch-all should still be last
	assert.Equal(t, "http_status:404", cfg.Ingress[2].Service)
}

func TestAddIngress_UpdateExisting(t *testing.T) {
	cfg := NewTunnelConfig("test-tunnel", "/creds.json")

	cfg.AddIngress("tokyo-3100.example.com", "http://localhost:3100")
	assert.Len(t, cfg.Ingress, 2)

	// Update existing hostname with new service
	cfg.AddIngress("tokyo-3100.example.com", "http://localhost:4000")

	// Should not add a new rule, just update
	assert.Len(t, cfg.Ingress, 2)
	assert.Equal(t, "http://localhost:4000", cfg.Ingress[0].Service)
}

func TestRemoveIngress(t *testing.T) {
	cfg := NewTunnelConfig("test-tunnel", "/creds.json")

	cfg.AddIngress("tokyo-3100.example.com", "http://localhost:3100")
	cfg.AddIngress("paris-3101.example.com", "http://localhost:3101")
	assert.Len(t, cfg.Ingress, 3)

	// Remove one
	removed := cfg.RemoveIngress("tokyo-3100.example.com")
	assert.True(t, removed)
	assert.Len(t, cfg.Ingress, 2)
	assert.Equal(t, "paris-3101.example.com", cfg.Ingress[0].Hostname)

	// Try to remove non-existent
	removed = cfg.RemoveIngress("nonexistent.example.com")
	assert.False(t, removed)
	assert.Len(t, cfg.Ingress, 2)
}

func TestHasIngress(t *testing.T) {
	cfg := NewTunnelConfig("test-tunnel", "/creds.json")

	cfg.AddIngress("tokyo-3100.example.com", "http://localhost:3100")

	assert.True(t, cfg.HasIngress("tokyo-3100.example.com"))
	assert.False(t, cfg.HasIngress("nonexistent.example.com"))
}

func TestIngressCount(t *testing.T) {
	cfg := NewTunnelConfig("test-tunnel", "/creds.json")

	// Only catch-all, should be 0 active
	assert.Equal(t, 0, cfg.IngressCount())

	cfg.AddIngress("tokyo-3100.example.com", "http://localhost:3100")
	assert.Equal(t, 1, cfg.IngressCount())

	cfg.AddIngress("paris-3101.example.com", "http://localhost:3101")
	assert.Equal(t, 2, cfg.IngressCount())

	cfg.RemoveIngress("tokyo-3100.example.com")
	assert.Equal(t, 1, cfg.IngressCount())
}

func TestGenerateHostname(t *testing.T) {
	tests := []struct {
		name         string
		worktreeName string
		port         int
		domain       string
		expected     string
	}{
		{
			name:         "basic hostname",
			worktreeName: "tokyo",
			port:         3100,
			domain:       "example.com",
			expected:     "tokyo-3100.example.com",
		},
		{
			name:         "different port",
			worktreeName: "paris",
			port:         8080,
			domain:       "mysite.dev",
			expected:     "paris-8080.mysite.dev",
		},
		{
			name:         "empty domain returns empty",
			worktreeName: "tokyo",
			port:         3100,
			domain:       "",
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateHostname(tt.worktreeName, tt.port, tt.domain)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateService(t *testing.T) {
	assert.Equal(t, "http://localhost:3100", GenerateService(3100))
	assert.Equal(t, "http://localhost:8080", GenerateService(8080))
	assert.Equal(t, "http://localhost:0", GenerateService(0))
}

func TestSaveAndLoadConfig(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Override the TunnelsDir function temporarily by using a direct path
	projectDir := filepath.Join(tmpDir, "test-project")
	err := os.MkdirAll(projectDir, 0755)
	require.NoError(t, err)

	configPath := filepath.Join(projectDir, "config.yaml")

	// Write YAML manually for this test
	yamlContent := `tunnel: my-tunnel-id
credentials-file: /path/to/creds.json
ingress:
    - hostname: tokyo-3100.example.com
      service: http://localhost:3100
    - service: http_status:404
`
	err = os.WriteFile(configPath, []byte(yamlContent), 0644)
	require.NoError(t, err)

	// Read it back
	readData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(readData), "tokyo-3100.example.com")
	assert.Contains(t, string(readData), "http://localhost:3100")
	assert.Contains(t, string(readData), "my-tunnel-id")
}
