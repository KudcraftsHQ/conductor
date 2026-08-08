package t3

import (
	"context"
	"fmt"
	"strings"
)

// DefaultModel is used when a project has no default model selection and the
// caller did not name one. T3 requires a model on thread.create.
var DefaultModel = ModelSelection{InstanceID: "claude-code", Model: "claude-opus-5"}

// FindProjectByRoot returns the project whose workspace root is exactly root.
func (s *ShellSnapshot) FindProjectByRoot(root string) (*Project, bool) {
	for i := range s.Projects {
		if s.Projects[i].DeletedAt != nil {
			continue
		}
		if pathsEqual(s.Projects[i].WorkspaceRoot, root) {
			return &s.Projects[i], true
		}
	}
	return nil, false
}

// FindProjectByTitle returns the project with the given title.
func (s *ShellSnapshot) FindProjectByTitle(title string) (*Project, bool) {
	for i := range s.Projects {
		if s.Projects[i].DeletedAt != nil {
			continue
		}
		if s.Projects[i].Title == title {
			return &s.Projects[i], true
		}
	}
	return nil, false
}

// FindThreadByWorktree returns the live thread bound to worktreePath.
//
// The worktree path is the join key between conductor and T3: conductor owns
// it, and T3 stores it verbatim on the thread. Titles are unsuitable because
// T3 rewrites them from the conversation.
func (s *ShellSnapshot) FindThreadByWorktree(worktreePath string) (*Thread, bool) {
	for i := range s.Threads {
		t := &s.Threads[i]
		if t.Archived() {
			continue
		}
		if t.Worktree() != "" && pathsEqual(t.Worktree(), worktreePath) {
			return t, true
		}
	}
	return nil, false
}

// LiveThreadsWithWorktrees returns every non-archived thread bound to a
// worktree. The port reconciler uses this to find worktrees whose thread has
// gone away.
func (s *ShellSnapshot) LiveThreadsWithWorktrees() []Thread {
	var out []Thread
	for _, t := range s.Threads {
		if !t.Archived() && t.Worktree() != "" {
			out = append(out, t)
		}
	}
	return out
}

