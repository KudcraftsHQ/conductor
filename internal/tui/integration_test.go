package tui_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/testutil"
	"github.com/hammashamzah/conductor/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorktreeLifecycle_CreateArchiveDeleteRecreate tests the full worktree lifecycle:
// 1. Create a worktree from a branch (simulating PR workflow)
// 2. Archive the worktree
// 3. Delete the archived worktree
// 4. Recreate a worktree from the same branch
//
// This tests the business logic layer (workspace.Manager + Store) that the TUI uses.
func TestWorktreeLifecycle_CreateArchiveDeleteRecreate(t *testing.T) {
	// Skip in short mode as this requires network access
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup isolated test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Initialize config
	env.InitConfig(t)

	// Clone test repo
	repoPath := env.CloneTestRepo(t)

	// Create a test branch in the repo
	testBranch := "feature/test-pr"
	env.CreateTestBranch(t, testBranch)

	// Add project to config
	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 2)

	// Load config and create store + manager
	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)

	// Initialize the global setup manager with the store
	workspace.InitSetupManager(s)

	// === STEP 1: Create worktree ===
	t.Log("Step 1: Creating worktree from branch")

	worktreeName, worktree, err := mgr.CreateWorktree(projectName, testBranch, 2)
	require.NoError(t, err, "CreateWorktree should succeed")
	require.NotEmpty(t, worktreeName, "worktree name should not be empty")

	// Verify worktree entry exists
	gotWorktree, ok := s.GetWorktree(projectName, worktreeName)
	require.True(t, ok, "worktree should exist in store")
	assert.Equal(t, testBranch, gotWorktree.Branch, "branch should match")
	assert.Equal(t, config.SetupStatusDone, gotWorktree.SetupStatus, "status should be done")
	assert.Len(t, gotWorktree.Ports, 2, "should have 2 ports allocated")
	assert.False(t, gotWorktree.Archived, "should not be archived")

	// Verify ports are allocated
	allocatedPorts := gotWorktree.Ports
	assert.NotEmpty(t, allocatedPorts, "ports should be allocated")

	// Verify git worktree exists on disk
	assert.True(t, workspace.WorktreeExists(worktree.Path), "git worktree should exist on disk")

	firstWorktreeName := worktreeName
	t.Logf("Created worktree: %s with ports %v", worktreeName, allocatedPorts)

	// === STEP 2: Archive worktree ===
	t.Log("Step 2: Archiving worktree")

	err = mgr.ArchiveWorktree(projectName, worktreeName)
	require.NoError(t, err, "ArchiveWorktree should succeed")

	// Wait for store to persist
	time.Sleep(50 * time.Millisecond)

	// Verify archived state
	assert.True(t, s.IsWorktreeArchived(projectName, worktreeName), "worktree should be archived")

	archivedWorktree, ok := s.GetWorktree(projectName, worktreeName)
	require.True(t, ok, "archived worktree should still exist in store")
	assert.True(t, archivedWorktree.Archived, "Archived flag should be true")
	assert.False(t, archivedWorktree.ArchivedAt.IsZero(), "ArchivedAt should be set")

	// Verify ports are freed from global allocation (not just worktree.Ports slice)
	cfgSnapshot := s.GetConfigSnapshot()
	for _, port := range allocatedPorts {
		portStr := fmt.Sprintf("%d", port)
		_, allocated := cfgSnapshot.PortAllocations[portStr]
		assert.False(t, allocated, "port %d should be freed from global allocation", port)
	}

	// Verify git worktree is removed from disk
	assert.False(t, workspace.WorktreeExists(worktree.Path), "git worktree should be removed from disk")

	t.Logf("Archived worktree: %s", worktreeName)

	// === STEP 3: Delete worktree ===
	t.Log("Step 3: Deleting archived worktree")

	err = mgr.DeleteWorktree(projectName, worktreeName)
	require.NoError(t, err, "DeleteWorktree should succeed")

	// Wait for store to persist
	time.Sleep(50 * time.Millisecond)

	// Verify worktree is completely removed from config
	_, ok = s.GetWorktree(projectName, worktreeName)
	assert.False(t, ok, "worktree should be removed from store")

	t.Logf("Deleted worktree: %s", worktreeName)

	// === STEP 4: Recreate worktree from same branch ===
	t.Log("Step 4: Recreating worktree from same branch")

	newWorktreeName, newWorktree, err := mgr.CreateWorktree(projectName, testBranch, 2)
	require.NoError(t, err, "CreateWorktree should succeed for same branch")
	require.NotEmpty(t, newWorktreeName, "new worktree name should not be empty")

	// Verify it's a different worktree name (city names are random)
	// Note: There's a small chance they could be the same, but very unlikely
	t.Logf("New worktree name: %s (original was: %s)", newWorktreeName, firstWorktreeName)

	// Verify new worktree entry exists
	gotNewWorktree, ok := s.GetWorktree(projectName, newWorktreeName)
	require.True(t, ok, "new worktree should exist in store")
	assert.Equal(t, testBranch, gotNewWorktree.Branch, "branch should match original")
	assert.Equal(t, config.SetupStatusDone, gotNewWorktree.SetupStatus, "status should be done")
	assert.Len(t, gotNewWorktree.Ports, 2, "should have 2 ports allocated")
	assert.False(t, gotNewWorktree.Archived, "should not be archived")

	// Verify git worktree exists on disk
	assert.True(t, workspace.WorktreeExists(newWorktree.Path), "new git worktree should exist on disk")

	// Verify the old worktree is still gone
	_, ok = s.GetWorktree(projectName, firstWorktreeName)
	assert.False(t, ok, "original worktree should still be gone")

	t.Logf("Created new worktree: %s with branch %s", newWorktreeName, testBranch)

	// === Cleanup: Archive and delete the new worktree ===
	t.Log("Cleanup: Archiving and deleting new worktree")

	err = mgr.ArchiveWorktree(projectName, newWorktreeName)
	require.NoError(t, err, "cleanup: ArchiveWorktree should succeed")

	err = mgr.DeleteWorktree(projectName, newWorktreeName)
	require.NoError(t, err, "cleanup: DeleteWorktree should succeed")

	t.Log("Test completed successfully!")
}

// TestWorktreeLifecycle_CannotDeleteNonArchived verifies that non-archived worktrees cannot be deleted.
func TestWorktreeLifecycle_CannotDeleteNonArchived(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	env.InitConfig(t)
	repoPath := env.CloneTestRepo(t)

	testBranch := "feature/delete-test"
	env.CreateTestBranch(t, testBranch)

	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 1)

	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)
	workspace.InitSetupManager(s)

	// Create worktree
	worktreeName, worktree, err := mgr.CreateWorktree(projectName, testBranch, 1)
	require.NoError(t, err)

	// Try to delete without archiving - should fail
	err = mgr.DeleteWorktree(projectName, worktreeName)
	assert.Error(t, err, "should not be able to delete non-archived worktree")
	assert.Contains(t, err.Error(), "must be archived")

	// Verify worktree still exists
	_, ok := s.GetWorktree(projectName, worktreeName)
	assert.True(t, ok, "worktree should still exist")

	// Cleanup
	_ = mgr.ArchiveWorktree(projectName, worktreeName)
	_ = mgr.DeleteWorktree(projectName, worktreeName)

	// Verify git worktree is cleaned up
	assert.False(t, workspace.WorktreeExists(worktree.Path), "git worktree should be removed after cleanup")
}

