package store

import (
	"sync"
	"testing"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore() *Store {
	cfg := config.NewConfig()
	// CRITICAL: WithDisableSave() prevents tests from overwriting real config file
	return New(cfg, WithDebounceTime(10*time.Millisecond), WithDisableSave())
}

func TestNew(t *testing.T) {
	cfg := config.NewConfig()
	s := New(cfg, WithDisableSave()) // Prevent saving to real config
	defer func() { _, _ = s.Close() }()

	assert.NotNil(t, s)
	assert.NotNil(t, s.config)
	assert.False(t, s.dirty)
}

func TestStore_AddAndGetProject(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	err := s.AddProject("test", project)
	require.NoError(t, err)

	// Get returns a copy
	got, ok := s.GetProject("test")
	assert.True(t, ok)
	assert.Equal(t, "/test/path", got.Path)

	// Modifying the copy doesn't affect the store
	got.Path = "/modified"
	got2, _ := s.GetProject("test")
	assert.Equal(t, "/test/path", got2.Path)
}

func TestStore_AddProject_Duplicate(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	err := s.AddProject("test", project)
	require.NoError(t, err)

	err = s.AddProject("test", project)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestStore_RemoveProject(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)

	err := s.RemoveProject("test")
	require.NoError(t, err)

	_, ok := s.GetProject("test")
	assert.False(t, ok)
}

func TestStore_AddAndGetWorktree(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)

	wt := config.NewWorktree("/test/wt", "main", false, []int{3100})
	err := s.AddWorktree("test", "tokyo", wt)
	require.NoError(t, err)

	got, ok := s.GetWorktree("test", "tokyo")
	assert.True(t, ok)
	assert.Equal(t, "/test/wt", got.Path)
	assert.Equal(t, "main", got.Branch)
}

func TestStore_SetWorktreeStatus(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)
	wt := config.NewWorktree("/test/wt", "main", false, []int{3100})
	_ = s.AddWorktree("test", "tokyo", wt)

	err := s.SetWorktreeStatus("test", "tokyo", config.SetupStatusRunning)
	require.NoError(t, err)

	status, ok := s.GetWorktreeStatus("test", "tokyo")
	assert.True(t, ok)
	assert.Equal(t, config.SetupStatusRunning, status)
}

func TestStore_SetTunnelState(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)
	wt := config.NewWorktree("/test/wt", "main", false, []int{3100})
	_ = s.AddWorktree("test", "tokyo", wt)

	tunnelState := &config.TunnelState{
		Active: true,
		URL:    "https://test.trycloudflare.com",
		Port:   3100,
	}
	err := s.SetTunnelState("test", "tokyo", tunnelState)
	require.NoError(t, err)

	got, ok := s.GetTunnelState("test", "tokyo")
	assert.True(t, ok)
	assert.True(t, got.Active)
	assert.Equal(t, "https://test.trycloudflare.com", got.URL)

	// Modifying the returned copy doesn't affect the store
	got.URL = "modified"
	got2, _ := s.GetTunnelState("test", "tokyo")
	assert.Equal(t, "https://test.trycloudflare.com", got2.URL)
}

func TestStore_ClearTunnelState(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)
	wt := config.NewWorktree("/test/wt", "main", false, []int{3100})
	_ = s.AddWorktree("test", "tokyo", wt)

	_ = s.SetTunnelState("test", "tokyo", &config.TunnelState{Active: true})
	err := s.ClearTunnelState("test", "tokyo")
	require.NoError(t, err)

	got, ok := s.GetTunnelState("test", "tokyo")
	assert.True(t, ok)
	assert.Nil(t, got)
}

func TestStore_ArchiveWorktree(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)
	wt := config.NewWorktree("/test/wt", "main", false, []int{3100})
	_ = s.AddWorktree("test", "tokyo", wt)

	err := s.ArchiveWorktree("test", "tokyo")
	require.NoError(t, err)

	assert.True(t, s.IsWorktreeArchived("test", "tokyo"))
}

func TestStore_RecoverInterruptedWorktrees(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)

	// Add worktrees in various states
	wt1 := config.NewWorktree("/test/wt1", "branch1", false, []int{3100})
	wt1.SetupStatus = config.SetupStatusCreating
	_ = s.AddWorktree("test", "tokyo", wt1)

	wt2 := config.NewWorktree("/test/wt2", "branch2", false, []int{3101})
	wt2.SetupStatus = config.SetupStatusRunning
	_ = s.AddWorktree("test", "paris", wt2)

	wt3 := config.NewWorktree("/test/wt3", "branch3", false, []int{3102})
	wt3.SetupStatus = config.SetupStatusDone
	_ = s.AddWorktree("test", "london", wt3)

	recovered := s.RecoverInterruptedWorktrees()
	assert.Equal(t, 2, recovered)

	status1, _ := s.GetWorktreeStatus("test", "tokyo")
	assert.Equal(t, config.SetupStatusFailed, status1)

	status2, _ := s.GetWorktreeStatus("test", "paris")
	assert.Equal(t, config.SetupStatusFailed, status2)

	status3, _ := s.GetWorktreeStatus("test", "london")
	assert.Equal(t, config.SetupStatusDone, status3)
}

