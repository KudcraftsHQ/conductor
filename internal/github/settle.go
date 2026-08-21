package github

import (
	"strings"
	"sync"
)

// PRStates maps a branch name to its pull request state: "open", "merged",
// "closed" or "draft".
type PRStates map[string]string

// BranchStates fetches every PR in a repository once and indexes it by head
// branch.
//
// Per-repository rather than per-branch on purpose. Resolving settled-ness
// touches every worktree conductor knows about — thirty-one on this machine —
// and `gh pr list --head` per branch is thirty-one round trips to answer a
// question one call already covers.
//
// When a branch carries several PRs the most final state wins: merged over
// closed over open. A reopened-then-merged branch is merged, and a branch whose
// second PR is still open is *not* settled by the first one having landed.
func BranchStates(owner, repo string) (PRStates, error) {
	prs, err := GetAllPRs(owner, repo)
	if err != nil {
		return nil, err
	}
	states := PRStates{}
	for _, pr := range prs {
		branch := strings.TrimSpace(pr.HeadBranch)
		if branch == "" {
			continue
		}
		if existing, ok := states[branch]; ok && rank(existing) >= rank(pr.State) {
			continue
		}
		states[branch] = pr.State
	}
	return states, nil
}

// rank orders PR states by finality. An open PR outranks a merged one because
// "still open" is the claim that keeps a worktree alive, and losing it to an
// older merged PR on the same branch would settle live work.
func rank(state string) int {
	switch state {
	case "open", "draft":
		return 3
	case "merged":
		return 2
	case "closed":
		return 1
	}
	return 0
}

// Cache memoises BranchStates per repository for the life of a command.
type Cache struct {
	mu     sync.Mutex
	byRepo map[string]PRStates
	fail   map[string]bool
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{byRepo: map[string]PRStates{}, fail: map[string]bool{}}
}

// StateFor returns the PR state of a branch, or "" when there is no PR, no
// GitHub remote, or gh cannot answer.
//
// A failure is cached alongside the successes. Without that, a project with no
// GitHub remote pays the full gh timeout once per worktree, and reconcile
// against a private or unauthenticated repo would crawl.
func (c *Cache) StateFor(projectPath, branch string) string {
	if branch == "" || projectPath == "" {
		return ""
	}
	owner, repo, err := DetectRepoFromRemote(projectPath)
	if err != nil {
		return ""
	}
	key := owner + "/" + repo

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail[key] {
		return ""
	}
	states, ok := c.byRepo[key]
	if !ok {
		states, err = BranchStates(owner, repo)
		if err != nil {
			c.fail[key] = true
			return ""
		}
		c.byRepo[key] = states
	}
	return states[branch]
}