// TestWorktreeLifecycle_CannotArchiveRoot verifies that root worktrees cannot be archived.
func TestWorktreeLifecycle_CannotArchiveRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	env.InitConfig(t)
	repoPath := env.CloneTestRepo(t)

	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 1)

	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)

	// Try to archive root worktree - should fail
	err := mgr.ArchiveWorktree(projectName, "root")
	assert.Error(t, err, "should not be able to archive root worktree")
	assert.Contains(t, err.Error(), "cannot archive root")
}

// TestConfigIsolation verifies that tests don't affect the real config.
func TestConfigIsolation(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Verify config dir is in temp directory
	configDir, err := config.ConductorDir()
	require.NoError(t, err)
	assert.Equal(t, env.ConfigDir, configDir, "config dir should be overridden to temp dir")

	// Initialize and add a project
	env.InitConfig(t)
	env.AddProject(t, "isolation-test", "/tmp/fake-repo", 1)

	// Verify it's in our temp config
	cfg := env.LoadConfig(t)
	_, ok := cfg.Projects["isolation-test"]
	assert.True(t, ok, "project should exist in test config")
}

// TestPRWorkflow_CreateArchiveDeleteRefreshRecreate tests the full PR-based workflow:
// 1. Create a worktree from a branch (simulating creating from PR)
// 2. Archive the worktree
// 3. Delete the archived worktree
// 4. Simulate PR refresh which should auto-create a new worktree for the same PR branch
// 5. Verify the new worktree is properly set up
//
// This tests the PR workflow that would be triggered by the TUI's PR list refresh.
func TestPRWorkflow_CreateArchiveDeleteRefreshRecreate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup isolated test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Initialize config
	env.InitConfig(t)

	// Clone test repo
	repoPath := env.CloneTestRepo(t)

	// Create a setup script that creates a marker file
	setupScript := `#!/bin/bash
set -e
echo "Setup running for $CONDUCTOR_WORKTREE_PATH"
echo "setup-complete" > "$CONDUCTOR_WORKTREE_PATH/.setup-marker"
echo "Branch: $(git branch --show-current)"
echo "Setup complete!"
`
	env.CreateSetupScript(t, setupScript)

	// Create a test branch with some changes (simulating a PR branch)
	// Use a unique branch name to avoid conflicts between test runs
	testBranch := fmt.Sprintf("claude/test-pr-workflow-%d", time.Now().UnixNano())
	env.CreateBranchWithChanges(t, testBranch, "test-file.txt", "Test content for PR workflow")

	// Push the branch to remote (simulating a real PR that exists on GitHub)
	env.PushBranch(t, testBranch)

	// Add project to config
	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 2)

	// Set GitHub config for the project
	env.SetProjectGitHubConfig(t, projectName, "KudcraftsHQ", "conductor-test-repo")

	// Load config and create store + manager
	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)

	// Initialize the global setup manager with the store
	workspace.InitSetupManager(s)

	// === STEP 1: Create worktree from branch (simulating PR creation) ===
	t.Log("Step 1: Creating worktree from PR branch")

	// Simulate what CreateWorktreeFromPR does
	prInfo := config.PRInfo{
		Number:     1,
		Title:      "Test PR",
		HeadBranch: testBranch,
		State:      "open",
	}

	// First check there's no existing worktree for this branch
	existingWt := findWorktreeByBranch(t, s, projectName, prInfo.HeadBranch)
	assert.Nil(t, existingWt, "should not have existing worktree for this branch")

	// Create the worktree (this creates the git worktree but doesn't run setup)
	worktreeName, worktree, err := mgr.CreateWorktree(projectName, testBranch, 2)
	require.NoError(t, err, "CreateWorktree should succeed")
	require.NotEmpty(t, worktreeName, "worktree name should not be empty")

	// Verify worktree entry exists
	gotWorktree, ok := s.GetWorktree(projectName, worktreeName)
	require.True(t, ok, "worktree should exist in store")
	assert.Equal(t, testBranch, gotWorktree.Branch, "branch should match")
	assert.Len(t, gotWorktree.Ports, 2, "should have 2 ports allocated")
	assert.False(t, gotWorktree.Archived, "should not be archived")

	// Verify git worktree exists on disk
	assert.True(t, workspace.WorktreeExists(worktree.Path), "git worktree should exist on disk")

	// Now run the setup script and wait for it to complete
	setupDone := make(chan struct{})
	var setupSuccess bool
	var setupErr error

	project, _ := s.GetProject(projectName)
	workspace.GetSetupManager().RunSetupAsync(project, projectName, worktreeName, worktree, func(success bool, err error) {
		setupSuccess = success
		setupErr = err
		close(setupDone)
	})

	// Wait for setup to complete (with timeout)
	select {
	case <-setupDone:
		require.True(t, setupSuccess, "setup should succeed: %v", setupErr)
	case <-time.After(30 * time.Second):
		t.Fatal("setup timed out")
	}

	// Re-check status after setup
	gotWorktree, ok = s.GetWorktree(projectName, worktreeName)
	require.True(t, ok, "worktree should still exist in store after setup")
	assert.Equal(t, config.SetupStatusDone, gotWorktree.SetupStatus, "status should be done after setup")

	// Verify setup script ran (marker file exists)
	markerPath := fmt.Sprintf("%s/.setup-marker", worktree.Path)
	markerContent, err := os.ReadFile(markerPath)
	require.NoError(t, err, "setup marker file should exist")
	assert.Equal(t, "setup-complete\n", string(markerContent), "setup script should have created marker")

	// Verify the worktree is on the correct branch
	currentBranch := getGitBranch(t, worktree.Path)
	assert.Equal(t, testBranch, currentBranch, "worktree should be on correct branch")

	// Verify test file from branch exists in worktree
	testFilePath := fmt.Sprintf("%s/test-file.txt", worktree.Path)
	testFileContent, err := os.ReadFile(testFilePath)
	require.NoError(t, err, "test file from branch should exist in worktree")
	assert.Equal(t, "Test content for PR workflow", string(testFileContent), "test file content should match")

	firstWorktreeName := worktreeName
	t.Logf("Created worktree: %s with branch %s", worktreeName, testBranch)

	// === STEP 2: Archive worktree ===
	t.Log("Step 2: Archiving worktree")

	err = mgr.ArchiveWorktree(projectName, worktreeName)
	require.NoError(t, err, "ArchiveWorktree should succeed")

	// Wait for store to persist
	time.Sleep(50 * time.Millisecond)

	// Verify archived state
	assert.True(t, s.IsWorktreeArchived(projectName, worktreeName), "worktree should be archived")
	assert.False(t, workspace.WorktreeExists(worktree.Path), "git worktree should be removed from disk")

	t.Logf("Archived worktree: %s", worktreeName)

	// === STEP 3: Delete worktree ===
	t.Log("Step 3: Deleting archived worktree")

	err = mgr.DeleteWorktree(projectName, worktreeName)
	require.NoError(t, err, "DeleteWorktree should succeed")

	// Wait for store to persist
	time.Sleep(50 * time.Millisecond)

	// Verify worktree is completely removed from config
	_, ok = s.GetWorktree(projectName, worktreeName)
	assert.False(t, ok, "worktree should be removed from store")

	// Verify no worktree exists for this branch now
	existingWt = findWorktreeByBranch(t, s, projectName, testBranch)
	assert.Nil(t, existingWt, "should not have worktree for this branch after deletion")

	t.Logf("Deleted worktree: %s", worktreeName)

	// === STEP 4: Simulate PR refresh - create new worktree from same branch ===
	t.Log("Step 4: Simulating PR refresh - creating new worktree from same PR branch")

	// This simulates what happens when:
	// - User is in PR list view
	// - They select a PR and press 'w' to create worktree
	// - Or auto-setup creates it for claude/* branches
	//
	// The archive process deleted the local branch, but the branch still exists on remote.
	// CreateWorktree should fetch from remote and create the worktree.

	// For claude/* branches, AutoSetupClaudePRs would find this PR and create worktree
	// Since we're testing the workflow, we'll call CreateWorktree directly
	newWorktreeName, newWorktree, err := mgr.CreateWorktree(projectName, testBranch, 2)
	require.NoError(t, err, "CreateWorktree should succeed for same branch after deletion")
	require.NotEmpty(t, newWorktreeName, "new worktree name should not be empty")

	t.Logf("New worktree name: %s (original was: %s)", newWorktreeName, firstWorktreeName)

	// Run setup for the new worktree
	setupDone2 := make(chan struct{})
	var setupSuccess2 bool
	var setupErr2 error

	workspace.GetSetupManager().RunSetupAsync(project, projectName, newWorktreeName, newWorktree, func(success bool, err error) {
		setupSuccess2 = success
		setupErr2 = err
		close(setupDone2)
	})

	// Wait for setup to complete (with timeout)
	select {
	case <-setupDone2:
		require.True(t, setupSuccess2, "setup should succeed for new worktree: %v", setupErr2)
	case <-time.After(30 * time.Second):
		t.Fatal("setup timed out for new worktree")
	}

	// === STEP 5: Verify new worktree is properly set up ===
	t.Log("Step 5: Verifying new worktree setup")

	// Verify new worktree entry exists with correct state
	gotNewWorktree, ok := s.GetWorktree(projectName, newWorktreeName)
	require.True(t, ok, "new worktree should exist in store")
	assert.Equal(t, testBranch, gotNewWorktree.Branch, "branch should match original PR branch")
	assert.Equal(t, config.SetupStatusDone, gotNewWorktree.SetupStatus, "setup status should be done")
	assert.Len(t, gotNewWorktree.Ports, 2, "should have 2 ports allocated")
	assert.False(t, gotNewWorktree.Archived, "should not be archived")

	// Verify git worktree exists on disk
	assert.True(t, workspace.WorktreeExists(newWorktree.Path), "new git worktree should exist on disk")

	// Verify setup script ran successfully (marker file exists)
	newMarkerPath := fmt.Sprintf("%s/.setup-marker", newWorktree.Path)
	newMarkerContent, err := os.ReadFile(newMarkerPath)
	require.NoError(t, err, "setup marker file should exist in new worktree")
	assert.Equal(t, "setup-complete\n", string(newMarkerContent), "setup script should have run for new worktree")

	// Verify the new worktree is on the correct branch
	newCurrentBranch := getGitBranch(t, newWorktree.Path)
	assert.Equal(t, testBranch, newCurrentBranch, "new worktree should be on correct branch")

	// Verify test file from branch exists in new worktree
	newTestFilePath := fmt.Sprintf("%s/test-file.txt", newWorktree.Path)
	newTestFileContent, err := os.ReadFile(newTestFilePath)
	require.NoError(t, err, "test file from branch should exist in new worktree")
	assert.Equal(t, "Test content for PR workflow", string(newTestFileContent), "test file content should match in new worktree")

	// Verify the old worktree is still gone
	_, ok = s.GetWorktree(projectName, firstWorktreeName)
	assert.False(t, ok, "original worktree should still be gone")

	// Verify we can find the new worktree by branch
	foundWt := findWorktreeByBranch(t, s, projectName, testBranch)
	require.NotNil(t, foundWt, "should find worktree by branch")
	assert.Equal(t, newWorktree.Path, foundWt.Path, "found worktree should be the new one")

	t.Logf("New worktree %s is fully set up with branch %s", newWorktreeName, testBranch)

	// === Cleanup ===
	t.Log("Cleanup: Archiving and deleting new worktree")

	err = mgr.ArchiveWorktree(projectName, newWorktreeName)
	require.NoError(t, err, "cleanup: ArchiveWorktree should succeed")

	err = mgr.DeleteWorktree(projectName, newWorktreeName)
	require.NoError(t, err, "cleanup: DeleteWorktree should succeed")

	// Clean up remote branch
	env.DeleteRemoteBranch(t, testBranch)

	t.Log("PR workflow test completed successfully!")
}