func TestStore_CleanupStaleTunnels(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)

	wt1 := config.NewWorktree("/test/wt1", "branch1", false, []int{3100})
	_ = s.AddWorktree("test", "tokyo", wt1)
	_ = s.SetTunnelState("test", "tokyo", &config.TunnelState{Active: true, URL: "url1"})

	wt2 := config.NewWorktree("/test/wt2", "branch2", false, []int{3101})
	_ = s.AddWorktree("test", "paris", wt2)
	_ = s.SetTunnelState("test", "paris", &config.TunnelState{Active: true, URL: "url2"})

	// Only tokyo is active
	activeTunnels := map[string]bool{"test/tokyo": true}
	cleaned := s.CleanupStaleTunnels(activeTunnels)

	assert.Equal(t, 1, cleaned)

	// Tokyo should still have tunnel
	state1, _ := s.GetTunnelState("test", "tokyo")
	assert.NotNil(t, state1)
	assert.True(t, state1.Active)

	// Paris should be cleared
	state2, _ := s.GetTunnelState("test", "paris")
	assert.Nil(t, state2)
}

func TestStore_DirtyFlag(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	assert.False(t, s.HasPendingSaves())

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)

	assert.True(t, s.HasPendingSaves())
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test/path", 2)
	_ = s.AddProject("test", project)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.GetProject("test")
		}()
		go func() {
			defer wg.Done()
			_ = s.SetGitHubConfig("test", "owner", "repo")
		}()
	}
	wg.Wait()
}

func TestStore_BatchMutate(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	err := s.BatchMutate(func(cfg *config.Config) error {
		cfg.Projects["test"] = config.NewProject("/test/path", 2)
		cfg.Projects["test"].Worktrees["tokyo"] = config.NewWorktree("/test/wt", "main", false, []int{3100})
		return nil
	})
	require.NoError(t, err)

	got, ok := s.GetWorktree("test", "tokyo")
	assert.True(t, ok)
	assert.Equal(t, "main", got.Branch)
}

func TestStore_ListProjects(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	_ = s.AddProject("alpha", config.NewProject("/alpha", 1))
	_ = s.AddProject("beta", config.NewProject("/beta", 1))
	_ = s.AddProject("gamma", config.NewProject("/gamma", 1))

	projects := s.ListProjects()
	assert.Len(t, projects, 3)
	assert.Contains(t, projects, "alpha")
	assert.Contains(t, projects, "beta")
	assert.Contains(t, projects, "gamma")
}

func TestStore_ListWorktrees(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test", 1)
	_ = s.AddProject("test", project)
	_ = s.AddWorktree("test", "tokyo", config.NewWorktree("/wt1", "main", false, nil))
	_ = s.AddWorktree("test", "paris", config.NewWorktree("/wt2", "dev", false, nil))

	worktrees := s.ListWorktrees("test")
	assert.Len(t, worktrees, 2)
	assert.Contains(t, worktrees, "tokyo")
	assert.Contains(t, worktrees, "paris")
}

func TestStore_GetConfigSnapshot(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test", 1)
	_ = s.AddProject("test", project)
	_ = s.AddWorktree("test", "tokyo", config.NewWorktree("/wt", "main", false, []int{3100}))

	snapshot := s.GetConfigSnapshot()
	assert.NotNil(t, snapshot)
	assert.Contains(t, snapshot.Projects, "test")
	assert.Contains(t, snapshot.Projects["test"].Worktrees, "tokyo")

	// Modifying snapshot doesn't affect store
	snapshot.Projects["test"].Path = "/modified"
	got, _ := s.GetProject("test")
	assert.Equal(t, "/test", got.Path)
}

func TestStore_UpdateSettings(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	now := time.Now()
	s.SetLastUpdateCheck(now)
	s.SetLastVersion("1.2.3")

	settings := s.GetUpdateSettings()
	assert.Equal(t, "1.2.3", settings.LastVersion)
	assert.True(t, settings.LastCheck.Equal(now))
}

func TestStore_SetWorktreePRs(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()

	project := config.NewProject("/test", 1)
	_ = s.AddProject("test", project)
	_ = s.AddWorktree("test", "tokyo", config.NewWorktree("/wt", "main", false, nil))

	prs := []config.PRInfo{
		{Number: 1, Title: "First PR"},
		{Number: 2, Title: "Second PR"},
	}
	err := s.SetWorktreePRs("test", "tokyo", prs)
	require.NoError(t, err)

	got := s.GetWorktreePRs("test", "tokyo")
	assert.Len(t, got, 2)
	assert.Equal(t, "First PR", got[0].Title)
}
