package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig(t *testing.T) {
	cfg := NewConfig()

	assert.Equal(t, 1, cfg.Version)
	assert.NotNil(t, cfg.Projects)
	assert.NotNil(t, cfg.PortAllocations)
	assert.Equal(t, 0, len(cfg.Projects))
	assert.Equal(t, 0, len(cfg.PortAllocations))
	assert.Equal(t, 3100, cfg.Defaults.PortRangeStart)
	assert.Equal(t, 3999, cfg.Defaults.PortRangeEnd)
	assert.Equal(t, 1, cfg.Defaults.PortsPerWorktree)
}

func TestLoadProjectConfig_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := LoadProjectConfig(tmpDir)

	// Non-existent project config should return nil, nil
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestLoadProjectConfig_ValidConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a valid conductor.json
	projectCfg := &ProjectConfig{
		Scripts: map[string]string{
			"setup": "echo setup",
			"run":   "echo run",
		},
		Ports: PortConfig{
			Default: 2,
			Labels:  []string{"web", "api"},
		},
	}
	data, err := json.MarshalIndent(projectCfg, "", "  ")
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "conductor.json"), data, 0644)
	require.NoError(t, err)

	// Load it back
	loaded, err := LoadProjectConfig(tmpDir)

	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "echo setup", loaded.Scripts["setup"])
	assert.Equal(t, "echo run", loaded.Scripts["run"])
	assert.Equal(t, 2, loaded.Ports.Default)
	assert.Equal(t, []string{"web", "api"}, loaded.Ports.Labels)
}

func TestLoadProjectConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Create invalid JSON
	err := os.WriteFile(filepath.Join(tmpDir, "conductor.json"), []byte("not valid json"), 0644)
	require.NoError(t, err)

	cfg, err := LoadProjectConfig(tmpDir)

	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "failed to parse project config")
}

func TestSaveProjectConfig(t *testing.T) {
	tmpDir := t.TempDir()

	projectCfg := &ProjectConfig{
		Scripts: map[string]string{
			"setup": "npm install",
		},
		Ports: PortConfig{
			Default: 3,
			Labels:  []string{"frontend", "backend", "db"},
		},
	}

	err := SaveProjectConfig(tmpDir, projectCfg)
	require.NoError(t, err)

	// Verify file was created
	configPath := filepath.Join(tmpDir, "conductor.json")
	_, err = os.Stat(configPath)
	require.NoError(t, err)

	// Load it back and verify
	loaded, err := LoadProjectConfig(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, projectCfg.Scripts["setup"], loaded.Scripts["setup"])
	assert.Equal(t, projectCfg.Ports.Default, loaded.Ports.Default)
	assert.Equal(t, projectCfg.Ports.Labels, loaded.Ports.Labels)
}

func TestWorktreeBasePath(t *testing.T) {
	path, err := WorktreeBasePath("myproject")

	require.NoError(t, err)
	assert.Contains(t, path, ".conductor")
	assert.Contains(t, path, "myproject")
}

func TestWorktreePath(t *testing.T) {
	path, err := WorktreePath("myproject", "tokyo")

	require.NoError(t, err)
	assert.Contains(t, path, ".conductor")
	assert.Contains(t, path, "myproject")
	assert.Contains(t, path, "tokyo")
}

func TestConductorDir(t *testing.T) {
	dir, err := ConductorDir()

	require.NoError(t, err)
	assert.Contains(t, dir, ".conductor")
}

func TestConfigPath(t *testing.T) {
	path, err := ConfigPath()

	require.NoError(t, err)
	assert.Contains(t, path, ".conductor")
	assert.Contains(t, path, "conductor.json")
}

func TestNewProject(t *testing.T) {
	project := NewProject("/path/to/project", 2)

	assert.Equal(t, "/path/to/project", project.Path)
	assert.Equal(t, 2, project.DefaultPortsPerWorktree)
	assert.NotNil(t, project.Worktrees)
	assert.Equal(t, 0, len(project.Worktrees))
	assert.False(t, project.AddedAt.IsZero())
}

func TestNewWorktree(t *testing.T) {
	wt := NewWorktree("/path/to/worktree", "feature-branch", false, []int{3100, 3101})

	assert.Equal(t, "/path/to/worktree", wt.Path)
	assert.Equal(t, "feature-branch", wt.Branch)
	assert.False(t, wt.IsRoot)
	assert.Equal(t, []int{3100, 3101}, wt.Ports)
	assert.False(t, wt.CreatedAt.IsZero())
	assert.Equal(t, SetupStatusNone, wt.SetupStatus)
}

func TestNewWorktree_Root(t *testing.T) {
	wt := NewWorktree("/path/to/project", "main", true, []int{3100})

	assert.Equal(t, "/path/to/project", wt.Path)
	assert.Equal(t, "main", wt.Branch)
	assert.True(t, wt.IsRoot)
	assert.Equal(t, []int{3100}, wt.Ports)
}
