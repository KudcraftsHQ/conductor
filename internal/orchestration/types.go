// Package orchestration implements the Conductor side of the
// Conductor -> Herdr -> Hermes contract: a chat message asks for work on a
// registered project, and Conductor answers with a *running agent in a fresh
// worktree*, plus enough persisted state that the monitoring side can report
// real progress and a single honest completion.
//
// Three things this package refuses to do, because each was a real failure:
//
//   - Run the agent in the project's root checkout. A request that lands in the
//     root checkout edits the branch the human is on. Launch always creates a
//     new worktree and asserts the result is not the root.
//   - Block the caller until the work is done. The caller is a Discord
//     interaction watching a spinner. Launch has a budget to *confirm the agent
//     started*; it never has a budget to watch it run.
//   - Invent progress. Every progress event carries an observation read back
//     from Herdr — a status, a state-change sequence. Nothing here ticks a
//     timer and calls the tick progress.
package orchestration

import "time"

// TaskState is what Conductor believes is true about a task right now.
//
// It is deliberately coarse: the fine-grained truth lives in Herdr, and this
// package's job is to remember only what Herdr cannot (which worktree, which
// originating message, which gate decision) plus the last thing it observed.
type TaskState string

const (
	// StateLaunching means a launch is in flight: a worktree and pane are
	// being created. Persisted *before* the slow work so a crash mid-launch is
	// recoverable rather than invisible.
	StateLaunching TaskState = "launching"
	// StateUnconfirmed means the pane exists and the prompt was delivered, but
	// the agent was not observed moving within the confirm budget. An honest
	// admission, never reported as a started task.
	StateUnconfirmed TaskState = "dispatch_unconfirmed"
	// StateWorking means the agent has been observed working since dispatch.
	StateWorking TaskState = "working"
	// StateBlocked means the agent is waiting on a human.
	StateBlocked TaskState = "blocked"
	// StateAgentDone means the agent reached a settled state after having
	// worked. It is *not* the same as a completed task: the readback gate may
	// still be open.
	StateAgentDone TaskState = "agent_done"
	// StateAwaitingReadbackDecision means the agent finished but nobody has
	// said whether this task owes a Readback document. The agent and worktree
	// are untouched; a question has gone to the requester.
	StateAwaitingReadbackDecision TaskState = "awaiting_readback_decision"
	// StateAwaitingReadbackPublish means the gate is required and no published
	// document has been recorded yet.
	StateAwaitingReadbackPublish TaskState = "awaiting_readback_publish"
	// StateCompleted is terminal and user-facing: the completion summary has
	// been produced.
	StateCompleted TaskState = "completed"
	// StateAgentLost means the pane that hosted this task is gone or now hosts
	// a different terminal. Recorded, never silently relaunched.
	StateAgentLost TaskState = "agent_lost"
	// StateFailed means the launch itself failed. A duplicate of the
	// originating message may retry it.
	StateFailed TaskState = "failed"
)

// Terminal reports whether no further agent progress is expected.
func (s TaskState) Terminal() bool {
	switch s {
	case StateCompleted, StateAgentLost, StateFailed:
		return true
	}
	return false
}

// ReadbackGate is the *explicit* answer to "does this task owe a document?".
//
// It exists because publishing was previously implicit: a task that asked for
// research finished with its note in /tmp, and a task that only changed code
// was never asked about at all. Making it a persisted three-way state means a
// restart does not forget that a question is outstanding, and a re-ask does not
// pester the requester twice.
type ReadbackGate string

const (
	// GateUndecided — the request did not make it clear. The requester is
	// asked, once, when the agent finishes. Never a reason to block the agent.
	GateUndecided ReadbackGate = "awaiting_readback_decision"
	// GateNotNeeded — clearly code-only work; it may complete without a
	// document.
	GateNotNeeded ReadbackGate = "no_readback_needed"
	// GateRequired — the request asked for a report, research or an explicit
	// Readback; completion waits for the published document.
	GateRequired ReadbackGate = "readback_required"
)

// TestStatus is what is known about the task's tests. "unknown" is the honest
// default: Conductor does not run the agent's tests for it, and reporting
// "passed" because nothing said otherwise is exactly the kind of claim this
// contract exists to prevent.
type TestStatus string

const (
	TestsUnknown TestStatus = "unknown"
	TestsPassed  TestStatus = "passed"
	TestsFailed  TestStatus = "failed"
	TestsSkipped TestStatus = "skipped"
)

