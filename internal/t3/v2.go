package t3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Orchestration V2 wire types and operations.
//
// Everything here is converted to the V1-shaped ShellSnapshot / Thread at the
// boundary, so the watcher, the TUI, reconciliation and the mux keep one model
// whichever server they are talking to. Field names follow
// packages/contracts/src/orchestrationV2.ts (OrchestrationV2ThreadShellJson,
// OrchestrationV2ShellSnapshotJson, OrchestrationV2ArchivedShellSnapshot).

// V2 WS methods, from ORCHESTRATION_V2_WS_METHODS.
const (
	MethodV2DispatchCommand          = "orchestration.dispatchCommand"
	MethodV2LaunchThread             = "orchestration.launchThread"
	MethodV2GetArchivedShellSnapshot = "orchestration.getArchivedShellSnapshot"
	MethodV2SubscribeShell           = "orchestration.subscribeShell"
	MethodV2SubscribeArchivedShell   = "orchestration.subscribeArchivedShell"
)

// v2ProjectsMutatePath is where V2 moved project.create / project.delete.
const v2ProjectsMutatePath = "/api/projects/mutate"

// v2CreationSource is what conductor reports as the origin of the threads and
// messages it creates. The schema's choices are web | mobile | mcp | provider |
// server; the server overrides createdBy to "user" for socket clients anyway,
// and "web" is what the reference client sends for a user-driven action.
const v2CreationSource = "web"

