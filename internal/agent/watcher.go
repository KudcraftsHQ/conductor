package agent

import (
	"context"
	"log"
	"time"

	"github.com/hammashamzah/conductor/internal/clickup"
	"github.com/hammashamzah/conductor/internal/github"
	"github.com/hammashamzah/conductor/internal/store"
)

// PRWatcher monitors worktrees created by the agent for PR creation
type PRWatcher struct {
	store         *store.Store
	clickupClient *clickup.Client
	interval      time.Duration
}

// NewPRWatcher creates a new PR watcher
func NewPRWatcher(s *store.Store, client *clickup.Client, interval time.Duration) *PRWatcher {
	if interval == 0 {
		interval = 60 * time.Second
	}
	return &PRWatcher{
		store:         s,
		clickupClient: client,
		interval:      interval,
	}
}

// Start begins watching for PRs on agent-created worktrees
func (w *PRWatcher) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.checkForPRs()
		}
	}
}

// checkForPRs scans all projects for agent worktrees that need PR updates
func (w *PRWatcher) checkForPRs() {
	projects := w.store.GetAllProjects()

	for projectName, project := range projects {
		if project.GitHubOwner == "" || project.GitHubRepo == "" {
			continue
		}

		for worktreeName, worktree := range project.Worktrees {
			// Only watch agent-created worktrees (have ClickUp task ID)
			if worktree.ClickUpTaskID == "" {
				continue
			}
			// Skip archived or already-PR'd worktrees
			if worktree.Archived {
				continue
			}
			// Skip if already has PRs
			if len(worktree.PRs) > 0 {
				continue
			}

			// Check for PRs on this branch
			prs, err := github.GetPRsForBranch(project.GitHubOwner, project.GitHubRepo, worktree.Branch)
			if err != nil {
				log.Printf("watcher: failed to check PRs for %s/%s: %v", projectName, worktreeName, err)
				continue
			}

			if len(prs) == 0 {
				continue
			}

			// PR found! Update store
			_ = w.store.SetWorktreePRs(projectName, worktreeName, prs)

			// Update ClickUp task
			pr := prs[0]
			log.Printf("watcher: PR #%d created for task %s (%s/%s)", pr.Number, worktree.ClickUpTaskID, projectName, worktreeName)

			// Move task to "in review"
			if err := w.clickupClient.UpdateTaskStatus(worktree.ClickUpTaskID, "in review"); err != nil {
				log.Printf("watcher: failed to update ClickUp task status: %v", err)
			}

			// Add comment with PR URL
			comment := "PR created: " + pr.URL
			if err := w.clickupClient.AddTaskComment(worktree.ClickUpTaskID, comment); err != nil {
				log.Printf("watcher: failed to add ClickUp comment: %v", err)
			}
		}
	}
}