// TestScripts_FileBasedSetupAndArchive tests setup and archive scripts using .conductor-scripts/ files.
func TestScripts_FileBasedSetupAndArchive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	env.InitConfig(t)
	repoPath := env.CloneTestRepo(t)

	// Create setup script that creates a marker file
	setupScript := `#!/bin/bash
set -e
echo "setup-marker-content" > "$CONDUCTOR_WORKTREE_PATH/.setup-marker"
echo "Setup script executed for $CONDUCTOR_PROJECT_NAME / $CONDUCTOR_WORKTREE_PATH"
`
	env.CreateSetupScript(t, setupScript)

	// Create archive script that creates a marker file in the main repo
	// (since worktree directory will be deleted after archive)
	archiveScript := `#!/bin/bash
set -e
echo "archive-marker-content" > "$CONDUCTOR_WORKTREE_PATH/.archive-marker"
echo "Archive script executed for $CONDUCTOR_PROJECT_NAME"
`
	env.CreateArchiveScript(t, archiveScript)

	testBranch := "feature/script-test"
	env.CreateTestBranch(t, testBranch)

	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 1)

	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)
	workspace.InitSetupManager(s)

	// Create worktree
	t.Log("Creating worktree...")
	worktreeName, worktree, err := mgr.CreateWorktree(projectName, testBranch, 1)
	require.NoError(t, err)

	// Run setup script
	t.Log("Running setup script...")
	setupDone := make(chan struct{})
	var setupSuccess bool
	project, _ := s.GetProject(projectName)
	workspace.GetSetupManager().RunSetupAsync(project, projectName, worktreeName, worktree, func(success bool, err error) {
		setupSuccess = success
		close(setupDone)
	})

	select {
	case <-setupDone:
		require.True(t, setupSuccess, "setup script should succeed")
	case <-time.After(30 * time.Second):
		t.Fatal("setup script timed out")
	}

	// Verify setup marker exists
	setupMarkerPath := fmt.Sprintf("%s/.setup-marker", worktree.Path)
	setupMarkerContent, err := os.ReadFile(setupMarkerPath)
	require.NoError(t, err, "setup marker should exist")
	assert.Equal(t, "setup-marker-content\n", string(setupMarkerContent))
	t.Log("Setup script completed successfully - marker file created")

	// Archive worktree (this runs archive script)
	t.Log("Archiving worktree (runs archive script)...")
	err = mgr.ArchiveWorktree(projectName, worktreeName)
	require.NoError(t, err)

	// Check archive logs to verify script ran
	archiveLogs := workspace.GetSetupManager().GetArchiveLogs(projectName, worktreeName)
	assert.Contains(t, archiveLogs, "Archive script executed", "archive script should have run")
	t.Log("Archive script completed successfully")

	// Cleanup
	_ = mgr.DeleteWorktree(projectName, worktreeName)

	t.Log("File-based scripts test completed!")
}

