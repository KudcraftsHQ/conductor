package orchestration

import (
	"errors"
	"fmt"
	"time"
)

// missesBeforeLost is how many consecutive unreadable observations it takes
// before a task's agent is declared lost. One miss is a Herdr restart, a busy
// socket, or a laptop that slept; declaring a lost agent on the first miss
// would turn every reconnect into a false alarm — and a false "agent lost" is
// worse than a late one, because it invites somebody to relaunch work that is
// still running.
const missesBeforeLost = 3

// Monitor turns Herdr's live agent state into task progress.
//
// It is a *reader*. It never presses a key, never resends a prompt and never
// closes a worktree. Its only side effect is on Conductor's own record, and it
// only writes there when Herdr reported something new: a status change, or a
// state-change sequence beyond the last one seen. Nothing here advances on a
// timer, so "no progress for an hour" is a fact about the agent rather than an
// artefact of how often somebody polled.
type Monitor struct {
	Store *Store
	Herdr Herdr
	Now   func() time.Time
}

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Observation is what one look at a task found.
type Observation struct {
	Task *Task
	// Changed is true when this observation recorded a real state change.
	Changed bool
	// Status is the agent status Herdr reported, if it could be read.
	Status string
	// Unreachable means Herdr could not be asked; the task's state is
	// unchanged and nothing may be claimed from this observation.
	Unreachable bool
	// AskReadback carries the question to post *once*, when a finished task's
	// readback gate is still undecided. Empty on every later observation.
	AskReadback string
	// ReadyToComplete means the agent is finished and the gate is satisfied —
	// Complete will produce the final message.
	ReadyToComplete bool
	Detail          string
}

// Observe samples one task. Safe to call as often as a caller likes: repeated
// calls with no agent activity record nothing and ask nothing twice.
func (m *Monitor) Observe(taskID string) (*Observation, error) {
	task, err := m.Store.Get(taskID)
	if err != nil {
		return nil, err
	}
	if task.State.Terminal() {
		return &Observation{Task: task, Detail: "task is already " + string(task.State)}, nil
	}
	if task.Agent.PaneID == "" {
		return &Observation{
			Task: task, Unreachable: true,
			Detail: "no agent pane is recorded for this task yet",
		}, nil
	}

	info, getErr := m.Herdr.GetAgent(task.Agent.PaneID)
	if getErr != nil {
		return m.recordMiss(task, getErr)
	}
	return m.record(task, info)
}

// ObserveAll samples every task that still expects progress. This is what a
// monitor calls after a restart: the store, not memory, is the list of what is
// running.
func (m *Monitor) ObserveAll() ([]*Observation, error) {
	active, err := m.Store.Active()
	if err != nil {
		return nil, err
	}
	out := make([]*Observation, 0, len(active))
	for _, t := range active {
		obs, err := m.Observe(t.ID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return out, err
		}
		out = append(out, obs)
	}
	return out, nil
}

