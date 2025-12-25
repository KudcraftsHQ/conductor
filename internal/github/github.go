package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// ghPR represents the JSON output from gh pr list/view
type ghPR struct {
	Number         int       `json:"number"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`
	State          string    `json:"state"`
	Author         ghAuthor  `json:"author"`
	IsDraft        bool      `json:"isDraft"`
	HeadRefName    string    `json:"headRefName"` // The branch being merged
	UpdatedAt      time.Time `json:"updatedAt"`
}

type ghAuthor struct {
	Login string `json:"login"`
}

// IsGHInstalled checks if gh CLI is available
func IsGHInstalled() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// IsGHAuthenticated checks if gh is authenticated
func IsGHAuthenticated() bool {
	cmd := exec.Command("gh", "auth", "status")
	return cmd.Run() == nil
}

// GetPRsForBranch returns PRs where head branch matches the given branch
func GetPRsForBranch(owner, repo, branch string) ([]config.PRInfo, error) {
	if !IsGHInstalled() {
		return nil, fmt.Errorf("gh CLI not installed")
	}

	// Get all PRs (open, closed, merged) for this branch
	cmd := exec.Command("gh", "pr", "list",
		"--repo", fmt.Sprintf("%s/%s", owner, repo),
		"--head", branch,
		"--state", "all",
		"--json", "number,url,title,state,author,isDraft,headRefName,updatedAt",
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PRs: %w", err)
	}

	var ghPRs []ghPR
	if err := json.Unmarshal(output, &ghPRs); err != nil {
		return nil, fmt.Errorf("failed to parse PR data: %w", err)
	}

	return convertToPRInfo(ghPRs), nil
}

// GetAllPRs returns all open PRs for a repo
func GetAllPRs(owner, repo string) ([]config.PRInfo, error) {
	if !IsGHInstalled() {
		return nil, fmt.Errorf("gh CLI not installed")
	}

	cmd := exec.Command("gh", "pr", "list",
		"--repo", fmt.Sprintf("%s/%s", owner, repo),
		"--state", "all",
		"--limit", "100",
		"--json", "number,url,title,state,author,isDraft,headRefName,updatedAt",
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PRs: %w", err)
	}

	var ghPRs []ghPR
	if err := json.Unmarshal(output, &ghPRs); err != nil {
		return nil, fmt.Errorf("failed to parse PR data: %w", err)
	}

	return convertToPRInfo(ghPRs), nil
}

// DetectRepoFromRemote parses git remote to extract owner/repo
func DetectRepoFromRemote(projectPath string) (owner, repo string, err error) {
	cmd := exec.Command("git", "-C", projectPath, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to get git remote: %w", err)
	}

	url := strings.TrimSpace(string(output))
	return parseGitHubURL(url)
}

// parseGitHubURL extracts owner and repo from various GitHub URL formats
func parseGitHubURL(url string) (owner, repo string, err error) {
	// SSH format: git@github.com:owner/repo.git
	sshRegex := regexp.MustCompile(`git@github\.com:([^/]+)/([^/]+?)(?:\.git)?$`)
	if matches := sshRegex.FindStringSubmatch(url); len(matches) == 3 {
		return matches[1], matches[2], nil
	}

	// HTTPS format: https://github.com/owner/repo.git
	httpsRegex := regexp.MustCompile(`https://github\.com/([^/]+)/([^/]+?)(?:\.git)?$`)
	if matches := httpsRegex.FindStringSubmatch(url); len(matches) == 3 {
		return matches[1], matches[2], nil
	}

	return "", "", fmt.Errorf("could not parse GitHub URL: %s", url)
}

// OpenInBrowser opens a URL in the default browser
func OpenInBrowser(url string) error {
	cmd := exec.Command("open", url)
	return cmd.Run()
}

// convertToPRInfo converts gh CLI output to our PRInfo type
func convertToPRInfo(ghPRs []ghPR) []config.PRInfo {
	prs := make([]config.PRInfo, len(ghPRs))
	for i, pr := range ghPRs {
		state := pr.State
		if pr.IsDraft {
			state = "draft"
		} else if state == "MERGED" {
			state = "merged"
		} else if state == "CLOSED" {
			state = "closed"
		} else {
			state = "open"
		}

		prs[i] = config.PRInfo{
			Number:     pr.Number,
			URL:        pr.URL,
			Title:      pr.Title,
			State:      state,
			Author:     pr.Author.Login,
			HeadBranch: pr.HeadRefName,
			UpdatedAt:  pr.UpdatedAt,
		}
	}
	return prs
}
