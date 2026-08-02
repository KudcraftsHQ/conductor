package orchestration

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Progress must come from the agent, not from the act of looking. Polling a
// quiet agent a hundred times has to leave the record exactly as it found it,
// or "still working after an hour" becomes indistinguishable from a hundred
// fabricated updates.
func TestPollingAQuietAgentRecordsNothing(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "migrate the billing schema")
	m := h.monitor()

	before := mustGet(t, h.store, res.Task.ID)
	for i := 0; i < 100; i++ {
		h.clock.Advance(30 * time.Second)
		obs, err := m.Observe(res.Task.ID)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if obs.Changed {
			t.Fatalf("observation %d claimed a change from an idle-status agent", i)
		}
	}
	after := mustGet(t, h.store, res.Task.ID)
	if len(after.Progress) != len(before.Progress) {
		t.Fatalf("progress grew from %d to %d events with no agent activity",
			len(before.Progress), len(after.Progress))
	}
	if after.LastObservedAt.Equal(before.LastObservedAt) {
		t.Fatal("the observation time should still be refreshed, so staleness is visible")
	}
}

// Real agent activity — a status change, or Herdr's state-change sequence
// moving — is what produces an event.
func TestRealAgentActivityProducesProgress(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "migrate the billing schema")
	pane := res.Task.Agent.PaneID
	m := h.monitor()

	start := len(mustGet(t, h.store, res.Task.ID).Progress)

	for _, status := range []string{"blocked", "working", "blocked", "working"} {
		h.clock.Advance(time.Minute)
		h.herdr.set(pane, status)
		obs, err := m.Observe(res.Task.ID)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if !obs.Changed {
			t.Fatalf("a transition to %s was not recorded as a change", status)
		}
		if obs.Status != status {
			t.Fatalf("observed status %s, want %s", obs.Status, status)
		}
	}

	task := mustGet(t, h.store, res.Task.ID)
	if got := len(task.Progress) - start; got != 4 {
		t.Fatalf("recorded %d progress events for 4 transitions", got)
	}
	if task.LastSeq == 0 {
		t.Fatal("the state-change sequence was not recorded")
	}
	for _, ev := range task.Progress[start:] {
		if ev.Kind != "status" || ev.Status == "" || ev.StateChangeSeq == 0 {
			t.Fatalf("progress event lacks its supporting observation: %+v", ev)
		}
	}
}

// A settled agent that was never seen working has not finished — it has not
// started. Treating that as a completion is how a slow launch gets reported as
// an instant success.
func TestSettledAgentThatNeverWorkedIsNotComplete(t *testing.T) {
	h := newHarness(t)
	h.herdr.workOnPrompt = false
	h.herdr.workOnEnter = false
	res := h.launch("msg-1", "fix the login bug")

	h.herdr.set(res.Task.Agent.PaneID, "idle")
	obs, err := h.monitor().Observe(res.Task.ID)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if obs.ReadyToComplete {
		t.Fatal("a pane that never worked was reported ready to complete")
	}
	if obs.Task.State == StateAgentDone {
		t.Fatalf("state is %s; the agent never worked", obs.Task.State)
	}
}

// A Herdr that cannot be reached is a disconnect, not a lost agent. Declaring
// the agent lost on the first miss would invite somebody to relaunch work that
// is still running.
func TestTransientDisconnectDoesNotLoseTheAgent(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "migrate the billing schema")
	m := h.monitor()

	h.herdr.getErr = errors.New("socket closed")
	obs, err := m.Observe(res.Task.ID)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !obs.Unreachable {
		t.Fatal("the observation should be marked unreachable")
	}
	if obs.Task.State == StateAgentLost {
		t.Fatal("one miss must not lose the agent")
	}

	// Herdr comes back before the misses stack up.
	h.herdr.getErr = nil
	obs, err = m.Observe(res.Task.ID)
	if err != nil {
		t.Fatalf("observe after reconnect: %v", err)
	}
	if obs.Unreachable {
		t.Fatal("the agent is readable again")
	}
	if obs.Task.MissedObservations != 0 {
		t.Fatalf("miss counter is %d after a successful read", obs.Task.MissedObservations)
	}
	if obs.Task.State != StateWorking {
		t.Fatalf("state is %s, want %s", obs.Task.State, StateWorking)
	}
}

