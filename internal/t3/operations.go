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
	err = c.withProtocol(ctx, func(p int) error {
		if p >= ProtocolV2 {
			return c.mutateProject(ctx, v2ProjectCreateMutation{
				Type:          "project.create",
				CommandID:     NewID(),
				ProjectID:     projectID,
				Title:         title,
				WorkspaceRoot: workspaceRoot,
			})
		}
		return c.Dispatch(ctx, ProjectCreateCommand{
			Type:          "project.create",
			CommandID:     NewID(),
			ProjectID:     projectID,
			Title:         title,
			WorkspaceRoot: workspaceRoot,
			CreatedAt:     Now(),
		})
	})
	if err != nil {
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
	type projectDefault struct {
		ID                    string          `json:"id"`
		DefaultModelSelection *ModelSelection `json:"defaultModelSelection"`
	}
	var detail struct {
		Projects []projectDefault `json:"projects"`
	}
	err := c.withProtocol(ctx, func(p int) error {
		if p >= ProtocolV2 {
			shell, err := c.fetchShellV2(ctx)
			if err != nil {
				return err
			}
			detail.Projects = nil
			for _, project := range shell.Projects {
				detail.Projects = append(detail.Projects, projectDefault{ID: project.ID, DefaultModelSelection: project.DefaultModelSelection})
			}
			return nil
		}
		return c.do(ctx, "GET", "/api/orchestration/shell", nil, &detail)
	})
	if err != nil {
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
	// Provisioning, when set, reports whether the worktree is still being
	// provisioned; the first-turn wait keeps going while it is.
	Provisioning func() bool
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
	err := c.withProtocol(ctx, func(p int) error {
		if p >= ProtocolV2 {
			return c.createThreadV2(ctx, threadID, opts)
		}
		return c.Dispatch(ctx, ThreadCreateCommand{
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
		})
	})
	if err != nil {
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
		if _, err := c.WaitForTurnWith(ctx, threadID, TurnWait{
			Timeout:      turnStartTimeout,
			MaxWait:      turnPrepareCap,
			Provisioning: opts.Provisioning,
		}); err != nil {
			return threadID, err
		}
	}
	return threadID, nil
}

// createThreadV2 opens the thread through orchestration.launchThread, bound to
// the worktree with the existing_worktree strategy.
//
// The first turn is deliberately *not* sent as the launch's initialMessage.
// Conductor holds the first turn until the worktree is provisioned (ReadyGate),
// and folding it into the launch would mean either holding the thread itself
// back for minutes or giving up the hold. Launching bare and dispatching the
// turn afterwards keeps V1's shape: the thread appears at once, the turn waits.
//
// Without a worktree path there is nothing to bind, so the thread runs at the
// project root.
func (c *Client) createThreadV2(ctx context.Context, threadID string, opts CreateThreadOptions) error {
	strategy := v2WorkspaceStrategy{Type: "root", Branch: opts.Branch}
	if opts.WorktreePath != "" {
		strategy = v2WorkspaceStrategy{Type: "existing_worktree", WorktreePath: opts.WorktreePath, Branch: opts.Branch}
	}
	launched, err := c.launchThreadV2(ctx, v2ThreadLaunchInput{
		CommandID:         NewID(),
		CreationSource:    v2CreationSource,
		ThreadID:          threadID,
		ProjectID:         opts.ProjectID,
		Title:             opts.Title,
		ModelSelection:    opts.Model,
		RuntimeMode:       opts.RuntimeMode,
		InteractionMode:   InteractionModeDefault,
		WorkspaceStrategy: strategy,
	})
	if err != nil {
		return err
	}
	if launched != threadID {
		return fmt.Errorf("T3 launched thread %s, not the requested %s", launched, threadID)
	}
	return nil
}

// turnStartTimeout bounds the wait for a turn to begin when nothing visible is
// happening. Starting a provider session spawns a process, so this is seconds
// rather than milliseconds.
const turnStartTimeout = 45 * time.Second

// turnPrepareCap bounds the wait while the thread or its worktree is visibly
// being prepared. On V2 a launched thread's first run sits in "preparing"
// until the project's setup script — `conductor adopt`, a database clone —
// has finished, and a remote clone takes minutes. Matches readyGateTimeout.
const turnPrepareCap = 20 * time.Minute

// TurnOutcome says how a dispatched turn was accepted.
type TurnOutcome int

const (
	// TurnStarted means a run for the message has begun.
	TurnStarted TurnOutcome = iota + 1
	// TurnJoinedActive means a run was already active when the message was
	// sent, and it was handed to that run. With deliveryIntent "auto" the
	// server decides how: steered into the running turn (seen live on
	// 0.0.46-nightly.20261003), or queued to start when it ends.
	TurnJoinedActive
)

// TurnWait tunes WaitForTurnWith.
type TurnWait struct {
	// Timeout is how long to wait while nothing shows any sign of progress.
	Timeout time.Duration
	// MaxWait caps the wait while the thread is visibly preparing (a run in
	// preparing, queued or starting) or Provisioning reports true. Each such
	// observation pushes the deadline out by Timeout, never beyond MaxWait.
	MaxWait time.Duration
	// Provisioning optionally reports that conductor's own provisioning of the
	// worktree is still in progress, which counts as preparing.
	Provisioning func() bool
}

// WaitForTurn blocks until a turn has begun on the thread, or reports why it
// did not. See WaitForTurnWith.
func (c *Client) WaitForTurn(ctx context.Context, threadID string, timeout time.Duration) error {
	_, err := c.WaitForTurnWith(ctx, threadID, TurnWait{Timeout: timeout, MaxWait: turnPrepareCap})
	return err
}

// WaitForTurnWith blocks until a turn has begun on the thread, or reports why
// it did not.
//
// Dispatching only means the command was accepted. The provider session
// starts afterwards and asynchronously, and when it refuses, the reason lands
// on the thread and nowhere else — no error comes back on the dispatch, and
// the turn simply never appears. Without this check conductor reports success
// for a thread that will never run.
//
// A preparing thread is not a stuck one. While a run is preparing, queued or
// starting — or the worktree is still being provisioned — the wait extends,
// up to MaxWait; only a failed run or a long silence is reported as failure.
// On V2 a message sent while another run was active is steered into it or
// queued behind it, and is reported as TurnJoinedActive once that run is seen
// running.
func (c *Client) WaitForTurnWith(ctx context.Context, threadID string, opts TurnWait) (TurnOutcome, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = turnStartTimeout
	}
	if opts.MaxWait < opts.Timeout {
		opts.MaxWait = opts.Timeout
	}
	defer c.clearBaseline(threadID)

	start := time.Now()
	deadline := start.Add(opts.Timeout)
	hardCap := start.Add(opts.MaxWait)
	var lastStatus string
	sawPreparing := false

	for {
		snapshot, err := c.Shell(ctx)
		if err != nil {
			return 0, fmt.Errorf("thread %s: its turn could not be verified: %w", threadID, err)
		}
		preparing := false
		if thread, ok := snapshot.FindThreadByID(threadID); ok {
			if thread.Session != nil {
				lastStatus = thread.Session.Status
			}
			baseline, hasBaseline := c.baseline(threadID)
			switch {
			case !hasBaseline || thread.RunID != baseline.runID:
				// V1, or a run newer than the one before this message: its
				// verdict is ours.
				if reason, failed := thread.Failed(); failed {
					return 0, fmt.Errorf("thread %s: its turn did not start: %s", threadID, reason)
				}
				if thread.Started() {
					return TurnStarted, nil
				}
				preparing = lastStatus == "starting"
			case baseline.active:
				// Still the run that was active when the message was sent; the
				// message was steered into it or queued behind it. Once that run
				// is underway the message is in good hands. A failure of that
				// run is not ours to report.
				switch lastStatus {
				case "running":
					return TurnJoinedActive, nil
				case "starting":
					preparing = true
				}
			}
		}
		if !preparing && opts.Provisioning != nil && opts.Provisioning() {
			preparing = true
		}

		now := time.Now()
		if preparing {
			sawPreparing = true
			if extended := now.Add(opts.Timeout); extended.After(deadline) {
				deadline = extended
				if deadline.After(hardCap) {
					deadline = hardCap
				}
			}
		}
		if now.After(deadline) {
			status := lastStatus
			if status == "" {
				status = "no provider session"
			}
			if sawPreparing {
				return 0, fmt.Errorf(
					"thread %s: no turn started within %s; it was still preparing (session: %s). Check the thread in T3",
					threadID, now.Sub(start).Round(time.Second), status)
			}
			return 0, fmt.Errorf(
				"thread %s: no turn started within %s (session: %s). Check the thread in T3",
				threadID, opts.Timeout, status)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// StartTurn submits chat input to a thread. This is the API equivalent of
// typing into the composer, and is what lets hermes drive a thread remotely.
//
// runtimeMode only applies to V1, where it rides on every turn. V2's
// message.dispatch has no such field: the thread's own runtime mode governs.
func (c *Client) StartTurn(ctx context.Context, threadID, text, runtimeMode string) error {
	if runtimeMode == "" {
		runtimeMode = RuntimeModeFullAccess
	}
	return c.withProtocol(ctx, func(p int) error {
		if p >= ProtocolV2 {
			return c.startTurnV2(ctx, threadID, text)
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
	})
}

// startTurnV2 dispatches message.dispatch.
//
// The thread is read first for two reasons: an older V2 server needs the
// client to pick the dispatch mode from the thread's active run, and
// WaitForTurn needs the run that was latest *before* this message, so it does
// not mistake the previous run's verdict for this one's.
func (c *Client) startTurnV2(ctx context.Context, threadID, text string) error {
	var activeRunID *string
	baseline := runBaseline{}
	if shell, err := c.fetchShellV2(ctx); err == nil {
		for _, t := range shell.Threads {
			if t.ID == threadID {
				activeRunID = t.ActiveRunID
				if t.LatestRunID != nil {
					baseline.runID = *t.LatestRunID
				}
				baseline.active = t.ActiveRunID != nil && *t.ActiveRunID != ""
			}
		}
	}
	command := buildMessageDispatchV2(threadID, text, c.serverResolvesCommandContext(), activeRunID)
	c.setBaseline(threadID, baseline)
	if err := c.dispatchV2(ctx, command); err != nil {
		c.clearBaseline(threadID)
		return err
	}
	return nil
}

// runBaseline is the thread's latest run when conductor dispatched a turn,
// and whether a run was active then. Empty runID means "no run yet", which
// matches a thread whose RunID is empty.
type runBaseline struct {
	runID  string
	active bool
}

// setBaseline, baseline and clearBaseline track the runBaseline per thread.
func (c *Client) setBaseline(threadID string, b runBaseline) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.baselines == nil {
		c.baselines = make(map[string]runBaseline)
	}
	c.baselines[threadID] = b
}

func (c *Client) baseline(threadID string) (runBaseline, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.baselines[threadID]
	return b, ok
}

func (c *Client) clearBaseline(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.baselines, threadID)
}

// ArchiveThread archives a thread, which also reaps its terminals.
func (c *Client) ArchiveThread(ctx context.Context, threadID string) error {
	return c.threadCommand(ctx, "thread.archive", threadID)
}

// DeleteThread permanently removes a thread.
func (c *Client) DeleteThread(ctx context.Context, threadID string) error {
	return c.threadCommand(ctx, "thread.delete", threadID)
}

// threadCommand sends a command whose only argument is the thread id. Both
// protocols spell these identically; only the transport differs.
func (c *Client) threadCommand(ctx context.Context, kind, threadID string) error {
	command := v2ThreadIDCommand{Type: kind, CommandID: NewID(), ThreadID: threadID}
	return c.withProtocol(ctx, func(p int) error {
		if p >= ProtocolV2 {
			return c.dispatchV2(ctx, command)
		}
		return c.Dispatch(ctx, command)
	})
}

// deleteProject removes a project on either protocol.
func (c *Client) deleteProject(ctx context.Context, projectID string) error {
	return c.withProtocol(ctx, func(p int) error {
		if p >= ProtocolV2 {
			return c.mutateProject(ctx, v2ProjectDeleteMutation{
				Type: "project.delete", CommandID: NewID(), ProjectID: projectID, Force: true,
			})
		}
		return c.Dispatch(ctx, ProjectDeleteCommand{
			Type: "project.delete", CommandID: NewID(), ProjectID: projectID, Force: true,
		})
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
	if err := c.deleteProject(ctx, project.ID); err != nil {
		return fmt.Errorf("thread archived but its project could not be removed: %w", err)
	}
	return nil
}
