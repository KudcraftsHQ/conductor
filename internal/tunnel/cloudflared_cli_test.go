package tunnel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewCloudflaredCLI(t *testing.T) {
	cli := NewCloudflaredCLI()
	assert.NotNil(t, cli)
}

func TestGetCertPath(t *testing.T) {
	cli := NewCloudflaredCLI()
	certPath := cli.GetCertPath()

	assert.Contains(t, certPath, ".cloudflared")
	assert.Contains(t, certPath, "cert.pem")
}

func TestGetCloudflaredDir(t *testing.T) {
	cli := NewCloudflaredCLI()
	dir := cli.GetCloudflaredDir()

	assert.Contains(t, dir, ".cloudflared")
}

func TestGetCredentialsPath(t *testing.T) {
	cli := NewCloudflaredCLI()

	path := cli.GetCredentialsPath("abc123-def456")
	assert.Contains(t, path, ".cloudflared")
	assert.Contains(t, path, "abc123-def456.json")
}

func TestIsInstalled(t *testing.T) {
	cli := NewCloudflaredCLI()

	// This will return true if cloudflared is installed, false otherwise
	// We just verify it doesn't panic
	_ = cli.IsInstalled()
}

func TestIsAuthenticated(t *testing.T) {
	cli := NewCloudflaredCLI()

	// Test that cert.pem check works
	// Create a temporary cert.pem to test
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")

	// Without cert.pem, a fresh CLI would check ~/.cloudflared/cert.pem
	// We can't easily mock this, so just verify the function runs
	_ = cli.IsAuthenticated()

	// Create a fake cert file
	err := os.WriteFile(certPath, []byte("fake cert"), 0644)
	assert.NoError(t, err)
}

func TestCredentialsExist(t *testing.T) {
	cli := NewCloudflaredCLI()

	// Non-existent credentials should return false
	exists := cli.CredentialsExist("nonexistent-tunnel-id")
	assert.False(t, exists)
}

func TestCLITunnelInfo_Struct(t *testing.T) {
	info := CLITunnelInfo{
		ID:        "abc123",
		Name:      "my-tunnel",
		CreatedAt: "2024-01-01T00:00:00Z",
		DeletedAt: "",
	}

	assert.Equal(t, "abc123", info.ID)
	assert.Equal(t, "my-tunnel", info.Name)
	assert.Equal(t, "2024-01-01T00:00:00Z", info.CreatedAt)
	assert.Empty(t, info.DeletedAt)
}

func TestValidateAuth(t *testing.T) {
	cli := NewCloudflaredCLI()

	// ValidateAuth checks if cloudflared is installed and authenticated
	// In CI, cloudflared may not be installed, so we just verify the function
	// returns an appropriate error or nil without panicking
	err := cli.ValidateAuth()

	// If cloudflared is not installed, we expect an error
	if !cli.IsInstalled() {
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not installed")
	}
	// If installed but not authenticated, we expect an auth error
	// If installed and authenticated, err should be nil
	// Either way, the function should complete without panic
}

func TestListTunnels_NotInstalled(t *testing.T) {
	cli := NewCloudflaredCLI()

	// Skip if cloudflared is not installed
	if !cli.IsInstalled() {
		t.Skip("cloudflared not installed, skipping test")
	}

	// If installed, ListTunnels may fail if not authenticated
	// Just verify it doesn't panic
	_, _ = cli.ListTunnels()
}

func TestFindTunnel_NotInstalled(t *testing.T) {
	cli := NewCloudflaredCLI()

	// Skip if cloudflared is not installed
	if !cli.IsInstalled() {
		t.Skip("cloudflared not installed, skipping test")
	}

	// If installed, FindTunnel may fail if not authenticated
	// Just verify it doesn't panic
	_, _ = cli.FindTunnel("nonexistent-tunnel")
}
