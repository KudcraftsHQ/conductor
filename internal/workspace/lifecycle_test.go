package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitRepoWithWorktree builds a real repository and a linked worktree, because
// the thing under test is whether the working tree survives — and only git can
// answer that honestly.
func gitRepoWithWorktree(t *testing.T) (repoPath, worktreePath, branch string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	root := t.TempDir()
	repoPath = filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(repoPath, 0755))

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	run(repoPath, "init", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0644))
	run(repoPath, "add", ".")
	run(repoPath, "commit", "-m", "initial")

	branch = "feat/keepme"
	worktreePath = filepath.Join(root, "wt")
	run(repoPath, "worktree", "add", "-b", branch, worktreePath)

	return repoPath, worktreePath, branch
}

func lifecycleConfig(repoPath, worktreePath, branch string) (*config.Config, string) {
	cfg := config.NewConfig()
	cfg.Projects["proj"] = &config.Project{
		Path:                    repoPath,
		DefaultPortsPerWorktree: 1,
		Worktrees: map[string]*config.Worktree{
			"sydney": {Path: worktreePath, Branch: branch, Ports: []int{3100}},
		},
	}
	return cfg, "sydney"
}

// Hibernation must never touch the working tree or the branch. This is the
// regression that would quietly destroy work: it is reached by archiving a
// thread, which is a routine, reversible act in T3.
func TestReleaseResourcesKeepsTreeAndBranch(t *testing.T) {
	repoPath, worktreePath, branch := gitRepoWithWorktree(t)
	cfg, name := lifecycleConfig(repoPath, worktreePath, branch)

	// Uncommitted work is exactly what must survive.
	scratch := filepath.Join(worktreePath, "wip.txt")
	require.NoError(t, os.WriteFile(scratch, []byte("unsaved\n"), 0644))

	manager := NewManager(cfg)
	require.NoError(t, manager.ReleaseResources("proj", name))

	assert.DirExists(t, worktreePath, "hibernation must leave the working tree in place")
	assert.FileExists(t, scratch, "hibernation must leave uncommitted work alone")
	assert.True(t, GitBranchExists(repoPath, branch), "hibernation must not delete the branch")

	worktree := cfg.Projects["proj"].Worktrees[name]
	assert.True(t, worktree.Hibernated)
	assert.False(t, worktree.Archived, "hibernated is not archived — the tree is still there")
	assert.Empty(t, worktree.Ports, "ports must be returned")
}

// The T3-driven teardown removes the tree but keeps the branch, matching what
// T3's own worktree removal does. A thread deleted by mistake then costs a
// directory that can be recreated rather than commits that cannot.
func TestRemoveTreeKeepsBranchWhenAsked(t *testing.T) {
	repoPath, worktreePath, branch := gitRepoWithWorktree(t)
	cfg, name := lifecycleConfig(repoPath, worktreePath, branch)

	manager := NewManager(cfg)
	require.NoError(t, manager.RemoveTree("proj", name, false))

	assert.NoDirExists(t, worktreePath)
	assert.True(t, GitBranchExists(repoPath, branch), "the branch must outlive the worktree")
	assert.True(t, cfg.Projects["proj"].Worktrees[name].Archived)
}

// The conductor-initiated archive is the destructive one and still deletes the
// branch, so existing behaviour is unchanged for anyone not using T3.
func TestArchiveWorktreeStillDeletesBranch(t *testing.T) {
	repoPath, worktreePath, branch := gitRepoWithWorktree(t)
	cfg, name := lifecycleConfig(repoPath, worktreePath, branch)

	manager := NewManager(cfg)
	require.NoError(t, manager.ArchiveWorktree("proj", name))

	assert.NoDirExists(t, worktreePath)
	assert.False(t, GitBranchExists(repoPath, branch))
}

// Removing a tree that is already gone is success: T3 deletes its own worktree
// after the last thread on it goes, racing the watcher to the same directory.
func TestRemoveTreeToleratesAMissingDirectory(t *testing.T) {
	repoPath, worktreePath, branch := gitRepoWithWorktree(t)
	cfg, name := lifecycleConfig(repoPath, worktreePath, branch)

	require.NoError(t, os.RemoveAll(worktreePath))

	manager := NewManager(cfg)
	assert.NoError(t, manager.RemoveTree("proj", name, false))
	assert.True(t, cfg.Projects["proj"].Worktrees[name].Archived)
}

// RegisterWorktree has to accept a directory anywhere, because T3 puts the
// worktrees it creates under its own root rather than conductor's.
func TestRegisterWorktreeAcceptsAnArbitraryPath(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Projects["proj"] = &config.Project{
		Path:                    "/tmp/proj",
		DefaultPortsPerWorktree: 1,
		Worktrees:               map[string]*config.Worktree{},
	}

	manager := NewManager(cfg)
	worktree, err := manager.RegisterWorktree("proj", "sydney", "feat/x", "/home/u/.t3/worktrees/proj/feat-x", 1)
	require.NoError(t, err)

	assert.Equal(t, "/home/u/.t3/worktrees/proj/feat-x", worktree.Path)
	assert.Equal(t, "feat/x", worktree.Branch)
	assert.Len(t, worktree.Ports, 1)
}

// A worktree already registered at a path must not be registered twice, which
// is what makes the setup hook safe to fire more than once.
func TestRegisterWorktreeRejectsADuplicateName(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Projects["proj"] = &config.Project{
		Path:                    "/tmp/proj",
		DefaultPortsPerWorktree: 1,
		Worktrees: map[string]*config.Worktree{
			"sydney": {Path: "/somewhere", Branch: "b"},
		},
	}

	_, err := NewManager(cfg).RegisterWorktree("proj", "sydney", "b", "/somewhere", 1)
	assert.Error(t, err)
}