func TestPersistentlyUnreadablePaneBecomesLost(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "migrate the billing schema")
	m := h.monitor()
	h.herdr.getErr = errors.New("no such pane")

	var obs *Observation
	var err error
	for i := 0; i < missesBeforeLost; i++ {
		obs, err = m.Observe(res.Task.ID)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	if obs.Task.State != StateAgentLost {
		t.Fatalf("state is %s after %d misses, want %s",
			obs.Task.State, missesBeforeLost, StateAgentLost)
	}
	// And a lost agent is never silently relaunched.
	if len(h.wts.created) != 1 {
		t.Fatalf("%d worktrees exist; losing an agent must not create another",
			len(h.wts.created))
	}
}

// A pane id outlives the terminal in it. Reporting the new terminal's status as
// this task's progress would be a fabrication.
func TestRecycledPaneIsDetected(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "migrate the billing schema")
	h.herdr.recycle(res.Task.Agent.PaneID)

	obs, err := h.monitor().Observe(res.Task.ID)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if obs.Task.State != StateAgentLost {
		t.Fatalf("state is %s, want %s", obs.Task.State, StateAgentLost)
	}
	if !strings.Contains(obs.Task.Detail, "recycled") {
		t.Fatalf("the detail should explain the recycle: %s", obs.Task.Detail)
	}
}

// The store, not memory, is the list of what is running: a fresh process must
// pick up every live task and keep recording against the same record.
func TestMonitorResumesAfterARestart(t *testing.T) {
	h := newHarness(t)
	a := h.launch("msg-1", "migrate the billing schema")
	b := h.launch("msg-2", "refactor the uploader")

	// A brand-new store handle over the same files — what a restart gets.
	restarted := h.reopen()
	observations, err := restarted.ObserveAll()
	if err != nil {
		t.Fatalf("observe all: %v", err)
	}
	if len(observations) != 2 {
		t.Fatalf("a restarted monitor saw %d live tasks, want 2", len(observations))
	}

	h.herdr.set(a.Task.Agent.PaneID, "done")
	if _, err := restarted.Observe(a.Task.ID); err != nil {
		t.Fatalf("observe: %v", err)
	}

	// The original handle sees the same truth: state is on disk, not in a
	// process.
	reloaded := mustGet(t, h.store, a.Task.ID)
	if reloaded.State == StateWorking {
		t.Fatal("the completion recorded by the restarted monitor was not persisted")
	}
	still := mustGet(t, h.store, b.Task.ID)
	if still.State != StateWorking {
		t.Fatalf("the other task's state changed to %s", still.State)
	}
}

// A completed or lost task is not re-observed into life.
func TestTerminalTasksAreLeftAlone(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "fix the crash in the uploader")
	h.herdr.set(res.Task.Agent.PaneID, "done")
	m := h.monitor()
	if _, err := m.Observe(res.Task.ID); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if _, err := m.Complete(res.Task.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	active, err := h.store.Active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("a completed task is still in the active list: %v", active[0].State)
	}
	obs, err := m.Observe(res.Task.ID)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if obs.Changed {
		t.Fatal("a completed task was changed by an observation")
	}
}

func TestRecordTestsAndSummary(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "fix the crash in the uploader")
	m := h.monitor()

	if _, err := m.RecordTests(res.Task.ID, "sideways", ""); err == nil {
		t.Fatal("an unknown test status should be refused")
	}
	task, err := m.RecordTests(res.Task.ID, TestsPassed, "go test ./... — ok")
	if err != nil {
		t.Fatalf("record tests: %v", err)
	}
	if task.Tests != TestsPassed || task.TestDetail == "" {
		t.Fatalf("tests not recorded: %+v", task.Tests)
	}
	if _, err := m.RecordSummary(res.Task.ID, "dropped the double fetch"); err != nil {
		t.Fatalf("record summary: %v", err)
	}
	if got := mustGet(t, h.store, res.Task.ID).Summary; got != "dropped the double fetch" {
		t.Fatalf("summary is %q", got)
	}
}

func TestRecordReadbackRefusesAConstructedURL(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "research the margin drop and write it up")
	if _, err := h.monitor().RecordReadback(res.Task.ID, "herdr-x", ""); err == nil {
		t.Fatal("recording a readback with no URL should be refused")
	}
}
