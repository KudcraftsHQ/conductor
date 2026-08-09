package t3

import (
	"time"

	"github.com/google/uuid"
)

// ShellSnapshot is the read model returned by GET /api/orchestration/shell.
// Only the fields conductor uses are declared; T3 sends considerably more.
type ShellSnapshot struct {
	SnapshotSequence int       `json:"snapshotSequence"`
	Projects         []Project `json:"projects"`
	Threads          []Thread  `json:"threads"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// Project is a T3 project: a workspace root plus its scripts.
type Project struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	WorkspaceRoot string  `json:"workspaceRoot"`
	Scripts       []any   `json:"scripts"`
	DeletedAt     *string `json:"deletedAt"`
}

// Thread is one agent conversation, optionally bound to a worktree.
type Thread struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"projectId"`
	Title        string  `json:"title"`
	Branch       *string `json:"branch"`
	WorktreePath *string `json:"worktreePath"`
	ArchivedAt   *string `json:"archivedAt"`
	DeletedAt    *string `json:"deletedAt"`
}

// Archived reports whether the thread is archived or deleted, which is how a
// closed window presents in the snapshot.
func (t Thread) Archived() bool {
	return t.ArchivedAt != nil || t.DeletedAt != nil
}

// Worktree returns the thread's worktree path, or "" when it has none.
func (t Thread) Worktree() string {
	if t.WorktreePath == nil {
		return ""
	}
	return *t.WorktreePath
}

// ModelSelection identifies a model on a configured provider instance.
type ModelSelection struct {
	InstanceID string `json:"instanceId"`
	Model      string `json:"model"`
}

// Runtime modes govern how much the agent may do without asking.
const (
	RuntimeModeApprovalRequired = "approval-required"
	RuntimeModeAutoAcceptEdits  = "auto-accept-edits"
	RuntimeModeAuto             = "auto"
	RuntimeModeFullAccess       = "full-access"
)

// Interaction modes select the agent's default posture.
const (
	InteractionModeDefault = "default"
	InteractionModePlan    = "plan"
)

// ThreadCreateCommand opens a new thread, optionally bound to a worktree.
//
// Branch and WorktreePath are encoded as explicit nulls when empty: the server
// schema is NullOr rather than optional, so omitting them is a decode error.
type ThreadCreateCommand struct {
	Type            string         `json:"type"`
	CommandID       string         `json:"commandId"`
	ThreadID        string         `json:"threadId"`
	ProjectID       string         `json:"projectId"`
	Title           string         `json:"title"`
	ModelSelection  ModelSelection `json:"modelSelection"`
	RuntimeMode     string         `json:"runtimeMode"`
	InteractionMode string         `json:"interactionMode"`
	Branch          *string        `json:"branch"`
	WorktreePath    *string        `json:"worktreePath"`
	CreatedAt       string         `json:"createdAt"`
}

// TurnMessage is the user message that starts a turn.
type TurnMessage struct {
	MessageID   string `json:"messageId"`
	Role        string `json:"role"`
	Text        string `json:"text"`
	Attachments []any  `json:"attachments"`
}

// ThreadTurnStartCommand submits chat input to a thread — the API equivalent
// of typing into the composer and pressing enter.
type ThreadTurnStartCommand struct {
	Type            string          `json:"type"`
	CommandID       string          `json:"commandId"`
	ThreadID        string          `json:"threadId"`
	Message         TurnMessage     `json:"message"`
	ModelSelection  *ModelSelection `json:"modelSelection,omitempty"`
	RuntimeMode     string          `json:"runtimeMode"`
	InteractionMode string          `json:"interactionMode"`
	CreatedAt       string          `json:"createdAt"`
}

// ThreadArchiveCommand archives a thread. Its terminals are reaped with it.
type ThreadArchiveCommand struct {
	Type      string `json:"type"`
	CommandID string `json:"commandId"`
	ThreadID  string `json:"threadId"`
}

// ThreadDeleteCommand permanently removes a thread.
type ThreadDeleteCommand struct {
	Type      string `json:"type"`
	CommandID string `json:"commandId"`
	ThreadID  string `json:"threadId"`
}

// ProjectCreateCommand registers a workspace root as a T3 project.
type ProjectCreateCommand struct {
	Type                         string `json:"type"`
	CommandID                    string `json:"commandId"`
	ProjectID                    string `json:"projectId"`
	Title                        string `json:"title"`
	WorkspaceRoot                string `json:"workspaceRoot"`
	CreateWorkspaceRootIfMissing bool   `json:"createWorkspaceRootIfMissing,omitempty"`
	CreatedAt                    string `json:"createdAt"`
}

// ProjectDeleteCommand removes a project. Conductor uses it to clean up the
// per-worktree project it created, so archived worktrees do not accumulate as
// dead entries in T3's sidebar.
type ProjectDeleteCommand struct {
	Type      string `json:"type"`
	CommandID string `json:"commandId"`
	ProjectID string `json:"projectId"`
	Force     bool   `json:"force,omitempty"`
}

// NewID returns a fresh identifier for commands, threads and messages.
func NewID() string { return uuid.NewString() }

// Now returns an RFC3339 timestamp in the format the server expects.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// ptr returns a pointer to v, or nil when v is empty.
func ptr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