// TestScripts_InlineSetupAndArchive tests setup and archive scripts using conductor.json inline scripts.
func TestScripts_InlineSetupAndArchive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	env.InitConfig(t)
	repoPath := env.CloneTestRepo(t)

	// Create conductor.json with inline scripts
	scripts := map[string]string{
		"setup":   `echo "inline-setup-marker" > "$CONDUCTOR_WORKTREE_PATH/.inline-setup-marker"`,
		"archive": `echo "inline-archive-marker" > "$CONDUCTOR_WORKTREE_PATH/.inline-archive-marker"`,
		"run":     `echo "Run script executed with PORT=$PORT"`,
	}
	env.CreateProjectConfig(t, scripts)

	testBranch := "feature/inline-script-test"
	env.CreateTestBranch(t, testBranch)

	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 1)

	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)
	workspace.InitSetupManager(s)

	// Create worktree
	t.Log("Creating worktree...")
	worktreeName, worktree, err := mgr.CreateWorktree(projectName, testBranch, 1)
	require.NoError(t, err)

	// Run setup script (inline)
	t.Log("Running inline setup script...")
	setupDone := make(chan struct{})
	var setupSuccess bool
	project, _ := s.GetProject(projectName)
	workspace.GetSetupManager().RunSetupAsync(project, projectName, worktreeName, worktree, func(success bool, err error) {
		setupSuccess = success
		close(setupDone)
	})

	select {
	case <-setupDone:
		require.True(t, setupSuccess, "inline setup script should succeed")
	case <-time.After(30 * time.Second):
		t.Fatal("inline setup script timed out")
	}

	// Verify inline setup marker exists
	inlineSetupMarkerPath := fmt.Sprintf("%s/.inline-setup-marker", worktree.Path)
	inlineSetupMarkerContent, err := os.ReadFile(inlineSetupMarkerPath)
	require.NoError(t, err, "inline setup marker should exist")
	assert.Equal(t, "inline-setup-marker\n", string(inlineSetupMarkerContent))
	t.Log("Inline setup script completed successfully - marker file created")

	// Archive worktree (this runs inline archive script)
	t.Log("Archiving worktree (runs inline archive script)...")
	err = mgr.ArchiveWorktree(projectName, worktreeName)
	require.NoError(t, err)

	// Check archive logs to verify inline script ran
	archiveLogs := workspace.GetSetupManager().GetArchiveLogs(projectName, worktreeName)
	assert.Contains(t, archiveLogs, "Archive", "inline archive script should have run")
	t.Log("Inline archive script completed successfully")

	// Cleanup
	_ = mgr.DeleteWorktree(projectName, worktreeName)

	t.Log("Inline scripts test completed!")
}

// TestScripts_EnvironmentVariables tests that scripts receive correct environment variables.
func TestScripts_EnvironmentVariables(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	env.InitConfig(t)
	repoPath := env.CloneTestRepo(t)

	// Create setup script that writes all env vars to a file
	setupScript := `#!/bin/bash
set -e
cat > "$CONDUCTOR_WORKTREE_PATH/.env-dump" << EOF
PROJECT_NAME=$CONDUCTOR_PROJECT_NAME
WORKTREE_PATH=$CONDUCTOR_WORKTREE_PATH
PORT=$PORT
CONDUCTOR_PORT=$CONDUCTOR_PORT
CONDUCTOR_PORT_0=$CONDUCTOR_PORT_0
CONDUCTOR_PORT_1=$CONDUCTOR_PORT_1
CONDUCTOR_PORTS=$CONDUCTOR_PORTS
EOF
`
	env.CreateSetupScript(t, setupScript)

	testBranch := "feature/env-test"
	env.CreateTestBranch(t, testBranch)

	projectName := "env-test-project"
	env.AddProject(t, projectName, repoPath, 2) // 2 ports

	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)
	workspace.InitSetupManager(s)

	// Create worktree
	worktreeName, worktree, err := mgr.CreateWorktree(projectName, testBranch, 2)
	require.NoError(t, err)

	// Run setup script
	setupDone := make(chan struct{})
	var setupSuccess bool
	project, _ := s.GetProject(projectName)
	workspace.GetSetupManager().RunSetupAsync(project, projectName, worktreeName, worktree, func(success bool, err error) {
		setupSuccess = success
		close(setupDone)
	})

	select {
	case <-setupDone:
		require.True(t, setupSuccess, "setup script should succeed")
	case <-time.After(30 * time.Second):
		t.Fatal("setup script timed out")
	}

	// Read and verify environment variables
	envDumpPath := fmt.Sprintf("%s/.env-dump", worktree.Path)
	envDumpContent, err := os.ReadFile(envDumpPath)
	require.NoError(t, err, "env dump file should exist")

	envContent := string(envDumpContent)
	t.Logf("Environment dump:\n%s", envContent)

	// Verify key environment variables
	assert.Contains(t, envContent, fmt.Sprintf("PROJECT_NAME=%s", projectName))
	assert.Contains(t, envContent, fmt.Sprintf("WORKTREE_PATH=%s", worktree.Path))
	assert.Contains(t, envContent, "PORT=") // Should have a port
	assert.Contains(t, envContent, "CONDUCTOR_PORT=")
	assert.Contains(t, envContent, "CONDUCTOR_PORT_0=")
	assert.Contains(t, envContent, "CONDUCTOR_PORT_1=")
	assert.Contains(t, envContent, "CONDUCTOR_PORTS=")

	// Verify ports are in correct range (3100-3999)
	for _, port := range worktree.Ports {
		assert.GreaterOrEqual(t, port, 3100, "port should be >= 3100")
		assert.LessOrEqual(t, port, 3999, "port should be <= 3999")
	}

	t.Logf("Allocated ports: %v", worktree.Ports)

	// Cleanup
	_ = mgr.ArchiveWorktree(projectName, worktreeName)
	_ = mgr.DeleteWorktree(projectName, worktreeName)

	t.Log("Environment variables test completed!")
}

// getGitBranch returns the current branch name of a git repository.
func getGitBranch(t *testing.T, repoPath string) string {
	t.Helper()

	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get git branch: %v", err)
	}

	return strings.TrimSpace(string(output))
}

// findWorktreeByBranch finds a non-archived worktree by its branch name.
// Returns nil if no worktree is found for the branch.
func findWorktreeByBranch(t *testing.T, s *store.Store, projectName, branch string) *config.Worktree {
	t.Helper()

	project, ok := s.GetProject(projectName)
	if !ok {
		return nil
	}

	for _, wt := range project.Worktrees {
		if wt.Branch == branch && !wt.Archived {
			return wt
		}
	}

	return nil
}

