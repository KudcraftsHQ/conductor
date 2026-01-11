package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WorktreeExists checks if a worktree directory exists
func WorktreeExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

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

// GitWorktreeAdd creates a new git worktree based on the specified branch from origin
func GitWorktreeAdd(repoPath, worktreePath, branch string) error {
	// Check if the remote branch exists
	remoteBranchExists := GitRemoteBranchExists(repoPath, "origin", branch)

	if remoteBranchExists {
		// Fetch the specific branch from origin and update the remote tracking branch
		// Using refspec to ensure origin/<branch> is updated locally
		refspec := fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)
		fetchCmd := exec.Command("git", "fetch", "origin", refspec)
		fetchCmd.Dir = repoPath
		_, _ = fetchCmd.CombinedOutput() // Ignore error - will fallback below

		// Create worktree tracking the remote branch
		cmd := exec.Command("git", "worktree", "add", "--track", "-b", branch, worktreePath, "origin/"+branch)
		cmd.Dir = repoPath
		if _, err := cmd.CombinedOutput(); err == nil {
			return nil
		}
		// If tracking failed, try without --track
		cmd = exec.Command("git", "worktree", "add", "-b", branch, worktreePath, "origin/"+branch)
		cmd.Dir = repoPath
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git worktree add failed: %s", string(out))
		}
		return nil
	}

	// Remote branch doesn't exist - create from default branch
	defaultBranch := GitGetDefaultBranch(repoPath)

	// Fetch latest from origin first
	fetchCmd := exec.Command("git", "fetch", "origin", defaultBranch)
	fetchCmd.Dir = repoPath
	_, _ = fetchCmd.CombinedOutput() // Ignore error - remote might not exist

	// Create worktree with new branch based on origin's default branch
	cmd := exec.Command("git", "worktree", "add", "-b", branch, worktreePath, "origin/"+defaultBranch)
	cmd.Dir = repoPath
	if _, err := cmd.CombinedOutput(); err != nil {
		// Fallback to creating from current HEAD if origin doesn't exist
		cmd = exec.Command("git", "worktree", "add", "-b", branch, worktreePath)
		cmd.Dir = repoPath
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git worktree add failed: %s", string(out))
		}
	}
	return nil
}

// GitWorktreeAddNewBranch creates a worktree with a new branch based on another branch from origin
// This is used when the original branch is already checked out elsewhere
func GitWorktreeAddNewBranch(repoPath, worktreePath, newBranch, baseBranch string) error {
	// First fetch the base branch from origin
	refspec := fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", baseBranch, baseBranch)
	fetchCmd := exec.Command("git", "fetch", "origin", refspec)
	fetchCmd.Dir = repoPath
	_, _ = fetchCmd.CombinedOutput() // Ignore error - will fallback below

	// Create worktree with new branch based on origin/<baseBranch>
	cmd := exec.Command("git", "worktree", "add", "-b", newBranch, worktreePath, "origin/"+baseBranch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add failed: %s", string(out))
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

// WorktreeInfo holds information about a git worktree
type WorktreeInfo struct {
	Path   string
	Branch string
}

// GitWorktreeListWithBranches returns list of worktrees with their branches
func GitWorktreeListWithBranches(repoPath string) ([]WorktreeInfo, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list failed: %w", err)
	}

	var worktrees []WorktreeInfo
	var current WorktreeInfo

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			// Save previous worktree if exists
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = WorktreeInfo{Path: strings.TrimPrefix(line, "worktree ")}
		} else if strings.HasPrefix(line, "branch refs/heads/") {
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	// Save last worktree
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// GitBranchCheckedOutAt returns the worktree path where a branch is checked out, or empty string if not checked out
func GitBranchCheckedOutAt(repoPath, branch string) string {
	worktrees, err := GitWorktreeListWithBranches(repoPath)
	if err != nil {
		return ""
	}

	for _, wt := range worktrees {
		if wt.Branch == branch {
			return wt.Path
		}
	}
	return ""
}

// SuggestNewBranchName suggests a new branch name when the original is already in use
// Returns something like "branch-name-2", "branch-name-3", etc.
func SuggestNewBranchName(repoPath, originalBranch string) string {
	worktrees, err := GitWorktreeListWithBranches(repoPath)
	if err != nil {
		return originalBranch + "-2"
	}

	// Build a set of existing branches
	existingBranches := make(map[string]bool)
	for _, wt := range worktrees {
		existingBranches[wt.Branch] = true
	}

	// Also check local branches
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				existingBranches[line] = true
			}
		}
	}

	// Find the next available suffix
	for i := 2; i <= 100; i++ {
		candidate := fmt.Sprintf("%s-%d", originalBranch, i)
		if !existingBranches[candidate] {
			return candidate
		}
	}

	// Fallback
	return fmt.Sprintf("%s-%d", originalBranch, time.Now().Unix())
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

// GitHasUncommittedChanges checks if working directory has uncommitted changes
func GitHasUncommittedChanges(worktreePath string) (bool, error) {
	// git status --porcelain returns empty string if clean
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status failed: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// GitCommitsBehind returns how many commits the branch is behind origin/defaultBranch
func GitCommitsBehind(worktreePath, defaultBranch string) (int, error) {
	// git rev-list HEAD..origin/main --count
	cmd := exec.Command("git", "rev-list", "HEAD..origin/"+defaultBranch, "--count")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		// Could be no upstream or other error - return 0
		return 0, nil
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("invalid count: %w", err)
	}
	return count, nil
}

// OrphanedBranchInfo contains info about an orphaned git branch
type OrphanedBranchInfo struct {
	Branch       string
	LastCommit   string
	CommitDate   string
	CheckedOutAt string // Path where checked out (if any)
}

// GitListOrphanedBranches returns local branches that don't have associated worktrees in config
// but may still be preventing worktree creation
func GitListOrphanedBranches(repoPath string, knownBranches map[string]bool) ([]OrphanedBranchInfo, error) {
	// Get all local branches with their last commit info
	// Format: branch|commit-short|commit-date
	cmd := exec.Command("git", "branch", "--format=%(refname:short)|%(objectname:short)|%(committerdate:relative)")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch list failed: %w", err)
	}

	// Get worktree list to check where branches are checked out
	worktrees, _ := GitWorktreeListWithBranches(repoPath)
	checkedOutMap := make(map[string]string)
	for _, wt := range worktrees {
		if wt.Branch != "" {
			checkedOutMap[wt.Branch] = wt.Path
		}
	}

	var orphaned []OrphanedBranchInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}

		branch := parts[0]
		commit := parts[1]
		date := parts[2]

		// Skip if this branch is known (has a worktree in config)
		if knownBranches[branch] {
			continue
		}

		// Skip main/master branches
		if branch == "main" || branch == "master" {
			continue
		}

		info := OrphanedBranchInfo{
			Branch:     branch,
			LastCommit: commit,
			CommitDate: date,
		}

		// Check if it's checked out somewhere
		if path, ok := checkedOutMap[branch]; ok {
			info.CheckedOutAt = path
		}

		orphaned = append(orphaned, info)
	}

	return orphaned, nil
}

// GitDeleteBranchForce forcefully deletes a local branch
func GitDeleteBranchForce(repoPath, branch string) error {
	// First check if the branch is checked out in any worktree
	if path := GitBranchCheckedOutAt(repoPath, branch); path != "" {
		return fmt.Errorf("branch '%s' is still checked out at %s", branch, path)
	}

	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch delete failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