// ProgressEvent is one *observed* change. Every event carries the Herdr
// observation that justified it; there is no constructor that makes one from a
// timer.
type ProgressEvent struct {
	At time.Time `json:"at"`
	// Kind is "launched", "status", "gate", "readback", "tests", "completed",
	// "lost" or "error".
	Kind string `json:"kind"`
	// Status is the agent status Herdr reported, when the event came from an
	// agent observation.
	Status string `json:"status,omitempty"`
	// StateChangeSeq is Herdr's monotonic per-pane counter at observation time.
	// It is what makes "the agent did something" checkable rather than assumed.
	StateChangeSeq int64  `json:"stateChangeSeq,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

// AgentRef is the Herdr coordinate of a task's agent. TerminalID is the part
// that matters for recovery: a pane id can be reused by a different terminal,
// and reporting that terminal's output as this task's progress would be a lie.
type AgentRef struct {
	WorkspaceID string `json:"workspaceId,omitempty"`
	PaneID      string `json:"paneId,omitempty"`
	TerminalID  string `json:"terminalId,omitempty"`
	AgentName   string `json:"agentName,omitempty"`
	Agent       string `json:"agent,omitempty"`
}

// WorktreeRef is the isolated checkout the agent runs in.
type WorktreeRef struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	RepoRoot string `json:"repoRoot"`
	Ports    []int  `json:"ports,omitempty"`
	// SetupStatus is the project setup script's outcome. A failed setup does
	// not stop the launch — the agent can install what it needs — but it is
	// reported rather than hidden, because "the tests won't run" usually
	// starts here.
	SetupStatus string `json:"setupStatus,omitempty"`
}

// ReadbackRef is the publishing metadata a completion quotes. The URL is only
// ever what the publisher reported; this package never builds one from a slug.
type ReadbackRef struct {
	Slug        string    `json:"slug,omitempty"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"publishedAt,omitempty"`
}

// Origin identifies the chat message that asked for the work. RequestID is the
// idempotency key: the same originating message must never produce a second
// worktree or a second agent.
type Origin struct {
	RequestID   string `json:"requestId"`
	Platform    string `json:"platform,omitempty"`
	ChannelID   string `json:"channelId,omitempty"`
	ThreadID    string `json:"threadId,omitempty"`
	RequesterID string `json:"requesterId,omitempty"`
}

// Task is the whole persisted record for one orchestrated request.
type Task struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Prompt  string `json:"prompt"`

	Origin   Origin      `json:"origin"`
	Worktree WorktreeRef `json:"worktree"`
	Agent    AgentRef    `json:"agent"`

	State  TaskState `json:"state"`
	Detail string    `json:"detail,omitempty"`

	Gate ReadbackGate `json:"readbackGate"`
	// GateDecidedBy records how the gate reached its current value:
	// "classifier" or "user". A user decision is never overwritten by a
	// re-classification.
	GateDecidedBy string `json:"readbackGateDecidedBy,omitempty"`
	// GateQuestionSentAt is set the first time the requester is asked. It is
	// what stops a restart, or a second observation, from asking again.
	GateQuestionSentAt time.Time `json:"readbackQuestionSentAt,omitempty"`

	Readback   ReadbackRef `json:"readback,omitempty"`
	Tests      TestStatus  `json:"tests"`
	TestDetail string      `json:"testDetail,omitempty"`
	Summary    string      `json:"summary,omitempty"`

	// HasWorked records that the agent was observed *working* at least once.
	// A settled status before that is a pane that has not started, not a
	// finished task — the distinction that keeps a slow launch from being
	// reported as an instant completion.
	HasWorked bool `json:"hasWorked"`
	// LastSeq is the highest Herdr state-change sequence observed. Progress is
	// appended only when an observation beats it or the status changes.
	LastSeq        int64     `json:"lastSeq"`
	LastStatus     string    `json:"lastStatus,omitempty"`
	LastObservedAt time.Time `json:"lastObservedAt,omitempty"`
	// MissedObservations counts consecutive failures to read the agent. One
	// miss is a restarting Herdr or a busy socket; several in a row is a pane
	// that is really gone. Acting on the first miss would declare every
	// reconnect a lost agent.
	MissedObservations int `json:"missedObservations,omitempty"`

	// LaunchEnterSent records that launch already pressed Enter once for this
	// task. A retry after a crash must not press it again: by then the pane may
	// hold something a human typed, and Enter would submit that instead.
	LaunchEnterSent bool `json:"launchEnterSent,omitempty"`

	// LaunchAttempts and LaunchLeaseUntil make a crashed launch recoverable
	// without letting a duplicate message start a second agent while the first
	// launch is still in flight.
	LaunchAttempts   int       `json:"launchAttempts"`
	LaunchLeaseUntil time.Time `json:"launchLeaseUntil,omitempty"`

	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	CompletedAt time.Time `json:"completedAt,omitempty"`

	Progress []ProgressEvent `json:"progress,omitempty"`
}

// maxProgressEvents bounds the stored history. A long-running agent can emit
// thousands of state changes over a day; the record keeps the most recent
// window rather than growing without limit.
const maxProgressEvents = 200

func (t *Task) appendProgress(ev ProgressEvent) {
	t.Progress = append(t.Progress, ev)
	if len(t.Progress) > maxProgressEvents {
		t.Progress = t.Progress[len(t.Progress)-maxProgressEvents:]
	}
}

// Clone returns a deep copy, so callers cannot mutate stored state by holding
// on to a returned task.
func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	cp := *t
	if t.Progress != nil {
		cp.Progress = append([]ProgressEvent(nil), t.Progress...)
	}
	if t.Worktree.Ports != nil {
		cp.Worktree.Ports = append([]int(nil), t.Worktree.Ports...)
	}
	return &cp
}