// TestWorktreeCreation_WithExistingWorktree tests creating a second worktree when one already exists.
// This simulates the scenario:
// 1. User has an open PR with an existing worktree
// 2. User wants to create another worktree for a different branch
// 3. Both worktrees should work properly and have correct status
func TestWorktreeCreation_WithExistingWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup isolated test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Initialize config
	env.InitConfig(t)

	// Clone test repo
	repoPath := env.CloneTestRepo(t)

	// Create a setup script that creates a marker file
	setupScript := `#!/bin/bash
set -e
echo "Setup running for $CONDUCTOR_WORKTREE_PATH"
echo "setup-complete-$(basename $CONDUCTOR_WORKTREE_PATH)" > "$CONDUCTOR_WORKTREE_PATH/.setup-marker"
echo "Branch: $(git branch --show-current)"
echo "Setup complete!"
`
	env.CreateSetupScript(t, setupScript)

	// Create two test branches simulating two open PRs
	branch1 := fmt.Sprintf("claude/test-pr-1-%d", time.Now().UnixNano())
	branch2 := fmt.Sprintf("claude/test-pr-2-%d", time.Now().UnixNano())

	env.CreateBranchWithChanges(t, branch1, "test-file-1.txt", "Content for PR 1")
	env.CreateBranchWithChanges(t, branch2, "test-file-2.txt", "Content for PR 2")

	// Push both branches to remote
	env.PushBranch(t, branch1)
	env.PushBranch(t, branch2)

	// Add project to config
	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 2)
	env.SetProjectGitHubConfig(t, projectName, "KudcraftsHQ", "conductor-test-repo")

	// Load config and create store + manager
	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)
	workspace.InitSetupManager(s)

	// === STEP 1: Create first worktree ===
	t.Log("Step 1: Creating first worktree from branch1")

	worktreeName1, worktree1, err := mgr.CreateWorktree(projectName, branch1, 2)
	require.NoError(t, err, "CreateWorktree for branch1 should succeed")
	require.NotEmpty(t, worktreeName1, "worktree1 name should not be empty")

	// Verify first worktree entry exists
	gotWorktree1, ok := s.GetWorktree(projectName, worktreeName1)
	require.True(t, ok, "worktree1 should exist in store")
	assert.Equal(t, branch1, gotWorktree1.Branch, "branch should match")
	assert.Equal(t, config.SetupStatusDone, gotWorktree1.SetupStatus, "status should be done")
	assert.Len(t, gotWorktree1.Ports, 2, "should have 2 ports allocated")
	assert.False(t, gotWorktree1.Archived, "should not be archived")

	// Verify git worktree exists on disk
	assert.True(t, workspace.WorktreeExists(worktree1.Path), "git worktree1 should exist on disk")

	// Verify the worktree is on the correct branch
	currentBranch1 := getGitBranch(t, worktree1.Path)
	assert.Equal(t, branch1, currentBranch1, "worktree1 should be on correct branch")

	// Run setup for worktree1
	setupDone1 := make(chan struct{})
	var setupSuccess1 bool
	var setupErr1 error

	project, _ := s.GetProject(projectName)
	workspace.GetSetupManager().RunSetupAsync(project, projectName, worktreeName1, worktree1, func(success bool, err error) {
		setupSuccess1 = success
		setupErr1 = err
		close(setupDone1)
	})

	select {
	case <-setupDone1:
		require.True(t, setupSuccess1, "setup for worktree1 should succeed: %v", setupErr1)
	case <-time.After(30 * time.Second):
		t.Fatal("setup for worktree1 timed out")
	}

	t.Logf("Created first worktree: %s with branch %s", worktreeName1, branch1)

	// === STEP 2: Create second worktree while first one exists ===
	t.Log("Step 2: Creating second worktree from branch2 (while first worktree exists)")

	worktreeName2, worktree2, err := mgr.CreateWorktree(projectName, branch2, 2)
	require.NoError(t, err, "CreateWorktree for branch2 should succeed")
	require.NotEmpty(t, worktreeName2, "worktree2 name should not be empty")

	// Verify second worktree entry exists
	gotWorktree2, ok := s.GetWorktree(projectName, worktreeName2)
	require.True(t, ok, "worktree2 should exist in store")
	assert.Equal(t, branch2, gotWorktree2.Branch, "branch should match")
	assert.Equal(t, config.SetupStatusDone, gotWorktree2.SetupStatus, "status should be done")
	assert.Len(t, gotWorktree2.Ports, 2, "should have 2 ports allocated")
	assert.False(t, gotWorktree2.Archived, "should not be archived")

	// Verify git worktree exists on disk
	assert.True(t, workspace.WorktreeExists(worktree2.Path), "git worktree2 should exist on disk")

	// Verify the worktree is on the correct branch
	currentBranch2 := getGitBranch(t, worktree2.Path)
	assert.Equal(t, branch2, currentBranch2, "worktree2 should be on correct branch")

	// Run setup for worktree2
	setupDone2 := make(chan struct{})
	var setupSuccess2 bool
	var setupErr2 error

	workspace.GetSetupManager().RunSetupAsync(project, projectName, worktreeName2, worktree2, func(success bool, err error) {
		setupSuccess2 = success
		setupErr2 = err
		close(setupDone2)
	})

	select {
	case <-setupDone2:
		require.True(t, setupSuccess2, "setup for worktree2 should succeed: %v", setupErr2)
	case <-time.After(30 * time.Second):
		t.Fatal("setup for worktree2 timed out")
	}

	t.Logf("Created second worktree: %s with branch %s", worktreeName2, branch2)

	// === STEP 3: Verify both worktrees are working correctly ===
	t.Log("Step 3: Verifying both worktrees are independent and working")

	// Verify both worktrees still exist and have correct status
	gotWorktree1, ok = s.GetWorktree(projectName, worktreeName1)
	require.True(t, ok, "worktree1 should still exist in store")
	assert.Equal(t, config.SetupStatusDone, gotWorktree1.SetupStatus, "worktree1 status should still be done")
	assert.False(t, gotWorktree1.Archived, "worktree1 should not be archived")

	gotWorktree2, ok = s.GetWorktree(projectName, worktreeName2)
	require.True(t, ok, "worktree2 should still exist in store")
	assert.Equal(t, config.SetupStatusDone, gotWorktree2.SetupStatus, "worktree2 status should still be done")
	assert.False(t, gotWorktree2.Archived, "worktree2 should not be archived")

	// Verify both directories exist
	assert.True(t, workspace.WorktreeExists(worktree1.Path), "worktree1 directory should exist")
	assert.True(t, workspace.WorktreeExists(worktree2.Path), "worktree2 directory should exist")

	// Verify branches are still correct
	assert.Equal(t, branch1, getGitBranch(t, worktree1.Path), "worktree1 branch unchanged")
	assert.Equal(t, branch2, getGitBranch(t, worktree2.Path), "worktree2 branch unchanged")

	// Verify setup markers exist
	marker1Path := fmt.Sprintf("%s/.setup-marker", worktree1.Path)
	marker1Content, err := os.ReadFile(marker1Path)
	require.NoError(t, err, "setup marker1 should exist")
	assert.Contains(t, string(marker1Content), "setup-complete", "marker1 content should be correct")

	marker2Path := fmt.Sprintf("%s/.setup-marker", worktree2.Path)
	marker2Content, err := os.ReadFile(marker2Path)
	require.NoError(t, err, "setup marker2 should exist")
	assert.Contains(t, string(marker2Content), "setup-complete", "marker2 content should be correct")

	// Verify each worktree has its own test file
	testFile1Path := fmt.Sprintf("%s/test-file-1.txt", worktree1.Path)
	testFile1Content, err := os.ReadFile(testFile1Path)
	require.NoError(t, err, "test file 1 should exist in worktree1")
	assert.Equal(t, "Content for PR 1", string(testFile1Content), "test file 1 content should match")

	testFile2Path := fmt.Sprintf("%s/test-file-2.txt", worktree2.Path)
	testFile2Content, err := os.ReadFile(testFile2Path)
	require.NoError(t, err, "test file 2 should exist in worktree2")
	assert.Equal(t, "Content for PR 2", string(testFile2Content), "test file 2 content should match")

	// Verify ports are different for each worktree
	assert.NotEqual(t, gotWorktree1.Ports, gotWorktree2.Ports, "worktrees should have different ports")

	t.Log("Both worktrees are working correctly!")

	// === Cleanup ===
	t.Log("Cleanup: Archiving and deleting both worktrees")

	err = mgr.ArchiveWorktree(projectName, worktreeName1)
	require.NoError(t, err, "cleanup: ArchiveWorktree1 should succeed")
	err = mgr.DeleteWorktree(projectName, worktreeName1)
	require.NoError(t, err, "cleanup: DeleteWorktree1 should succeed")

	err = mgr.ArchiveWorktree(projectName, worktreeName2)
	require.NoError(t, err, "cleanup: ArchiveWorktree2 should succeed")
	err = mgr.DeleteWorktree(projectName, worktreeName2)
	require.NoError(t, err, "cleanup: DeleteWorktree2 should succeed")

	// Clean up remote branches
	env.DeleteRemoteBranch(t, branch1)
	env.DeleteRemoteBranch(t, branch2)

	t.Log("TestWorktreeCreation_WithExistingWorktree completed successfully!")
}

