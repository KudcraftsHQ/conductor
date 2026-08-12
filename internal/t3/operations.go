package t3

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// PreferredModel is conductor's opening bid when nothing else names one, not a
// fallback to use blindly.
//
// It is only ever offered to SelectModel as the lowest-priority candidate, and
// is discarded if this build does not have that instance. There is deliberately
// no unvalidated default: conductor used to ship one ('claude-code', which no
// build has) and it silently broke every thread it created. See providers.go.
var PreferredModel = ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"}

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

// FindThreadsByWorktree returns every live thread bound to worktreePath.
//
// One worktree can carry several threads: T3 reuses an existing worktree when a
// new thread picks a branch that already has one, and its own delete flow only
// offers to remove a directory once the last thread on it goes. Conductor has
// to count them the same way or it will tear a worktree down while other
// threads are still working in it.
func (s *ShellSnapshot) FindThreadsByWorktree(worktreePath string) []Thread {
	var out []Thread
	for i := range s.Threads {
		t := s.Threads[i]
		if t.Archived() {
			continue
		}
		if t.Worktree() != "" && pathsEqual(t.Worktree(), worktreePath) {
			out = append(out, t)
		}
	}
	return out
}

// ThreadIDsByWorktree returns the ids of every thread in the snapshot bound to
// worktreePath, whatever its state. Callers use it to record the binding, so an
// archived thread counts: it still holds the worktree open.
func (s *ShellSnapshot) ThreadIDsByWorktree(worktreePath string) []string {
	var ids []string
	for i := range s.Threads {
		t := s.Threads[i]
		if t.Worktree() != "" && pathsEqual(t.Worktree(), worktreePath) {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// FindThreadByID returns a thread by id whatever its state, so callers can tell
// "archived" apart from "T3 has not applied the create yet". Those mean opposite
// things and the snapshot's absence alone cannot distinguish them.
func (s *ShellSnapshot) FindThreadByID(threadID string) (*Thread, bool) {
	for i := range s.Threads {
		if s.Threads[i].ID == threadID {
			return &s.Threads[i], true
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

// ProjectDefaultModels returns the default model selection of the named
// projects, in the order given, skipping any that have none.
//
// A project's default is a preference and not a guarantee: most of the projects
// on this machine default to an instance that is configured but disabled, so
// these are candidates for SelectModel to validate, never answers.
func (c *Client) ProjectDefaultModels(ctx context.Context, projectIDs ...string) []ModelSelection {
	var detail struct {
		Projects []struct {
			ID                    string          `json:"id"`
			DefaultModelSelection *ModelSelection `json:"defaultModelSelection"`
		} `json:"projects"`
	}
	if err := c.do(ctx, "GET", "/api/orchestration/shell", nil, &detail); err != nil {
		return nil
	}
	var out []ModelSelection
	for _, wanted := range projectIDs {
		for _, p := range detail.Projects {
			if p.ID == wanted && p.DefaultModelSelection != nil && p.DefaultModelSelection.Model != "" {
				out = append(out, *p.DefaultModelSelection)
			}
		}
	}
	return out
}

// ResolveModelSelection picks a model selection that this T3 build can actually
// run, preferring the caller's hints.
//
// Order of preference: the CONDUCTOR_T3_MODEL override, then the caller's hints
// (typically the worktree's project default, then the parent repository's),
// then whatever T3's own working threads are using, then conductor's preferred
// instance. Every candidate is checked against the live provider registry, so
// the returned selection is one a turn can start on.
func (c *Client) ResolveModelSelection(ctx context.Context, hints ...ModelSelection) (ModelSelection, error) {
	instances, err := c.ProviderInstances(ctx)
	if err != nil {
		return ModelSelection{}, err
	}

	// An override is an instruction, not a hint. Quietly substituting something
	// else for it would hide the very mistake it exists to work around, so an
	// unusable one is an error rather than a candidate to fall past.
	if override, ok := ModelSelectionFromEnv(); ok {
		if invalid := ValidateModelSelection(instances, override); invalid != nil {
			return ModelSelection{}, fmt.Errorf("CONDUCTOR_T3_MODEL=%s cannot be used: %w",
				override, invalid)
		}
		return override, nil
	}

	var candidates []ModelSelection
	candidates = append(candidates, hints...)
	if snapshot, err := c.Shell(ctx); err == nil {
		candidates = append(candidates, snapshot.SelectionsInUse()...)
	}
	candidates = append(candidates, PreferredModel)

	return SelectModel(instances, candidates)
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
	// ReadyGate, when set, must return before the first turn is submitted.
	//
	// Telling an agent to wait is not the same as it waiting: the instruction
	// lives in a context file it may or may not act on, and by the time it
	// reads it the turn has already begun. Holding the turn instead removes the
	// race rather than documenting it — an agent cannot get ahead of work that
	// was never dispatched. A gate that fails does not stop the thread being
	// created; the error is reported and the turn is sent anyway, because a
	// thread with no turn is harder to recover from than an early one.
	ReadyGate func(context.Context) error
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
	// Resolve before creating, not after. thread.create accepts an instance id
	// the build does not have and only fails when the first turn tries to start,
	// by which point the thread exists and looks fine — so an unrunnable
	// selection has to be rejected here, while there is nothing to clean up.
	if opts.Model.InstanceID == "" || opts.Model.Model == "" {
		resolved, err := c.ResolveModelSelection(ctx)
		if err != nil {
			return "", err
		}
		opts.Model = resolved
	} else if instances, err := c.ProviderInstances(ctx); err == nil {
		if invalid := ValidateModelSelection(instances, opts.Model); invalid != nil {
			return "", fmt.Errorf("cannot create T3 thread %q: %w", opts.Title, invalid)
		}
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
		if opts.ReadyGate != nil {
			if err := opts.ReadyGate(ctx); err != nil {
				fmt.Fprintf(os.Stderr,
					"warning: starting the first turn before %s is ready: %v\n", opts.WorktreePath, err)
			}
		}
		if err := c.StartTurn(ctx, threadID, opts.TaskPrompt, opts.RuntimeMode); err != nil {
			// The thread exists; report the failure without pretending it does not.
			return threadID, fmt.Errorf("thread created but its first turn failed: %w", err)
		}
		if err := c.WaitForTurn(ctx, threadID, turnStartTimeout); err != nil {
			return threadID, err
		}
	}
	return threadID, nil
}

// turnStartTimeout bounds the wait for a turn to begin. Starting a provider
// session spawns a process, so this is seconds rather than milliseconds.
const turnStartTimeout = 45 * time.Second

// WaitForTurn blocks until a turn has begun on the thread, or reports why it
// did not.
//
// Dispatching thread.turn.start only means the command bus accepted it. The
// provider session starts afterwards and asynchronously, and when it refuses,
// the reason lands in thread.session.lastError and nowhere else — no error
// comes back on the dispatch, and the turn simply never appears. Without this
// check conductor reports success for a thread that will never run, which is
// exactly how an unknown provider instance stayed hidden for so long.
func (c *Client) WaitForTurn(ctx context.Context, threadID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastSessionStatus string

	for {
		snapshot, err := c.Shell(ctx)
		if err != nil {
			return fmt.Errorf("thread %s created but its turn could not be verified: %w", threadID, err)
		}
		for i := range snapshot.Threads {
			thread := snapshot.Threads[i]
			if thread.ID != threadID {
				continue
			}
			if reason, failed := thread.Failed(); failed {
				return fmt.Errorf("thread %s created but its turn did not start: %s", threadID, reason)
			}
			if thread.Started() {
				return nil
			}
			if thread.Session != nil {
				lastSessionStatus = thread.Session.Status
			}
		}

		if time.Now().After(deadline) {
			status := lastSessionStatus
			if status == "" {
				status = "no provider session"
			}
			return fmt.Errorf(
				"thread %s created but no turn started within %s (session: %s). Check the thread in T3",
				threadID, timeout, status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
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

	threads := snapshot.FindThreadsByWorktree(worktreePath)
	if len(threads) == 0 {
		RemoveMarker(worktreePath)
		return nil // Already closed.
	}
	// Every thread on the worktree is archived, not just the first. Leaving one
	// live would point an agent at a tree that is about to be removed.
	for _, thread := range threads {
		if err := c.ArchiveThread(ctx, thread.ID); err != nil {
			return err
		}
	}
	RemoveMarker(worktreePath)

	// Only remove a project conductor made *for* this worktree. A project
	// rooted at the main repository belongs to the user and must survive.
	project, ok := snapshot.FindProjectByRoot(worktreePath)
	if !ok || project.ID != threads[0].ProjectID {
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
