package orchestration

import (
	"fmt"
	"path/filepath"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// conductorWorktrees is the production Worktrees implementation: Conductor's
// own worktree manager, which allocates ports, creates the git worktree and
// runs the project's setup script.
type conductorWorktrees struct {
	cfg   *config.Config
	store *store.Store
	mgr   *workspace.Manager
}

// NewConductorWorktrees wires the orchestration launcher to Conductor's
// worktree lifecycle.
func NewConductorWorktrees(cfg *config.Config, s *store.Store) Worktrees {
	return &conductorWorktrees{cfg: cfg, store: s, mgr: workspace.NewManagerWithStore(cfg, s)}
}

// CreateFresh always makes a new worktree. There is no branch parameter and no
// reuse path: the worktree name comes from Conductor's city generator and the
// branch from the worktree name, so two requests for the same project can never
// land in the same checkout.
func (c *conductorWorktrees) CreateFresh(project string) (WorktreeRef, error) {
	proj, ok := c.cfg.GetProject(project)
	if !ok {
		return WorktreeRef{}, fmt.Errorf("project %q is not registered with conductor", project)
	}

	name, wt, err := c.mgr.CreateWorktree(project, "", 0)
	if wt == nil {
		return WorktreeRef{}, fmt.Errorf("create worktree for %q: %w", project, err)
	}
	ref := WorktreeRef{
		Name:        name,
		Path:        wt.Path,
		Branch:      wt.Branch,
		RepoRoot:    proj.Path,
		Ports:       append([]int(nil), wt.Ports...),
		SetupStatus: string(wt.SetupStatus),
	}
	if err != nil {
		// The git worktree exists; only the setup script failed. Losing the
		// worktree over that would throw away a usable checkout, so the failure
		// is carried forward as a status instead.
		ref.SetupStatus = fmt.Sprintf("failed: %v", err)
	}
	return ref, nil
}

func (c *conductorWorktrees) RootPath(project string) (string, error) {
	proj, ok := c.cfg.GetProject(project)
	if !ok {
		return "", fmt.Errorf("project %q is not registered with conductor", project)
	}
	if proj.Path == "" {
		return "", fmt.Errorf("project %q has no path recorded", project)
	}
	return filepath.Clean(proj.Path), nil
}
