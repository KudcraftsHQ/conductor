package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitRemoteBranchExists checks if a remote branch exists
func GitRemoteBranchExists(repoPath, remote, branch string) bool {
	cmd := exec.Command("git", "ls-remote", "--heads", remote, branch)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// GitGetDefaultBranch returns the default branch (master if exists, otherwise main)
func GitGetDefaultBranch(repoPath string) string {
	// Check master first - if both exist, prefer master
	if GitRemoteBranchExists(repoPath, "origin", "master") {
		return "master"
	}
	if GitRemoteBranchExists(repoPath, "origin", "main") {
		return "main"
	}
	// Fallback to main
	return "main"
}

// GitWorktreeAdd creates a new git worktree based on origin's default branch
func GitWorktreeAdd(repoPath, worktreePath, branch string) error {
	// Determine the default branch
	defaultBranch := GitGetDefaultBranch(repoPath)

	// Fetch latest from origin first
	fetchCmd := exec.Command("git", "fetch", "origin", defaultBranch)
	fetchCmd.Dir = repoPath
	fetchCmd.CombinedOutput() // Ignore error - remote might not exist

	// Create worktree with new branch based on origin's default branch
	cmd := exec.Command("git", "worktree", "add", "-b", branch, worktreePath, "origin/"+defaultBranch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback to creating from current HEAD if origin doesn't exist
		cmd = exec.Command("git", "worktree", "add", "-b", branch, worktreePath)
		cmd.Dir = repoPath
		out, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git worktree add failed: %s", string(out))
		}
	}
	return nil
}

// GitWorktreeAddExisting creates a worktree for an existing branch
func GitWorktreeAddExisting(repoPath, worktreePath, branch string) error {
	cmd := exec.Command("git", "worktree", "add", worktreePath, branch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add failed: %s", string(out))
	}
	return nil
}

// GitWorktreeRemove removes a git worktree
func GitWorktreeRemove(repoPath, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove failed: %s", string(out))
	}
	return nil
}

// GitWorktreeList returns list of worktrees
func GitWorktreeList(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list failed: %w", err)
	}

	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimPrefix(line, "worktree ")
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// GitBranchDelete deletes a git branch
func GitBranchDelete(repoPath, branch string) error {
	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch delete failed: %s", string(out))
	}
	return nil
}

// GitBranchExists checks if a branch exists
func GitBranchExists(repoPath, branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repoPath
	return cmd.Run() == nil
}

// GitCurrentBranch returns the current branch name
func GitCurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// GetRootPath returns the git root path (handles worktrees)
func GetRootPath(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git root: %w", err)
	}

	gitDir := strings.TrimSpace(string(out))
	if gitDir == ".git" {
		// We're in the root repo
		absPath, _ := filepath.Abs(path)
		return absPath, nil
	}

	// We're in a worktree, git-common-dir points to .git of main repo
	// e.g., /path/to/repo/.git -> /path/to/repo
	return filepath.Dir(gitDir), nil
}

// IsWorktree checks if the current path is a git worktree (not main repo)
func IsWorktree(path string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}

	gitDir := strings.TrimSpace(string(out))
	// If .git is a file (not a directory), it's a worktree
	return strings.Contains(gitDir, ".git/worktrees/"), nil
}