// TestWorktreeCreation_FromPRWithExistingWorktree tests creating a worktree from a PR
// when another worktree already exists. This uses CreateWorktreeFromPR which goes through
// the WorktreeQueue - the actual code path used by the TUI.
func TestWorktreeCreation_FromPRWithExistingWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup isolated test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Initialize config
	env.InitConfig(t)

	// Clone test repo
	repoPath := env.CloneTestRepo(t)

	// Create a setup script
	setupScript := `#!/bin/bash
set -e
echo "Setup running for $CONDUCTOR_WORKTREE_PATH"
echo "setup-complete-$(basename $CONDUCTOR_WORKTREE_PATH)" > "$CONDUCTOR_WORKTREE_PATH/.setup-marker"
echo "Branch: $(git branch --show-current)"
echo "Setup complete!"
`
	env.CreateSetupScript(t, setupScript)

	// Create two test branches simulating two open PRs
	branch1 := fmt.Sprintf("claude/test-pr-from-pr-1-%d", time.Now().UnixNano())
	branch2 := fmt.Sprintf("claude/test-pr-from-pr-2-%d", time.Now().UnixNano())

	env.CreateBranchWithChanges(t, branch1, "test-file-1.txt", "Content for PR 1")
	env.CreateBranchWithChanges(t, branch2, "test-file-2.txt", "Content for PR 2")

	// Push both branches to remote
	env.PushBranch(t, branch1)
	env.PushBranch(t, branch2)

	// Add project to config
	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 2)
	env.SetProjectGitHubConfig(t, projectName, "KudcraftsHQ", "conductor-test-repo")

	// Load config and create store + manager
	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)
	workspace.InitSetupManager(s)

	// Simulate PRInfo objects
	pr1 := config.PRInfo{
		Number:     1,
		Title:      "Test PR 1",
		HeadBranch: branch1,
		State:      "open",
	}

	pr2 := config.PRInfo{
		Number:     2,
		Title:      "Test PR 2",
		HeadBranch: branch2,
		State:      "open",
	}

	// === STEP 1: Create first worktree using CreateWorktreeFromPR ===
	t.Log("Step 1: Creating first worktree from PR 1 using CreateWorktreeFromPR")

	worktreeName1, worktree1, err := mgr.CreateWorktreeFromPR(projectName, pr1)
	require.NoError(t, err, "CreateWorktreeFromPR for PR 1 should succeed")
	require.NotEmpty(t, worktreeName1, "worktree1 name should not be empty")

	t.Logf("Created worktree1: %s, status: %s", worktreeName1, worktree1.SetupStatus)

	// Wait for the worktree queue to process and setup to complete
	waitForWorktreeReady(t, s, projectName, worktreeName1, 60*time.Second)

	// Re-fetch worktree from store to get updated status
	gotWorktree1, ok := s.GetWorktree(projectName, worktreeName1)
	require.True(t, ok, "worktree1 should exist in store")
	t.Logf("Worktree1 final status: %s", gotWorktree1.SetupStatus)

	// Verify status is done or failed
	if gotWorktree1.SetupStatus == config.SetupStatusFailed {
		t.Logf("WARNING: Worktree1 setup failed. This is the bug we're looking for!")
	}
	assert.Equal(t, config.SetupStatusDone, gotWorktree1.SetupStatus, "worktree1 status should be done")

	// Verify git worktree exists on disk
	assert.True(t, workspace.WorktreeExists(gotWorktree1.Path), "git worktree1 should exist on disk")

	// Verify branch is correct
	currentBranch1 := getGitBranch(t, gotWorktree1.Path)
	assert.Equal(t, branch1, currentBranch1, "worktree1 should be on correct branch")

	t.Logf("First worktree %s created successfully with status %s", worktreeName1, gotWorktree1.SetupStatus)

	// === STEP 2: Create second worktree using CreateWorktreeFromPR while first exists ===
	t.Log("Step 2: Creating second worktree from PR 2 using CreateWorktreeFromPR (while first worktree exists)")

	worktreeName2, worktree2, err := mgr.CreateWorktreeFromPR(projectName, pr2)
	require.NoError(t, err, "CreateWorktreeFromPR for PR 2 should succeed")
	require.NotEmpty(t, worktreeName2, "worktree2 name should not be empty")

	t.Logf("Created worktree2: %s, initial status: %s", worktreeName2, worktree2.SetupStatus)

	// Wait for the worktree queue to process and setup to complete
	waitForWorktreeReady(t, s, projectName, worktreeName2, 60*time.Second)

	// Re-fetch worktree from store to get updated status
	gotWorktree2, ok := s.GetWorktree(projectName, worktreeName2)
	require.True(t, ok, "worktree2 should exist in store")
	t.Logf("Worktree2 final status: %s", gotWorktree2.SetupStatus)

	// Verify status is done or failed - this is the bug check
	if gotWorktree2.SetupStatus == config.SetupStatusFailed {
		t.Logf("BUG FOUND: Worktree2 status is FAILED when it should be DONE!")
		t.Logf("Worktree2 path: %s", gotWorktree2.Path)
		t.Logf("Worktree2 branch: %s", gotWorktree2.Branch)

		// Check if the directory exists
		if workspace.WorktreeExists(gotWorktree2.Path) {
			t.Logf("Directory exists: yes")
			// Check if branch is correct
			actualBranch := getGitBranch(t, gotWorktree2.Path)
			t.Logf("Actual branch: %s", actualBranch)
		} else {
			t.Logf("Directory exists: no")
		}
	}
	assert.Equal(t, config.SetupStatusDone, gotWorktree2.SetupStatus, "worktree2 status should be done, not failed")

	// Verify git worktree exists on disk
	assert.True(t, workspace.WorktreeExists(gotWorktree2.Path), "git worktree2 should exist on disk")

	// Verify branch is correct
	currentBranch2 := getGitBranch(t, gotWorktree2.Path)
	assert.Equal(t, branch2, currentBranch2, "worktree2 should be on correct branch")

	t.Logf("Second worktree %s created with status %s", worktreeName2, gotWorktree2.SetupStatus)

	// === STEP 3: Verify both worktrees are functional ===
	t.Log("Step 3: Verifying both worktrees are functional")

	// Both should have status done
	gotWorktree1, _ = s.GetWorktree(projectName, worktreeName1)
	gotWorktree2, _ = s.GetWorktree(projectName, worktreeName2)

	assert.Equal(t, config.SetupStatusDone, gotWorktree1.SetupStatus, "worktree1 final status should be done")
	assert.Equal(t, config.SetupStatusDone, gotWorktree2.SetupStatus, "worktree2 final status should be done")

	// Both should exist on disk
	assert.True(t, workspace.WorktreeExists(gotWorktree1.Path), "worktree1 should exist on disk")
	assert.True(t, workspace.WorktreeExists(gotWorktree2.Path), "worktree2 should exist on disk")

	// Both should have their respective test files
	testFile1 := fmt.Sprintf("%s/test-file-1.txt", gotWorktree1.Path)
	testFile1Content, err := os.ReadFile(testFile1)
	require.NoError(t, err, "test file 1 should exist")
	assert.Equal(t, "Content for PR 1", string(testFile1Content))

	testFile2 := fmt.Sprintf("%s/test-file-2.txt", gotWorktree2.Path)
	testFile2Content, err := os.ReadFile(testFile2)
	require.NoError(t, err, "test file 2 should exist")
	assert.Equal(t, "Content for PR 2", string(testFile2Content))

	t.Log("Both worktrees are functional!")

	// === Cleanup ===
	t.Log("Cleanup: Archiving and deleting both worktrees")

	err = mgr.ArchiveWorktree(projectName, worktreeName1)
	require.NoError(t, err, "cleanup: ArchiveWorktree1 should succeed")
	err = mgr.DeleteWorktree(projectName, worktreeName1)
	require.NoError(t, err, "cleanup: DeleteWorktree1 should succeed")

	err = mgr.ArchiveWorktree(projectName, worktreeName2)
	require.NoError(t, err, "cleanup: ArchiveWorktree2 should succeed")
	err = mgr.DeleteWorktree(projectName, worktreeName2)
	require.NoError(t, err, "cleanup: DeleteWorktree2 should succeed")

	// Clean up remote branches
	env.DeleteRemoteBranch(t, branch1)
	env.DeleteRemoteBranch(t, branch2)

	t.Log("TestWorktreeCreation_FromPRWithExistingWorktree completed successfully!")
}

