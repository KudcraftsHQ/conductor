package t3

import (
	"encoding/json"
	"fmt"
	"strings"
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
	ID              string  `json:"id"`
	ProjectID       string  `json:"projectId"`
	Title           string  `json:"title"`
	Branch          *string `json:"branch"`
	WorktreePath    *string `json:"worktreePath"`
	ArchivedAt      *string `json:"archivedAt"`
	DeletedAt       *string `json:"deletedAt"`
	SettledAt       *string `json:"settledAt"`
	SettledOverride *string `json:"settledOverride"`
	// Activity blockers. These hold a thread active regardless of any override
	// or auto-settle rule, because work that is waiting on a human must stay
	// visible — and its dev server must stay up.
	HasPendingApprovals bool            `json:"hasPendingApprovals"`
	HasPendingUserInput bool            `json:"hasPendingUserInput"`
	ModelSelection      ModelSelection  `json:"modelSelection"`
	Session             *ThreadSession  `json:"session"`
	LatestTurn          json.RawMessage `json:"latestTurn"`
}

// Settled override values, from T3's own schema:
//
//	settledOverride: Schema.NullOr(Schema.Literals(["settled", "active"]))
//
// The override is a tri-state, not a bool: nil means "no explicit ruling, work
// it out". "active" is the keep-alive pin, which suppresses auto-settling until
// real activity clears it server-side.
const (
	SettledOverrideSettled = "settled"
	SettledOverrideActive  = "active"
)

// ThreadSession is the provider session bound to a thread.
//
// Status and LastError are the only place a rejected turn shows up. T3 accepts
// thread.turn.start on the command bus and starts the provider afterwards, so a
// turn that the provider refuses leaves a thread that looks created and simply
// never runs. Conductor has to read this to tell the two apart.
type ThreadSession struct {
	ThreadID           string  `json:"threadId"`
	Status             string  `json:"status"`
	ProviderName       *string `json:"providerName"`
	ProviderInstanceID string  `json:"providerInstanceId"`
	ActiveTurnID       *string `json:"activeTurnId"`
	LastError          *string `json:"lastError"`
}

// Failed reports whether the session errored, along with the server's reason.
func (t Thread) Failed() (string, bool) {
	if t.Session == nil || t.Session.Status != "error" {
		return "", false
	}
	if t.Session.LastError != nil && *t.Session.LastError != "" {
		return *t.Session.LastError, true
	}
	return "the provider session failed without reporting a reason", true
}

// Started reports whether a turn has actually begun on the thread.
func (t Thread) Started() bool {
	if len(t.LatestTurn) > 0 && string(t.LatestTurn) != "null" {
		return true
	}
	return t.Session != nil && t.Session.ActiveTurnID != nil
}

// Archived reports whether the thread is archived or deleted, which is how a
// closed window presents in the snapshot.
func (t Thread) Archived() bool {
	return t.ArchivedAt != nil || t.DeletedAt != nil
}

// Settled reports whether T3 considers this thread finished.
//
// The answer is the server's, not ours. Until T3 0.0.38 settling was derived in
// the client — packages/client-runtime/src/state/threadSettled.ts exported
// effectiveSettled(), and conductor carried a hand port of it plus a gh-backed
// PR lookup so it could reach the same verdict about a merged branch.
//
// Upstream f32f9a2f4 ("fix(server): settle threads server-side") moved that
// policy into ThreadSettlementReactor and deleted effectiveSettled() outright.
// The server now stamps settledAt and settledOverride itself, including the
// auto-settle-on-merge and inactivity arms conductor could only approximate. So
// the port is gone: reading the projected fields is both simpler and correct by
// construction, and it costs no gh round-trips.
//
// The one thing kept on this side is the activity guard below. It is not a
// duplicate of T3's policy — the server already refuses to *auto*-settle a
// thread that is running or blocked on a human — but an explicit settle can
// still land on a thread that is mid-turn, and conductor acts on this verdict by
// stopping a dev server. Pulling the server out from under a running session, or
// from under someone about to answer an approval prompt, is worse than leaving
// it up a while longer.
func (t Thread) Settled() bool {
	if t.HasPendingApprovals || t.HasPendingUserInput {
		return false
	}
	if t.Session != nil && (t.Session.Status == "starting" || t.Session.Status == "running") {
		return false
	}

	if t.SettledOverride != nil {
		switch *t.SettledOverride {
		case SettledOverrideSettled:
			return true
		case SettledOverrideActive:
			return false
		}
	}
	return t.SettledAt != nil
}

// Done reports whether a thread has stopped holding its worktree open, either
// because it is finished or because it is gone.
func (t Thread) Done() bool {
	return t.Archived() || t.Settled()
}

// Worktree returns the thread's worktree path, or "" when it has none.
func (t Thread) Worktree() string {
	if t.WorktreePath == nil {
		return ""
	}
	return *t.WorktreePath
}

// ModelSelection identifies a model on a configured provider instance.
//
// Options are the provider's per-model knobs — reasoning effort, context
// window, fast mode — and they matter as much as the model slug does. Dropping
// them is not neutral: T3 falls back to the model descriptor's own default,
// which for every current Claude model is effort "high". A thread conductor
// created therefore used to think harder (and cost more) than the same model
// picked by hand in the UI, with nothing in the payload to show why.
type ModelSelection struct {
	InstanceID string        `json:"instanceId"`
	Model      string        `json:"model"`
	Options    []ModelOption `json:"options,omitempty"`
}

// ModelOption is one provider option selection. Value is a string for select
// options ("medium", "1m") and a bool for toggles (fastMode), which is why it
// is not typed narrower.
type ModelOption struct {
	ID    string `json:"id"`
	Value any    `json:"value"`
}

// OptionsKey renders the options as a stable string, for comparison and
// display. Order is preserved rather than sorted: T3 emits them in descriptor
// order and round-tripping that unchanged keeps payloads diff-clean.
func (m ModelSelection) OptionsKey() string {
	if len(m.Options) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.Options))
	for _, option := range m.Options {
		parts = append(parts, fmt.Sprintf("%s=%v", option.ID, option.Value))
	}
	return strings.Join(parts, ",")
}

// String renders a selection the way CONDUCTOR_T3_MODEL accepts it, so what
// conductor prints can be pasted straight back in.
func (m ModelSelection) String() string {
	base := m.InstanceID + "/" + m.Model
	if options := m.OptionsKey(); options != "" {
		return base + "?" + strings.ReplaceAll(options, ",", "&")
	}
	return base
}

// WithoutOptions returns the selection stripped of its options. Options are
// scoped to a model — "effort=xhigh" on one model is not a value another
// necessarily offers — so they must not survive a model substitution.
func (m ModelSelection) WithoutOptions() ModelSelection {
	return ModelSelection{InstanceID: m.InstanceID, Model: m.Model}
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