// recordMiss handles an unreadable agent. The task keeps its state until the
// misses stack up, so a disconnect looks like a disconnect and only a
// persistently absent pane becomes a lost agent.
func (m *Monitor) recordMiss(task *Task, cause error) (*Observation, error) {
	now := m.now()
	var lost bool
	updated, err := m.Store.Update(task.ID, func(t *Task) error {
		t.MissedObservations++
		if t.MissedObservations >= missesBeforeLost {
			lost = true
			t.State = StateAgentLost
			t.Detail = fmt.Sprintf(
				"herdr could not read pane %s %d times in a row: %v",
				t.Agent.PaneID, t.MissedObservations, cause)
			t.appendProgress(ProgressEvent{
				At: now, Kind: "lost", Detail: t.Detail})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	detail := fmt.Sprintf("could not read pane %s (%v); attempt %d of %d before the agent is declared lost",
		task.Agent.PaneID, cause, updated.MissedObservations, missesBeforeLost)
	if lost {
		detail = updated.Detail
	}
	return &Observation{
		Task: updated, Unreachable: true, Changed: lost, Detail: detail,
	}, nil
}

func (m *Monitor) record(task *Task, info *AgentInfo) (*Observation, error) {
	now := m.now()
	obs := &Observation{Status: info.Status}

	// A pane id outlives the terminal in it. If the terminal changed, whatever
	// is in that pane now belongs to somebody else, and reporting its status as
	// this task's progress would be a fabrication.
	if task.Agent.TerminalID != "" && info.TerminalID != "" &&
		info.TerminalID != task.Agent.TerminalID {
		updated, err := m.Store.Update(task.ID, func(t *Task) error {
			t.State = StateAgentLost
			t.Detail = fmt.Sprintf(
				"pane %s now hosts terminal %s, not the %s this task started; the pane was recycled",
				t.Agent.PaneID, info.TerminalID, t.Agent.TerminalID)
			t.appendProgress(ProgressEvent{
				At: now, Kind: "lost", Status: info.Status,
				StateChangeSeq: info.StateChangeSeq, Detail: t.Detail})
			return nil
		})
		if err != nil {
			return nil, err
		}
		obs.Task = updated
		obs.Changed = true
		obs.Detail = updated.Detail
		return obs, nil
	}

	changed := info.StateChangeSeq > task.LastSeq || info.Status != task.LastStatus
	var ask string
	var ready bool

	updated, err := m.Store.Update(task.ID, func(t *Task) error {
		t.MissedObservations = 0
		t.LastObservedAt = now
		if t.Agent.TerminalID == "" {
			t.Agent.TerminalID = info.TerminalID
		}
		if !changed {
			return nil
		}
		prevStatus := t.LastStatus
		t.LastStatus = info.Status
		if info.StateChangeSeq > t.LastSeq {
			t.LastSeq = info.StateChangeSeq
		}

		switch {
		case isWorking(info.Status):
			t.HasWorked = true
			t.State = StateWorking
			t.Detail = "the agent is working"
		case isBlocked(info.Status):
			t.State = StateBlocked
			t.Detail = "the agent is waiting on a human"
		case isSettled(info.Status) && t.HasWorked:
			applyTerminal(t, now, &ask, &ready)
		default:
			// Settled but never observed working: a pane that has not started
			// yet. Not progress, and emphatically not a finished task.
			t.Detail = "the agent has not started working yet"
		}
		t.appendProgress(ProgressEvent{
			At: now, Kind: "status", Status: info.Status,
			StateChangeSeq: info.StateChangeSeq,
			Detail:         fmt.Sprintf("%s -> %s", orNone(prevStatus), info.Status),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	obs.Task = updated
	obs.Changed = changed
	obs.AskReadback = ask
	obs.ReadyToComplete = ready
	obs.Detail = updated.Detail
	return obs, nil
}

// applyTerminal decides what a finished agent means for the task.
//
// Finishing is not completing. The readback gate is the last thing between the
// two, and an undecided gate is a *question*, never a block: the agent stays
// exactly where it is, the worktree is untouched, and the task simply is not
// reported done until somebody answers.
func applyTerminal(t *Task, now time.Time, ask *string, ready *bool) {
	switch t.Gate {
	case GateUndecided:
		t.State = StateAwaitingReadbackDecision
		if t.GateQuestionSentAt.IsZero() {
			*ask = GateQuestion(t)
			t.GateQuestionSentAt = now
			t.Detail = "the agent finished; asked the requester whether this needs a Readback write-up"
			t.appendProgress(ProgressEvent{
				At: now, Kind: "gate",
				Detail: "asked the requester whether a Readback write-up is needed"})
		} else {
			t.Detail = "the agent finished; still waiting on the Readback decision"
		}
	case GateRequired:
		if t.Readback.URL == "" {
			t.State = StateAwaitingReadbackPublish
			t.Detail = "the agent finished; waiting for the Readback document to be published"
			return
		}
		t.State = StateAgentDone
		t.Detail = "the agent finished and the Readback document is published"
		*ready = true
	default: // GateNotNeeded
		t.State = StateAgentDone
		t.Detail = "the agent finished"
		*ready = true
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// ResolveGate records a human's answer to the readback question. A user
// decision outranks the classifier and is never re-derived afterwards.
//
// Answering after the agent has already finished is the common case — that is
// when the question gets asked — so this also moves the task on to whatever the
// answer implies.
func (m *Monitor) ResolveGate(taskID string, gate ReadbackGate) (*Task, error) {
	if gate != GateRequired && gate != GateNotNeeded {
		return nil, fmt.Errorf(
			"a readback decision must be %q or %q, not %q", GateRequired, GateNotNeeded, gate)
	}
	now := m.now()
	return m.Store.Update(taskID, func(t *Task) error {
		if t.State == StateCompleted {
			return fmt.Errorf("task %s is already completed", t.ID)
		}
		t.Gate = gate
		t.GateDecidedBy = "user"
		t.appendProgress(ProgressEvent{
			At: now, Kind: "gate",
			Detail: fmt.Sprintf("requester set the readback gate to %s", gate)})
		// Only a finished agent's state depends on the gate; a working agent
		// keeps working and picks this up when it settles.
		if t.State == StateAwaitingReadbackDecision || t.State == StateAwaitingReadbackPublish ||
			t.State == StateAgentDone {
			var ask string
			var ready bool
			applyTerminal(t, now, &ask, &ready)
		}
		return nil
	})
}

// RecordReadback stores the publishing metadata a completion will quote. The
// URL is what the publisher printed — this package never constructs one from a
// slug, because a fabricated link is indistinguishable, to the person reading
// the thread, from the silence it is meant to end.
func (m *Monitor) RecordReadback(taskID, slug, url string) (*Task, error) {
	if url == "" {
		return nil, errors.New("a readback record needs the URL the publisher printed")
	}
	now := m.now()
	return m.Store.Update(taskID, func(t *Task) error {
		t.Readback = ReadbackRef{Slug: slug, URL: url, PublishedAt: now}
		t.appendProgress(ProgressEvent{
			At: now, Kind: "readback",
			Detail: fmt.Sprintf("readback published: %s", url)})
		if t.Gate == GateUndecided {
			// Publishing settles the question it was going to ask.
			t.Gate = GateRequired
			t.GateDecidedBy = "user"
		}
		if t.State == StateAwaitingReadbackPublish || t.State == StateAwaitingReadbackDecision {
			var ask string
			var ready bool
			applyTerminal(t, now, &ask, &ready)
		}
		return nil
	})
}

// RecordTests stores what is known about the task's tests. Conductor does not
// run them; it repeats what it was told, and "unknown" stays "unknown".
func (m *Monitor) RecordTests(taskID string, status TestStatus, detail string) (*Task, error) {
	switch status {
	case TestsUnknown, TestsPassed, TestsFailed, TestsSkipped:
	default:
		return nil, fmt.Errorf("unknown test status %q", status)
	}
	now := m.now()
	return m.Store.Update(taskID, func(t *Task) error {
		t.Tests = status
		t.TestDetail = detail
		t.appendProgress(ProgressEvent{
			At: now, Kind: "tests",
			Detail: fmt.Sprintf("tests recorded as %s: %s", status, detail)})
		return nil
	})
}

// RecordSummary stores the one-line outcome the completion message carries.
func (m *Monitor) RecordSummary(taskID, summary string) (*Task, error) {
	return m.Store.Update(taskID, func(t *Task) error {
		t.Summary = summary
		return nil
	})
}