// waitForWorktreeReady waits for a worktree to reach a terminal state (Done or Failed).
func waitForWorktreeReady(t *testing.T, s *store.Store, projectName, worktreeName string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		wt, ok := s.GetWorktree(projectName, worktreeName)
		if !ok {
			t.Fatalf("worktree %s not found in store", worktreeName)
		}

		switch wt.SetupStatus {
		case config.SetupStatusDone, config.SetupStatusFailed:
			t.Logf("Worktree %s reached terminal state: %s", worktreeName, wt.SetupStatus)
			return
		default:
			t.Logf("Worktree %s status: %s, waiting...", worktreeName, wt.SetupStatus)
			time.Sleep(500 * time.Millisecond)
		}
	}

	t.Fatalf("timeout waiting for worktree %s to be ready", worktreeName)
}

// TestWorktreeCreation_RemoteOnlyBranch tests creating a worktree when:
// - Branch only exists on remote (not locally)
// - Another worktree already exists
// This simulates the scenario where a user archives a worktree (which deletes the local branch),
// and then tries to create a new worktree for a different PR.
func TestWorktreeCreation_RemoteOnlyBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup isolated test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Initialize config
	env.InitConfig(t)

	// Clone test repo
	repoPath := env.CloneTestRepo(t)

	// Create a setup script
	setupScript := `#!/bin/bash
set -e
echo "Setup running for $CONDUCTOR_WORKTREE_PATH"
echo "setup-complete-$(basename $CONDUCTOR_WORKTREE_PATH)" > "$CONDUCTOR_WORKTREE_PATH/.setup-marker"
echo "Branch: $(git branch --show-current)"
echo "Setup complete!"
`
	env.CreateSetupScript(t, setupScript)

	// Create two test branches
	branch1 := fmt.Sprintf("claude/test-remote-only-1-%d", time.Now().UnixNano())
	branch2 := fmt.Sprintf("claude/test-remote-only-2-%d", time.Now().UnixNano())

	env.CreateBranchWithChanges(t, branch1, "test-file-1.txt", "Content for PR 1")
	env.CreateBranchWithChanges(t, branch2, "test-file-2.txt", "Content for PR 2")

	// Push both branches to remote
	env.PushBranch(t, branch1)
	env.PushBranch(t, branch2)

	// Delete local branches to simulate remote-only scenario
	// (like when a user archives a worktree which deletes the local branch)
	cmd := exec.Command("git", "branch", "-D", branch1)
	cmd.Dir = repoPath
	_ = cmd.Run() // Ignore error if branch doesn't exist locally

	cmd = exec.Command("git", "branch", "-D", branch2)
	cmd.Dir = repoPath
	_ = cmd.Run()

	// Verify branches don't exist locally
	cmd = exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch1)
	cmd.Dir = repoPath
	if cmd.Run() == nil {
		t.Fatalf("branch1 should not exist locally")
	}

	cmd = exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch2)
	cmd.Dir = repoPath
	if cmd.Run() == nil {
		t.Fatalf("branch2 should not exist locally")
	}

	t.Logf("Branches %s and %s exist only on remote", branch1, branch2)

	// Add project to config
	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 2)
	env.SetProjectGitHubConfig(t, projectName, "KudcraftsHQ", "conductor-test-repo")

	// Load config and create store + manager
	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)
	workspace.InitSetupManager(s)

	// Simulate PRInfo objects
	pr1 := config.PRInfo{
		Number:     1,
		Title:      "Test PR 1",
		HeadBranch: branch1,
		State:      "open",
	}

	pr2 := config.PRInfo{
		Number:     2,
		Title:      "Test PR 2",
		HeadBranch: branch2,
		State:      "open",
	}

	// === STEP 1: Create first worktree from remote-only branch ===
	t.Log("Step 1: Creating first worktree from remote-only branch")

	worktreeName1, worktree1, err := mgr.CreateWorktreeFromPR(projectName, pr1)
	require.NoError(t, err, "CreateWorktreeFromPR for PR 1 should succeed")
	require.NotEmpty(t, worktreeName1, "worktree1 name should not be empty")

	t.Logf("Created worktree1: %s, status: %s", worktreeName1, worktree1.SetupStatus)

	// Wait for the worktree queue to process
	waitForWorktreeReady(t, s, projectName, worktreeName1, 60*time.Second)

	// Re-fetch worktree
	gotWorktree1, ok := s.GetWorktree(projectName, worktreeName1)
	require.True(t, ok, "worktree1 should exist in store")
	t.Logf("Worktree1 final status: %s", gotWorktree1.SetupStatus)

	// Verify status - this is where the bug might occur
	if gotWorktree1.SetupStatus == config.SetupStatusFailed {
		t.Logf("BUG: Worktree1 status is FAILED when creating from remote-only branch")
	}
	assert.Equal(t, config.SetupStatusDone, gotWorktree1.SetupStatus, "worktree1 status should be done")

	// Verify worktree is on correct branch
	if workspace.WorktreeExists(gotWorktree1.Path) {
		currentBranch := getGitBranch(t, gotWorktree1.Path)
		assert.Equal(t, branch1, currentBranch, "worktree1 should be on correct branch")
		t.Logf("Worktree1 is on branch: %s", currentBranch)
	}

	// === STEP 2: Create second worktree from remote-only branch (while first exists) ===
	t.Log("Step 2: Creating second worktree from remote-only branch (while first worktree exists)")

	worktreeName2, worktree2, err := mgr.CreateWorktreeFromPR(projectName, pr2)
	require.NoError(t, err, "CreateWorktreeFromPR for PR 2 should succeed")
	require.NotEmpty(t, worktreeName2, "worktree2 name should not be empty")

	t.Logf("Created worktree2: %s, initial status: %s", worktreeName2, worktree2.SetupStatus)

	// Wait for the worktree queue to process
	waitForWorktreeReady(t, s, projectName, worktreeName2, 60*time.Second)

	// Re-fetch worktree
	gotWorktree2, ok := s.GetWorktree(projectName, worktreeName2)
	require.True(t, ok, "worktree2 should exist in store")
	t.Logf("Worktree2 final status: %s", gotWorktree2.SetupStatus)

	// Verify status - this is where the bug might occur
	if gotWorktree2.SetupStatus == config.SetupStatusFailed {
		t.Logf("BUG: Worktree2 status is FAILED when creating from remote-only branch")
		t.Logf("Worktree2 path: %s", gotWorktree2.Path)

		// Try to diagnose
		if workspace.WorktreeExists(gotWorktree2.Path) {
			t.Logf("Directory exists: yes")
			actualBranch := getGitBranch(t, gotWorktree2.Path)
			t.Logf("Actual branch: %s (expected: %s)", actualBranch, branch2)
		} else {
			t.Logf("Directory exists: no")
		}
	}
	assert.Equal(t, config.SetupStatusDone, gotWorktree2.SetupStatus, "worktree2 status should be done")

	// Verify worktree exists and is on correct branch
	assert.True(t, workspace.WorktreeExists(gotWorktree2.Path), "worktree2 should exist on disk")
	if workspace.WorktreeExists(gotWorktree2.Path) {
		currentBranch := getGitBranch(t, gotWorktree2.Path)
		assert.Equal(t, branch2, currentBranch, "worktree2 should be on correct branch")
		t.Logf("Worktree2 is on branch: %s", currentBranch)
	}

	// === STEP 3: Verify both worktrees have their test files ===
	t.Log("Step 3: Verifying both worktrees have their test files")

	testFile1Path := fmt.Sprintf("%s/test-file-1.txt", gotWorktree1.Path)
	testFile1Content, err := os.ReadFile(testFile1Path)
	require.NoError(t, err, "test file 1 should exist in worktree1")
	assert.Equal(t, "Content for PR 1", string(testFile1Content))

	testFile2Path := fmt.Sprintf("%s/test-file-2.txt", gotWorktree2.Path)
	testFile2Content, err := os.ReadFile(testFile2Path)
	require.NoError(t, err, "test file 2 should exist in worktree2")
	assert.Equal(t, "Content for PR 2", string(testFile2Content))

	t.Log("Both worktrees have correct test files!")

	// === Cleanup ===
	t.Log("Cleanup: Archiving and deleting both worktrees")

	err = mgr.ArchiveWorktree(projectName, worktreeName1)
	require.NoError(t, err, "cleanup: ArchiveWorktree1 should succeed")
	err = mgr.DeleteWorktree(projectName, worktreeName1)
	require.NoError(t, err, "cleanup: DeleteWorktree1 should succeed")

	err = mgr.ArchiveWorktree(projectName, worktreeName2)
	require.NoError(t, err, "cleanup: ArchiveWorktree2 should succeed")
	err = mgr.DeleteWorktree(projectName, worktreeName2)
	require.NoError(t, err, "cleanup: DeleteWorktree2 should succeed")

	// Clean up remote branches
	env.DeleteRemoteBranch(t, branch1)
	env.DeleteRemoteBranch(t, branch2)

	t.Log("TestWorktreeCreation_RemoteOnlyBranch completed successfully!")
}

