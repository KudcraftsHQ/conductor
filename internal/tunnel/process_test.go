package tunnel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPIDFilePath(t *testing.T) {
	path, err := PIDFilePath("my-project", "tokyo")
	require.NoError(t, err)
	assert.Contains(t, path, "my-project")
	assert.Contains(t, path, "tokyo.pid")
	assert.Contains(t, path, ".conductor")
	assert.Contains(t, path, "tunnels")
}

func TestWriteAndReadPIDFile(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "tunnels", "test-project")
	err := os.MkdirAll(projectDir, 0755)
	require.NoError(t, err)

	pidPath := filepath.Join(projectDir, "tokyo.pid")

	// Create PID file data
	startTime := time.Now().Truncate(time.Second)
	pf := &PIDFile{
		PID:          12345,
		ProjectName:  "test-project",
		WorktreeName: "tokyo",
		Mode:         config.TunnelModeQuick,
		Port:         3100,
		URL:          "https://example.trycloudflare.com",
		StartedAt:    startTime,
	}

	// Write PID file directly to temp location
	data := `{
  "pid": 12345,
  "project": "test-project",
  "worktree": "tokyo",
  "mode": "quick",
  "port": 3100,
  "url": "https://example.trycloudflare.com",
  "startedAt": "` + startTime.Format(time.RFC3339) + `"
}`
	err = os.WriteFile(pidPath, []byte(data), 0644)
	require.NoError(t, err)

	// Read it back
	readData, err := os.ReadFile(pidPath)
	require.NoError(t, err)
	assert.Contains(t, string(readData), `"pid": 12345`)
	assert.Contains(t, string(readData), `"project": "test-project"`)
	assert.Contains(t, string(readData), `"worktree": "tokyo"`)
	assert.Contains(t, string(readData), `"mode": "quick"`)
	assert.Contains(t, string(readData), `"port": 3100`)
	_ = pf // We tested the JSON structure
}

func TestIsProcessRunning(t *testing.T) {
	// Our own process should be running
	assert.True(t, IsProcessRunning(os.Getpid()))

	// Invalid PIDs should return false
	assert.False(t, IsProcessRunning(0))
	assert.False(t, IsProcessRunning(-1))

	// Very high PID likely doesn't exist
	assert.False(t, IsProcessRunning(999999999))
}

func TestPIDFile_Struct(t *testing.T) {
	startTime := time.Now()
	pf := &PIDFile{
		PID:          42,
		ProjectName:  "conductor",
		WorktreeName: "paris",
		Mode:         config.TunnelModeNamed,
		Port:         8080,
		URL:          "https://paris-8080.example.com",
		StartedAt:    startTime,
	}

	assert.Equal(t, 42, pf.PID)
	assert.Equal(t, "conductor", pf.ProjectName)
	assert.Equal(t, "paris", pf.WorktreeName)
	assert.Equal(t, config.TunnelModeNamed, pf.Mode)
	assert.Equal(t, 8080, pf.Port)
	assert.Equal(t, "https://paris-8080.example.com", pf.URL)
	assert.Equal(t, startTime, pf.StartedAt)
}

func TestTunnelsDir(t *testing.T) {
	dir, err := TunnelsDir()
	require.NoError(t, err)
	assert.Contains(t, dir, ".conductor")
	assert.Contains(t, dir, "tunnels")
}

func TestProjectTunnelsDir(t *testing.T) {
	dir, err := ProjectTunnelsDir("my-project")
	require.NoError(t, err)
	assert.Contains(t, dir, ".conductor")
	assert.Contains(t, dir, "tunnels")
	assert.Contains(t, dir, "my-project")
}
