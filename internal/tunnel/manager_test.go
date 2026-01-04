package tunnel

import (
	"testing"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestGetDomainForProject(t *testing.T) {
	tests := []struct {
		name          string
		globalDomain  string
		projectDomain string
		expected      string
	}{
		{
			name:          "project domain takes precedence",
			globalDomain:  "global.com",
			projectDomain: "project.com",
			expected:      "project.com",
		},
		{
			name:          "falls back to global domain",
			globalDomain:  "global.com",
			projectDomain: "",
			expected:      "global.com",
		},
		{
			name:          "empty when both are empty",
			globalDomain:  "",
			projectDomain: "",
			expected:      "",
		},
		{
			name:          "project domain only",
			globalDomain:  "",
			projectDomain: "project.com",
			expected:      "project.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Defaults: config.Defaults{
					Tunnel: config.TunnelDefaults{
						Domain: tt.globalDomain,
					},
				},
			}

			var projectConfig *config.ProjectConfig
			if tt.projectDomain != "" {
				projectConfig = &config.ProjectConfig{
					Tunnel: &config.ProjectTunnelConfig{
						Domain: tt.projectDomain,
					},
				}
			}

			result := GetDomainForProject(cfg, projectConfig)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDomainForProject_NilProjectConfig(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Tunnel: config.TunnelDefaults{
				Domain: "global.com",
			},
		},
	}

	result := GetDomainForProject(cfg, nil)
	assert.Equal(t, "global.com", result)
}

func TestGetDomainForProject_NilTunnelConfig(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Tunnel: config.TunnelDefaults{
				Domain: "global.com",
			},
		},
	}

	projectConfig := &config.ProjectConfig{
		Tunnel: nil, // No tunnel config in project
	}

	result := GetDomainForProject(cfg, projectConfig)
	assert.Equal(t, "global.com", result)
}

func TestGenerateTunnelHostname(t *testing.T) {
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
			name:         "different worktree and port",
			worktreeName: "paris",
			port:         8080,
			domain:       "myapp.dev",
			expected:     "paris-8080.myapp.dev",
		},
		{
			name:         "empty domain returns empty string",
			worktreeName: "london",
			port:         3000,
			domain:       "",
			expected:     "",
		},
		{
			name:         "subdomain",
			worktreeName: "berlin",
			port:         5000,
			domain:       "dev.example.com",
			expected:     "berlin-5000.dev.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateTunnelHostname(tt.worktreeName, tt.port, tt.domain)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTunnelKey(t *testing.T) {
	key := tunnelKey("myproject", "tokyo")
	assert.Equal(t, "myproject/tokyo", key)

	key = tunnelKey("another-project", "paris")
	assert.Equal(t, "another-project/paris", key)
}

func TestNewManager(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	assert.NotNil(t, mgr)
	assert.NotNil(t, mgr.activeTunnels)
	assert.NotNil(t, mgr.namedManagers)
	assert.NotNil(t, mgr.cli)
	assert.NotNil(t, mgr.ctx)
	assert.NotNil(t, mgr.cancel)
}

func TestManager_IsCloudflaredInstalled(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Just verify it doesn't panic - actual result depends on system
	_ = mgr.IsCloudflaredInstalled()
}

func TestManager_IsCloudflaredAuthenticated(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Just verify it doesn't panic - actual result depends on system
	_ = mgr.IsCloudflaredAuthenticated()
}

func TestManager_Close(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Close should not error even with no tunnels
	err := mgr.Close()
	assert.NoError(t, err)
}

func TestManager_StopAll_Empty(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// StopAll should not error with no tunnels
	err := mgr.StopAll()
	assert.NoError(t, err)
}

func TestManager_GetStatus_NotRunning(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Should return nil for non-existent tunnel
	status := mgr.GetStatus("nonexistent", "tokyo")
	assert.Nil(t, status)
}

func TestManager_IsRunning_NotRunning(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Should return false for non-existent tunnel
	running := mgr.IsRunning("nonexistent", "tokyo")
	assert.False(t, running)
}

func TestManager_GetURL_NotRunning(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Should return empty for non-existent tunnel
	url := mgr.GetURL("nonexistent", "tokyo")
	assert.Empty(t, url)
}

func TestManager_GetLogs_NotRunning(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Should return nil for non-existent tunnel
	logs := mgr.GetLogs("nonexistent", "tokyo")
	assert.Nil(t, logs)
}

func TestManager_StopTunnel_NotRunning(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Should not error for non-existent tunnel
	err := mgr.StopTunnel("nonexistent", "tokyo")
	assert.NoError(t, err)
}

func TestManager_StopNamedTunnel_NotExists(t *testing.T) {
	cfg := config.NewConfig()
	mgr := NewManager(cfg)

	// Should not error for non-existent named tunnel
	err := mgr.StopNamedTunnel("nonexistent", "tokyo")
	assert.NoError(t, err)
}