// pathsEqual compares filesystem paths, tolerating a trailing separator.
func pathsEqual(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// EnsureProject returns the id of the project rooted at workspaceRoot,
// creating it if T3 does not know about it yet.
func (c *Client) EnsureProject(ctx context.Context, title, workspaceRoot string) (string, error) {
	snapshot, err := c.Shell(ctx)
	if err != nil {
		return "", err
	}
	if project, ok := snapshot.FindProjectByRoot(workspaceRoot); ok {
		return project.ID, nil
	}

	projectID := NewID()
	command := ProjectCreateCommand{
		Type:          "project.create",
		CommandID:     NewID(),
		ProjectID:     projectID,
		Title:         title,
		WorkspaceRoot: workspaceRoot,
		CreatedAt:     Now(),
	}
	if err := c.Dispatch(ctx, command); err != nil {
		return "", fmt.Errorf("failed to create T3 project %q: %w", title, err)
	}
	return projectID, nil
}

// ProjectModel returns the project's default model, falling back to
// DefaultModel when it has none configured.
func (c *Client) ProjectModel(ctx context.Context, projectID string) ModelSelection {
	var detail struct {
		Projects []struct {
			ID                    string          `json:"id"`
			DefaultModelSelection *ModelSelection `json:"defaultModelSelection"`
		} `json:"projects"`
	}
	if err := c.do(ctx, "GET", "/api/orchestration/shell", nil, &detail); err == nil {
		for _, p := range detail.Projects {
			if p.ID == projectID && p.DefaultModelSelection != nil && p.DefaultModelSelection.Model != "" {
				return *p.DefaultModelSelection
			}
		}
	}
	return DefaultModel
}

// CreateThreadOptions describes a thread to open against a worktree.
type CreateThreadOptions struct {
	ProjectID    string
	Title        string
	Branch       string
	WorktreePath string
	Model        ModelSelection
	RuntimeMode  string
	// TaskPrompt, when set, is submitted as the thread's first turn.
	TaskPrompt string
}

// CreateThread opens a thread bound to a worktree and, when a task prompt is
// given, starts its first turn.
func (c *Client) CreateThread(ctx context.Context, opts CreateThreadOptions) (string, error) {
	if opts.ProjectID == "" {
		return "", fmt.Errorf("a project id is required to create a T3 thread")
	}
	if opts.RuntimeMode == "" {
		opts.RuntimeMode = RuntimeModeFullAccess
	}
	if opts.Model.Model == "" {
		opts.Model = DefaultModel
	}

	threadID := NewID()
	create := ThreadCreateCommand{
		Type:            "thread.create",
		CommandID:       NewID(),
		ThreadID:        threadID,
		ProjectID:       opts.ProjectID,
		Title:           opts.Title,
		ModelSelection:  opts.Model,
		RuntimeMode:     opts.RuntimeMode,
		InteractionMode: InteractionModeDefault,
		Branch:          ptr(opts.Branch),
		WorktreePath:    ptr(opts.WorktreePath),
		CreatedAt:       Now(),
	}
	if err := c.Dispatch(ctx, create); err != nil {
		return "", fmt.Errorf("failed to create T3 thread %q: %w", opts.Title, err)
	}

	if opts.TaskPrompt != "" {
		if err := c.StartTurn(ctx, threadID, opts.TaskPrompt, opts.RuntimeMode); err != nil {
			// The thread exists; report the failure without pretending it does not.
			return threadID, fmt.Errorf("thread created but its first turn failed: %w", err)
		}
	}
	return threadID, nil
}

// StartTurn submits chat input to a thread. This is the API equivalent of
// typing into the composer, and is what lets hermes drive a thread remotely.
func (c *Client) StartTurn(ctx context.Context, threadID, text, runtimeMode string) error {
	if runtimeMode == "" {
		runtimeMode = RuntimeModeFullAccess
	}
	command := ThreadTurnStartCommand{
		Type:      "thread.turn.start",
		CommandID: NewID(),
		ThreadID:  threadID,
		Message: TurnMessage{
			MessageID:   NewID(),
			Role:        "user",
			Text:        text,
			Attachments: []any{},
		},
		RuntimeMode:     runtimeMode,
		InteractionMode: InteractionModeDefault,
		CreatedAt:       Now(),
	}
	return c.Dispatch(ctx, command)
}

// ArchiveThread archives a thread, which also reaps its terminals.
func (c *Client) ArchiveThread(ctx context.Context, threadID string) error {
	return c.Dispatch(ctx, ThreadArchiveCommand{
		Type:      "thread.archive",
		CommandID: NewID(),
		ThreadID:  threadID,
	})
}

// CloseWorktree archives the thread bound to worktreePath and, when conductor
// created a project rooted at that same worktree, deletes the project too.
//
// Without the second step every archived worktree would leave a dead project
// behind in T3's sidebar, since a project outlives the threads inside it.
func (c *Client) CloseWorktree(ctx context.Context, worktreePath string) error {
	snapshot, err := c.Shell(ctx)
	if err != nil {
		return err
	}

	thread, ok := snapshot.FindThreadByWorktree(worktreePath)
	if !ok {
		RemoveMarker(worktreePath)
		return nil // Already closed.
	}
	if err := c.ArchiveThread(ctx, thread.ID); err != nil {
		return err
	}
	RemoveMarker(worktreePath)

	// Only remove a project conductor made *for* this worktree. A project
	// rooted at the main repository belongs to the user and must survive.
	project, ok := snapshot.FindProjectByRoot(worktreePath)
	if !ok || project.ID != thread.ProjectID {
		return nil
	}
	if err := c.Dispatch(ctx, ProjectDeleteCommand{
		Type:      "project.delete",
		CommandID: NewID(),
		ProjectID: project.ID,
		Force:     true,
	}); err != nil {
		return fmt.Errorf("thread archived but its project could not be removed: %w", err)
	}
	return nil
}