// v2Thread is OrchestrationV2ThreadShell as JSON. Only the fields conductor
// reads are declared.
type v2Thread struct {
	ID                 string         `json:"id"`
	ProjectID          string         `json:"projectId"`
	Title              string         `json:"title"`
	ProviderInstanceID string         `json:"providerInstanceId"`
	ModelSelection     ModelSelection `json:"modelSelection"`
	RuntimeMode        string         `json:"runtimeMode"`
	InteractionMode    string         `json:"interactionMode"`
	Branch             *string        `json:"branch"`
	WorktreePath       *string        `json:"worktreePath"`
	LatestRunID        *string        `json:"latestRunId"`
	LatestRunStartedAt *string        `json:"latestRunStartedAt"`
	ActiveRunID        *string        `json:"activeRunId"`
	// ActivityRunStatus: preparing | starting | running | waiting, or null.
	ActivityRunStatus *string `json:"activityRunStatus"`
	// Status: "idle" or the latest run's OrchestrationV2RunStatus.
	Status                string  `json:"status"`
	LastError             *string `json:"lastError"`
	PendingRuntimeRequest *struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"pendingRuntimeRequest"`
	ArchivedAt      *string `json:"archivedAt"`
	SettledOverride *string `json:"settledOverride"`
	SettledAt       *string `json:"settledAt"`
	DeletedAt       *string `json:"deletedAt"`
}

// v2Project is OrchestrationProjectShell. It has no deletedAt: a deleted
// project is simply absent.
type v2Project struct {
	ID                    string          `json:"id"`
	Title                 string          `json:"title"`
	WorkspaceRoot         string          `json:"workspaceRoot"`
	DefaultModelSelection *ModelSelection `json:"defaultModelSelection"`
	Scripts               []any           `json:"scripts"`
}

// v2ShellSnapshot is OrchestrationV2ShellSnapshotJson (GET /api/orchestration/shell).
type v2ShellSnapshot struct {
	SchemaVersion    int         `json:"schemaVersion"`
	SnapshotSequence int         `json:"snapshotSequence"`
	Projects         []v2Project `json:"projects"`
	Threads          []v2Thread  `json:"threads"`
	ArchivedThreads  []v2Thread  `json:"archivedThreads"`
}

// v2ArchivedShellSnapshot is OrchestrationV2ArchivedShellSnapshot.
type v2ArchivedShellSnapshot struct {
	SchemaVersion    int         `json:"schemaVersion"`
	SnapshotSequence int         `json:"snapshotSequence"`
	Projects         []v2Project `json:"projects"`
	Threads          []v2Thread  `json:"threads"`
}

// started reports whether a run has actually begun. A run that is still
// "preparing" (setup script, worktree checkout) or "queued" has not: the
// provider has not been asked to do anything yet, which is the V1 meaning of
// "no turn".
func (t v2Thread) started() bool {
	if t.LatestRunStartedAt != nil {
		return true
	}
	if t.ActivityRunStatus != nil && (*t.ActivityRunStatus == "running" || *t.ActivityRunStatus == "waiting") {
		return true
	}
	switch t.Status {
	case "running", "waiting", "completed", "interrupted", "cancelled", "rolled_back":
		return true
	}
	return false
}

// sessionStatus maps V2's run-centred status onto the V1 provider-session
// statuses conductor's Thread logic reads ("starting", "running", "error").
func (t v2Thread) sessionStatus() string {
	if t.ActivityRunStatus != nil {
		switch *t.ActivityRunStatus {
		case "preparing", "starting":
			return "starting"
		case "running", "waiting":
			return "running"
		}
	}
	switch t.Status {
	case "failed":
		return "error"
	case "preparing", "queued", "starting":
		return "starting"
	case "running", "waiting":
		return "running"
	}
	return "ready"
}

// toThread converts a V2 thread shell into conductor's Thread.
//
// settledAt and settledOverride carry over unchanged — V2 kept both names and
// the tri-state. The activity blockers are derived: V2 has one
// pendingRuntimeRequest instead of two booleans, and a "user_input" request is
// the one that means "waiting on an answer"; every other kind (provider
// approvals, auth refresh, dynamic tool calls) waits on a human decision too.
func (t v2Thread) toThread() Thread {
	thread := Thread{
		ID:              t.ID,
		ProjectID:       t.ProjectID,
		Title:           t.Title,
		Branch:          t.Branch,
		WorktreePath:    t.WorktreePath,
		ArchivedAt:      t.ArchivedAt,
		DeletedAt:       t.DeletedAt,
		SettledAt:       t.SettledAt,
		SettledOverride: t.SettledOverride,
		ModelSelection:  t.ModelSelection,
		Session: &ThreadSession{
			ThreadID:           t.ID,
			Status:             t.sessionStatus(),
			ProviderInstanceID: t.ProviderInstanceID,
			LastError:          t.LastError,
		},
	}
	if t.PendingRuntimeRequest != nil {
		if t.PendingRuntimeRequest.Kind == "user_input" {
			thread.HasPendingUserInput = true
		} else {
			thread.HasPendingApprovals = true
		}
	}
	if t.LatestRunID != nil {
		thread.RunID = *t.LatestRunID
	}
	if t.started() && t.LatestRunID != nil {
		thread.LatestTurn = json.RawMessage(fmt.Sprintf(`{"runId":%q}`, *t.LatestRunID))
	}
	return thread
}

func (p v2Project) toProject() Project {
	return Project{ID: p.ID, Title: p.Title, WorkspaceRoot: p.WorkspaceRoot, Scripts: p.Scripts}
}

// toShellSnapshot converts the V2 shell. archivedThreads is included when the
// server sent any (the HTTP route always sends []), and every archived thread
// is guaranteed an archivedAt so Thread.Archived() holds for it.
func (s v2ShellSnapshot) toShellSnapshot() *ShellSnapshot {
	out := &ShellSnapshot{SnapshotSequence: s.SnapshotSequence}
	for _, p := range s.Projects {
		out.Projects = append(out.Projects, p.toProject())
	}
	for _, t := range s.Threads {
		out.Threads = append(out.Threads, t.toThread())
	}
	for _, t := range s.ArchivedThreads {
		out.Threads = append(out.Threads, archivedThread(t))
	}
	return out
}

// archivedThread converts a thread read from an archive location, stamping
// archivedAt if the server left it null so the location is not lost.
func archivedThread(t v2Thread) Thread {
	thread := t.toThread()
	if thread.ArchivedAt == nil && thread.DeletedAt == nil {
		marker := "archived"
		thread.ArchivedAt = &marker
	}
	return thread
}

// fetchShellV2 reads the raw V2 shell over HTTP.
func (c *Client) fetchShellV2(ctx context.Context) (*v2ShellSnapshot, error) {
	var snapshot v2ShellSnapshot
	if err := c.doWith(ctx, http.MethodGet, "/api/orchestration/shell", protocolHeaders(ProtocolV2), nil, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// shellV2 is Shell for a V2 server: projects and active threads.
func (c *Client) shellV2(ctx context.Context) (*ShellSnapshot, error) {
	snapshot, err := c.fetchShellV2(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.toShellSnapshot(), nil
}

// fullSnapshotV2 is Snapshot for a V2 server: active and archived threads.
//
// V2 offers no single read of both: the active shell is HTTP, the archive is
// a socket RPC, and the one server query that returns both is not exposed. A
// thread moving between the two in between would be in neither, which reads
// exactly like a deletion — and the watcher tears a worktree down on
// deletion, irreversibly. Two defences, both needed:
//
//  1. The archive is read on both sides of the active read and every thread
//     seen in any of the three is kept, the latest read winning. A single
//     archive or unarchive inside the window cannot lose a thread.
//  2. Each read carries the server's application-event sequence (read in the
//     same transaction as the threads). Equal sequences on all three mean
//     nothing changed in between, so the union is an exact picture. If the
//     server keeps moving, the reads are retried a few times; a snapshot that
//     never settles is returned marked Inconsistent, which callers may show
//     but must not destroy anything on.
func (c *Client) fullSnapshotV2(ctx context.Context) (*ShellSnapshot, error) {
	conn, err := c.dial(ctx, ProtocolV2)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	var out *ShellSnapshot
	for attempt := 0; attempt < v2SnapshotAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 75 * time.Millisecond):
			}
		}
		before, err := conn.archivedShellV2(ctx)
		if err != nil {
			return nil, err
		}
		active, err := c.fetchShellV2(ctx)
		if err != nil {
			return nil, err
		}
		after, err := conn.archivedShellV2(ctx)
		if err != nil {
			return nil, err
		}
		out = mergeV2Snapshots(before, active, after)
		if !out.Inconsistent {
			return out, nil
		}
	}
	return out, nil
}

// v2SnapshotAttempts bounds how often fullSnapshotV2 re-reads a moving server.
const v2SnapshotAttempts = 4

// mergeV2Snapshots implements the three-read union described on
// fullSnapshotV2. Order of the result: active threads, then archived, each in
// the order the server sent them.
func mergeV2Snapshots(before *v2ArchivedShellSnapshot, active *v2ShellSnapshot, after *v2ArchivedShellSnapshot) *ShellSnapshot {
	out := &ShellSnapshot{SnapshotSequence: active.SnapshotSequence}
	for _, p := range active.Projects {
		out.Projects = append(out.Projects, p.toProject())
	}
	// Fail closed: a missing archive read, or any sequence that differs, means
	// the three reads may describe different moments.
	out.Inconsistent = before == nil || after == nil ||
		before.SnapshotSequence != active.SnapshotSequence ||
		after.SnapshotSequence != active.SnapshotSequence

	type entry struct {
		thread Thread
		rank   int
	}
	latest := make(map[string]entry)
	var order []string
	put := func(t Thread, rank int) {
		prev, seen := latest[t.ID]
		if !seen {
			order = append(order, t.ID)
		}
		if !seen || rank >= prev.rank {
			latest[t.ID] = entry{thread: t, rank: rank}
		}
	}
	if before != nil {
		for _, t := range before.Threads {
			put(archivedThread(t), 0)
		}
	}
	for _, t := range active.Threads {
		put(t.toThread(), 1)
	}
	for _, t := range active.ArchivedThreads {
		put(archivedThread(t), 1)
	}
	if after != nil {
		for _, t := range after.Threads {
			put(archivedThread(t), 2)
		}
	}

	// Active first, then archived, preserving first-seen order within each.
	for _, id := range order {
		if t := latest[id].thread; !t.Archived() {
			out.Threads = append(out.Threads, t)
		}
	}
	for _, id := range order {
		if t := latest[id].thread; t.Archived() {
			out.Threads = append(out.Threads, t)
		}
	}
	return out
}

// archivedShellV2 reads the archive over an open V2 socket.
func (c *Conn) archivedShellV2(ctx context.Context) (*v2ArchivedShellSnapshot, error) {
	var snapshot v2ArchivedShellSnapshot
	if err := c.Call(ctx, MethodV2GetArchivedShellSnapshot, map[string]any{}, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// dispatchV2 sends one OrchestrationV2Command over the socket.
func (c *Client) dispatchV2(ctx context.Context, command any) error {
	conn, err := c.dial(ctx, ProtocolV2)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var result struct {
		Sequence int `json:"sequence"`
	}
	return conn.Call(ctx, MethodV2DispatchCommand, command, &result)
}

// mutateProject sends one ProjectMutation over HTTP. V2 has no project
// commands on the orchestration bus.
func (c *Client) mutateProject(ctx context.Context, mutation any) error {
	return c.do(ctx, http.MethodPost, v2ProjectsMutatePath, mutation, nil)
}

// --- V2 commands ---

// v2ThreadIDCommand covers thread.archive, thread.unarchive and thread.delete.
type v2ThreadIDCommand struct {
	Type      string `json:"type"`
	CommandID string `json:"commandId"`
	ThreadID  string `json:"threadId"`
}

// v2DispatchMode is the message.dispatch dispatchMode union.
type v2DispatchMode struct {
	Type        string `json:"type"`
	TargetRunID string `json:"targetRunId,omitempty"`
}

// v2MessageDispatchCommand is the message.dispatch member of
// OrchestrationV2Command. createdBy and creationSource are required by the
// schema even though the server overrides createdBy for socket clients.
type v2MessageDispatchCommand struct {
	Type           string          `json:"type"`
	CommandID      string          `json:"commandId"`
	CreatedBy      string          `json:"createdBy"`
	CreationSource string          `json:"creationSource"`
	ThreadID       string          `json:"threadId"`
	MessageID      string          `json:"messageId"`
	Text           string          `json:"text"`
	Attachments    []any           `json:"attachments"`
	ModelSelection *ModelSelection `json:"modelSelection,omitempty"`
	DeliveryIntent string          `json:"deliveryIntent,omitempty"`
	DispatchMode   v2DispatchMode  `json:"dispatchMode"`
}

// v2WorkspaceStrategy is OrchestrationV2ThreadLaunchWorkspaceStrategy.
type v2WorkspaceStrategy struct {
	Type            string `json:"type"`
	WorktreePath    string `json:"worktreePath,omitempty"`
	Branch          string `json:"branch,omitempty"`
	BaseRef         string `json:"baseRef,omitempty"`
	StartFromOrigin *bool  `json:"startFromOrigin,omitempty"`
}

// v2LaunchMessage is the optional initialMessage of a launch.
type v2LaunchMessage struct {
	MessageID   string `json:"messageId,omitempty"`
	Text        string `json:"text"`
	Attachments []any  `json:"attachments"`
}

// v2ThreadLaunchInput is OrchestrationV2ThreadLaunchInput.
type v2ThreadLaunchInput struct {
	CommandID         string              `json:"commandId"`
	CreationSource    string              `json:"creationSource,omitempty"`
	ThreadID          string              `json:"threadId,omitempty"`
	ProjectID         string              `json:"projectId"`
	Title             string              `json:"title"`
	ModelSelection    ModelSelection      `json:"modelSelection"`
	RuntimeMode       string              `json:"runtimeMode"`
	InteractionMode   string              `json:"interactionMode"`
	WorkspaceStrategy v2WorkspaceStrategy `json:"workspaceStrategy"`
	InitialMessage    *v2LaunchMessage    `json:"initialMessage,omitempty"`
}

// v2ThreadLaunchResult is the part of OrchestrationV2ThreadLaunchResult
// conductor reads; the projection is large and ignored.
type v2ThreadLaunchResult struct {
	ThreadID string `json:"threadId"`
	Resumed  bool   `json:"resumed"`
}

// v2ProjectCreateMutation and v2ProjectDeleteMutation are ProjectMutation.
type v2ProjectCreateMutation struct {
	Type          string `json:"type"`
	CommandID     string `json:"commandId"`
	ProjectID     string `json:"projectId"`
	Title         string `json:"title"`
	WorkspaceRoot string `json:"workspaceRoot"`
}

type v2ProjectDeleteMutation struct {
	Type      string `json:"type"`
	CommandID string `json:"commandId"`
	ProjectID string `json:"projectId"`
	Force     bool   `json:"force,omitempty"`
}

// launchThreadV2 opens a thread through orchestration.launchThread.
//
// The thread id is chosen here rather than left to the server, so a launch
// whose reply is lost still leaves conductor knowing which thread it made.
func (c *Client) launchThreadV2(ctx context.Context, input v2ThreadLaunchInput) (string, error) {
	conn, err := c.dial(ctx, ProtocolV2)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	var result v2ThreadLaunchResult
	if err := conn.Call(ctx, MethodV2LaunchThread, input, &result); err != nil {
		return "", err
	}
	if result.ThreadID == "" {
		return input.ThreadID, nil
	}
	return result.ThreadID, nil
}

// buildMessageDispatchV2 builds message.dispatch for a thread.
//
// Servers that resolve command context themselves (capability
// serverResolvedCommandContext) are told start_immediately with
// deliveryIntent "auto" and pick steer/queue/restart against their own state —
// that is what the reference client does. Older V2 servers need the client to
// choose: start when nothing is running, otherwise queue behind the active
// run, which is the one mode every provider supports.
func buildMessageDispatchV2(threadID, text string, serverResolves bool, activeRunID *string) v2MessageDispatchCommand {
	command := v2MessageDispatchCommand{
		Type:           "message.dispatch",
		CommandID:      NewID(),
		CreatedBy:      "user",
		CreationSource: v2CreationSource,
		ThreadID:       threadID,
		MessageID:      NewID(),
		Text:           text,
		Attachments:    []any{},
		DispatchMode:   v2DispatchMode{Type: "start_immediately"},
	}
	switch {
	case serverResolves:
		command.DeliveryIntent = "auto"
	case activeRunID != nil:
		command.DispatchMode = v2DispatchMode{Type: "queue_after_active"}
	}
	return command
}
