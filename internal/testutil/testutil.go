// Package testutil provides test utilities for conductor integration tests.
// It allows tests to run in isolated environments without affecting the real config.
package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hammashamzah/conductor/internal/config"
)

const (
	// TestRepoURL is the public test repository for integration tests
	// Falls back to creating a local repo if not available
	TestRepoURL = "https://github.com/KudcraftsHQ/conductor-test-repo.git"

	// EnvConfigDir is the environment variable to override conductor config directory
	EnvConfigDir = "CONDUCTOR_CONFIG_DIR"
)

// TestEnv represents an isolated test environment
type TestEnv struct {
	// ConfigDir is the temporary directory for conductor config
	ConfigDir string

	// RepoDir is the directory where the test repo is cloned (if used)
	RepoDir string

	// originalEnv stores the original CONDUCTOR_CONFIG_DIR value
	originalEnv string

	// hadOriginalEnv tracks whether the env var was originally set
	hadOriginalEnv bool

	// t is the testing context
	t *testing.T
}

// NewTestEnv creates a new isolated test environment.
// It sets up a temporary directory for conductor config and overrides
// the CONDUCTOR_CONFIG_DIR environment variable.
// Call Cleanup() when done to restore the environment.
func NewTestEnv(t *testing.T) *TestEnv {
	t.Helper()

	// Create temp directory for config
	configDir := t.TempDir()

	// Save original env var
	originalEnv, hadOriginalEnv := os.LookupEnv(EnvConfigDir)

	// Set the override
	if err := os.Setenv(EnvConfigDir, configDir); err != nil {
		t.Fatalf("failed to set %s: %v", EnvConfigDir, err)
	}

	return &TestEnv{
		ConfigDir:      configDir,
		originalEnv:    originalEnv,
		hadOriginalEnv: hadOriginalEnv,
		t:              t,
	}
}

// Cleanup restores the original environment and removes temp directories.
// This is automatically called by t.TempDir() cleanup, but the env var
// must be restored manually.
func (e *TestEnv) Cleanup() {
	// Restore original env var
	if e.hadOriginalEnv {
		_ = os.Setenv(EnvConfigDir, e.originalEnv)
	} else {
		_ = os.Unsetenv(EnvConfigDir)
	}
}

// InitConfig creates an initial conductor config in the test environment.
// Returns the initialized config.
func (e *TestEnv) InitConfig(t *testing.T) *config.Config {
	t.Helper()

	cfg := config.NewConfig()
	if err := config.Save(cfg); err != nil {
		t.Fatalf("failed to save initial config: %v", err)
	}

	return cfg
}

// CloneTestRepo clones the KudcraftsHQ/conductor-test-repo into a temp directory.
// Returns the path to the cloned repository.
func (e *TestEnv) CloneTestRepo(t *testing.T) string {
	t.Helper()

	// Create temp directory for repo
	repoDir := filepath.Join(t.TempDir(), "conductor-test-repo")

	// Clone the repo
	cmd := exec.Command("git", "clone", "--depth", "1", TestRepoURL, repoDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to clone test repo: %v", err)
	}

	e.RepoDir = repoDir
	return repoDir
}

// CreateTestBranch creates a new branch in the test repo.
// The branch is created from the current HEAD.
func (e *TestEnv) CreateTestBranch(t *testing.T, branchName string) {
	t.Helper()

	if e.RepoDir == "" {
		t.Fatal("RepoDir not set - call CloneTestRepo first")
	}

	cmd := exec.Command("git", "branch", branchName)
	cmd.Dir = e.RepoDir

	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create branch %s: %v", branchName, err)
	}
}

// AddProject adds a project to the test config.
// Returns the project name used.
func (e *TestEnv) AddProject(t *testing.T, name string, repoPath string, portsPerWorktree int) *config.Project {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	project := config.NewProject(repoPath, portsPerWorktree)

	// Add root worktree
	rootWorktree := config.NewWorktree(repoPath, "main", true, nil)
	rootWorktree.SetupStatus = config.SetupStatusDone
	project.Worktrees["root"] = rootWorktree

	cfg.Projects[name] = project

	if err := config.Save(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	return project
}

// LoadConfig loads the current config from the test environment.
func (e *TestEnv) LoadConfig(t *testing.T) *config.Config {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	return cfg
}

// CreateSetupScript creates a setup.sh script in the test repo's .conductor-scripts directory.
// The script content is executed when a worktree is created.
func (e *TestEnv) CreateSetupScript(t *testing.T, scriptContent string) {
	t.Helper()
	e.createScriptFile(t, "setup.sh", scriptContent)
}

// CreateArchiveScript creates an archive.sh script in the test repo's .conductor-scripts directory.
// The script content is executed when a worktree is archived.
func (e *TestEnv) CreateArchiveScript(t *testing.T, scriptContent string) {
	t.Helper()
	e.createScriptFile(t, "archive.sh", scriptContent)
}

// createScriptFile creates a script file in .conductor-scripts and commits it.
func (e *TestEnv) createScriptFile(t *testing.T, filename, scriptContent string) {
	t.Helper()

	if e.RepoDir == "" {
		t.Fatal("RepoDir not set - call CloneTestRepo first")
	}

	scriptsDir := filepath.Join(e.RepoDir, ".conductor-scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("failed to create .conductor-scripts directory: %v", err)
	}

	scriptPath := filepath.Join(scriptsDir, filename)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write %s: %v", filename, err)
	}

	// Commit the script so it's available in worktrees
	cmd := exec.Command("git", "add", filepath.Join(".conductor-scripts", filename))
	cmd.Dir = e.RepoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git add %s: %v", filename, err)
	}

	cmd = exec.Command("git", "commit", "-m", fmt.Sprintf("Add %s for testing", filename))
	cmd.Dir = e.RepoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git commit %s: %v", filename, err)
	}
}