// TestWorktreeCreation_BranchAlreadyCheckedOut tests that creating a worktree
// for a branch that is already checked out in another worktree fails gracefully.
func TestWorktreeCreation_BranchAlreadyCheckedOut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup isolated test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Initialize config
	env.InitConfig(t)

	// Clone test repo
	repoPath := env.CloneTestRepo(t)

	// Create a test branch
	branch := fmt.Sprintf("claude/test-branch-conflict-%d", time.Now().UnixNano())
	env.CreateBranchWithChanges(t, branch, "test-file.txt", "Test content")
	env.PushBranch(t, branch)

	// Add project to config
	projectName := "test-project"
	env.AddProject(t, projectName, repoPath, 2)

	// Load config and create store + manager
	cfg := env.LoadConfig(t)
	s := store.New(cfg, store.WithDebounceTime(10*time.Millisecond))
	defer func() { _, _ = s.Close() }()

	mgr := workspace.NewManagerWithStore(cfg, s)
	workspace.InitSetupManager(s)

	// === STEP 1: Create first worktree for the branch ===
	t.Log("Step 1: Creating first worktree")

	worktreeName1, _, err := mgr.CreateWorktree(projectName, branch, 2)
	require.NoError(t, err, "First CreateWorktree should succeed")

	// Verify worktree was created
	gotWorktree1, ok := s.GetWorktree(projectName, worktreeName1)
	require.True(t, ok, "worktree1 should exist")
	assert.Equal(t, branch, gotWorktree1.Branch)
	assert.Equal(t, config.SetupStatusDone, gotWorktree1.SetupStatus)

	t.Logf("Created first worktree: %s for branch %s", worktreeName1, branch)

	// === STEP 2: Try to create a second worktree for the SAME branch ===
	t.Log("Step 2: Trying to create second worktree for same branch (should fail)")

	_, _, err = mgr.CreateWorktree(projectName, branch, 2)
	require.Error(t, err, "Second CreateWorktree for same branch should fail")
	assert.Contains(t, err.Error(), "already used by worktree", "error should mention branch is already used")
	t.Logf("Got expected error: %v", err)

	// === STEP 3: Verify only one worktree exists ===
	t.Log("Step 3: Verifying only one worktree exists")

	project, _ := s.GetProject(projectName)
	activeWorktrees := 0
	for name, wt := range project.Worktrees {
		if !wt.Archived && !wt.IsRoot {
			activeWorktrees++
			t.Logf("Active worktree: %s (branch: %s)", name, wt.Branch)
		}
	}
	assert.Equal(t, 1, activeWorktrees, "should only have one active worktree")

	// === Cleanup ===
	t.Log("Cleanup")

	err = mgr.ArchiveWorktree(projectName, worktreeName1)
	require.NoError(t, err)
	err = mgr.DeleteWorktree(projectName, worktreeName1)
	require.NoError(t, err)

	env.DeleteRemoteBranch(t, branch)

	t.Log("TestWorktreeCreation_BranchAlreadyCheckedOut completed successfully!")
}
