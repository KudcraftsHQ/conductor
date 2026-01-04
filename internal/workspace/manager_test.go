package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverInterruptedStates_CreatingWithoutDirectory(t *testing.T) {
	// Create a temp config with a worktree in "creating" state but no directory
	cfg := config.NewConfig()
	cfg.Projects["test-project"] = &config.Project{
		Path:      "/tmp/nonexistent-repo",
		Worktrees: make(map[string]*config.Worktree),
	}
	cfg.Projects["test-project"].Worktrees["tokyo"] = &config.Worktree{
		Path:        "/tmp/nonexistent-worktree-path",
		Branch:      "feature-branch",
		SetupStatus: config.SetupStatusCreating,
	}

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	assert.Equal(t, 1, recovered)
	assert.Equal(t, config.SetupStatusFailed, cfg.Projects["test-project"].Worktrees["tokyo"].SetupStatus)
}

func TestRecoverInterruptedStates_RunningState(t *testing.T) {
	// Create a temp config with a worktree in "running" state
	cfg := config.NewConfig()
	cfg.Projects["test-project"] = &config.Project{
		Path:      "/tmp/nonexistent-repo",
		Worktrees: make(map[string]*config.Worktree),
	}
	cfg.Projects["test-project"].Worktrees["paris"] = &config.Worktree{
		Path:        "/tmp/some-worktree-path",
		Branch:      "feature-branch",
		SetupStatus: config.SetupStatusRunning,
	}

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	assert.Equal(t, 1, recovered)
	assert.Equal(t, config.SetupStatusFailed, cfg.Projects["test-project"].Worktrees["paris"].SetupStatus)
}

func TestRecoverInterruptedStates_DoneStateUnchanged(t *testing.T) {
	// Worktrees in "done" state should not be changed
	cfg := config.NewConfig()
	cfg.Projects["test-project"] = &config.Project{
		Path:      "/tmp/nonexistent-repo",
		Worktrees: make(map[string]*config.Worktree),
	}
	cfg.Projects["test-project"].Worktrees["london"] = &config.Worktree{
		Path:        "/tmp/some-worktree-path",
		Branch:      "feature-branch",
		SetupStatus: config.SetupStatusDone,
	}

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	assert.Equal(t, 0, recovered)
	assert.Equal(t, config.SetupStatusDone, cfg.Projects["test-project"].Worktrees["london"].SetupStatus)
}

func TestRecoverInterruptedStates_FailedStateUnchanged(t *testing.T) {
	// Worktrees already in "failed" state should not be changed
	cfg := config.NewConfig()
	cfg.Projects["test-project"] = &config.Project{
		Path:      "/tmp/nonexistent-repo",
		Worktrees: make(map[string]*config.Worktree),
	}
	cfg.Projects["test-project"].Worktrees["berlin"] = &config.Worktree{
		Path:        "/tmp/some-worktree-path",
		Branch:      "feature-branch",
		SetupStatus: config.SetupStatusFailed,
	}

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	assert.Equal(t, 0, recovered)
	assert.Equal(t, config.SetupStatusFailed, cfg.Projects["test-project"].Worktrees["berlin"].SetupStatus)
}

func TestRecoverInterruptedStates_ArchivedSkipped(t *testing.T) {
	// Archived worktrees should be skipped even if they have "creating" status
	cfg := config.NewConfig()
	cfg.Projects["test-project"] = &config.Project{
		Path:      "/tmp/nonexistent-repo",
		Worktrees: make(map[string]*config.Worktree),
	}
	cfg.Projects["test-project"].Worktrees["madrid"] = &config.Worktree{
		Path:        "/tmp/some-worktree-path",
		Branch:      "feature-branch",
		SetupStatus: config.SetupStatusCreating,
		Archived:    true,
	}

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	assert.Equal(t, 0, recovered)
	// Status should remain unchanged for archived worktrees
	assert.Equal(t, config.SetupStatusCreating, cfg.Projects["test-project"].Worktrees["madrid"].SetupStatus)
}

func TestRecoverInterruptedStates_MultipleWorktrees(t *testing.T) {
	// Test with multiple worktrees in different states
	cfg := config.NewConfig()
	cfg.Projects["test-project"] = &config.Project{
		Path:      "/tmp/nonexistent-repo",
		Worktrees: make(map[string]*config.Worktree),
	}
	cfg.Projects["test-project"].Worktrees["tokyo"] = &config.Worktree{
		Path:        "/tmp/wt1",
		Branch:      "branch1",
		SetupStatus: config.SetupStatusCreating,
	}
	cfg.Projects["test-project"].Worktrees["paris"] = &config.Worktree{
		Path:        "/tmp/wt2",
		Branch:      "branch2",
		SetupStatus: config.SetupStatusRunning,
	}
	cfg.Projects["test-project"].Worktrees["london"] = &config.Worktree{
		Path:        "/tmp/wt3",
		Branch:      "branch3",
		SetupStatus: config.SetupStatusDone,
	}
	cfg.Projects["test-project"].Worktrees["berlin"] = &config.Worktree{
		Path:        "/tmp/wt4",
		Branch:      "branch4",
		SetupStatus: config.SetupStatusFailed,
	}

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	assert.Equal(t, 2, recovered)
	assert.Equal(t, config.SetupStatusFailed, cfg.Projects["test-project"].Worktrees["tokyo"].SetupStatus)
	assert.Equal(t, config.SetupStatusFailed, cfg.Projects["test-project"].Worktrees["paris"].SetupStatus)
	assert.Equal(t, config.SetupStatusDone, cfg.Projects["test-project"].Worktrees["london"].SetupStatus)
	assert.Equal(t, config.SetupStatusFailed, cfg.Projects["test-project"].Worktrees["berlin"].SetupStatus)
}

func TestRecoverInterruptedStates_CreatingWithExistingDirectory(t *testing.T) {
	// Create a temp directory to simulate a partially created worktree
	tmpDir, err := os.MkdirTemp("", "conductor-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	worktreePath := filepath.Join(tmpDir, "worktree")
	err = os.MkdirAll(worktreePath, 0755)
	require.NoError(t, err)

	// Create a config with a worktree in "creating" state with existing directory
	// but the directory is not a valid git worktree
	cfg := config.NewConfig()
	cfg.Projects["test-project"] = &config.Project{
		Path:      "/tmp/nonexistent-repo", // Invalid repo path
		Worktrees: make(map[string]*config.Worktree),
	}
	cfg.Projects["test-project"].Worktrees["rome"] = &config.Worktree{
		Path:        worktreePath,
		Branch:      "feature-branch",
		SetupStatus: config.SetupStatusCreating,
	}

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	// Should recover because even though directory exists, git verification will fail
	assert.Equal(t, 1, recovered)
	assert.Equal(t, config.SetupStatusFailed, cfg.Projects["test-project"].Worktrees["rome"].SetupStatus)
}

func TestRecoverInterruptedStates_NoProjects(t *testing.T) {
	// Test with empty config
	cfg := config.NewConfig()

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	assert.Equal(t, 0, recovered)
}

func TestRecoverInterruptedStates_NoWorktrees(t *testing.T) {
	// Test with project but no worktrees
	cfg := config.NewConfig()
	cfg.Projects["test-project"] = &config.Project{
		Path:      "/tmp/some-repo",
		Worktrees: make(map[string]*config.Worktree),
	}

	manager := NewManager(cfg)
	recovered := manager.RecoverInterruptedStates()

	assert.Equal(t, 0, recovered)
}