// CreateProjectConfig creates a conductor.json in the test repo with inline scripts.
// scripts is a map of script name ("setup", "run", "archive") to script content.
func (e *TestEnv) CreateProjectConfig(t *testing.T, scripts map[string]string) {
	t.Helper()

	if e.RepoDir == "" {
		t.Fatal("RepoDir not set - call CloneTestRepo first")
	}

	projectConfig := map[string]interface{}{
		"scripts": scripts,
	}

	data, err := json.MarshalIndent(projectConfig, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal project config: %v", err)
	}

	configPath := filepath.Join(e.RepoDir, "conductor.json")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("failed to write conductor.json: %v", err)
	}

	// Commit the config so it's available in worktrees
	cmd := exec.Command("git", "add", "conductor.json")
	cmd.Dir = e.RepoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git add conductor.json: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Add conductor.json for testing")
	cmd.Dir = e.RepoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git commit conductor.json: %v", err)
	}
}

// CreateBranchWithChanges creates a branch with a file change and commits it.
// Returns the branch name.
func (e *TestEnv) CreateBranchWithChanges(t *testing.T, branchName, fileName, content string) string {
	t.Helper()

	if e.RepoDir == "" {
		t.Fatal("RepoDir not set - call CloneTestRepo first")
	}

	// Create and checkout the branch
	cmd := exec.Command("git", "checkout", "-b", branchName)
	cmd.Dir = e.RepoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create branch %s: %v", branchName, err)
	}

	// Create a file with content
	filePath := filepath.Join(e.RepoDir, fileName)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file %s: %v", fileName, err)
	}

	// Add and commit
	cmd = exec.Command("git", "add", fileName)
	cmd.Dir = e.RepoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git add %s: %v", fileName, err)
	}

	cmd = exec.Command("git", "commit", "-m", "Add "+fileName+" for testing")
	cmd.Dir = e.RepoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}

	// Switch back to main
	cmd = exec.Command("git", "checkout", "main")
	cmd.Dir = e.RepoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to checkout main: %v", err)
	}

	return branchName
}

// PushBranch pushes a branch to the remote repository.
func (e *TestEnv) PushBranch(t *testing.T, branchName string) {
	t.Helper()

	if e.RepoDir == "" {
		t.Fatal("RepoDir not set - call CloneTestRepo first")
	}

	cmd := exec.Command("git", "push", "-u", "origin", branchName)
	cmd.Dir = e.RepoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to push branch %s: %v\nOutput: %s", branchName, err, output)
	}
}

// CreatePR creates a pull request on the test repo using gh CLI.
// Returns the PR number.
func (e *TestEnv) CreatePR(t *testing.T, branchName, title, body string) int {
	t.Helper()

	if e.RepoDir == "" {
		t.Fatal("RepoDir not set - call CloneTestRepo first")
	}

	cmd := exec.Command("gh", "pr", "create",
		"--head", branchName,
		"--title", title,
		"--body", body,
		"--repo", "KudcraftsHQ/conductor-test-repo")
	cmd.Dir = e.RepoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to create PR: %v\nOutput: %s", err, output)
	}

	// Parse PR number from output (URL like https://github.com/KudcraftsHQ/conductor-test-repo/pull/1)
	var prNumber int
	_, _ = fmt.Sscanf(string(output), "https://github.com/KudcraftsHQ/conductor-test-repo/pull/%d", &prNumber)
	if prNumber == 0 {
		t.Fatalf("failed to parse PR number from output: %s", output)
	}

	return prNumber
}

// ClosePR closes a pull request on the test repo.
func (e *TestEnv) ClosePR(t *testing.T, prNumber int) {
	t.Helper()

	cmd := exec.Command("gh", "pr", "close",
		fmt.Sprintf("%d", prNumber),
		"--repo", "KudcraftsHQ/conductor-test-repo")
	cmd.Dir = e.RepoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Warning: failed to close PR %d: %v\nOutput: %s", prNumber, err, output)
	}
}

// DeleteRemoteBranch deletes a branch from the remote repository.
func (e *TestEnv) DeleteRemoteBranch(t *testing.T, branchName string) {
	t.Helper()

	if e.RepoDir == "" {
		t.Fatal("RepoDir not set - call CloneTestRepo first")
	}

	cmd := exec.Command("git", "push", "origin", "--delete", branchName)
	cmd.Dir = e.RepoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Warning: failed to delete remote branch %s: %v\nOutput: %s", branchName, err, output)
	}
}

// SetProjectGitHubConfig sets the GitHub owner and repo for a project.
func (e *TestEnv) SetProjectGitHubConfig(t *testing.T, projectName, owner, repo string) {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	project, ok := cfg.Projects[projectName]
	if !ok {
		t.Fatalf("project %s not found", projectName)
	}

	project.GitHubOwner = owner
	project.GitHubRepo = repo

	if err := config.Save(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}
}
