package orchestration

import (
	"strings"
	"testing"
	"time"
)

// TestFullLifecycle walks one request from a Discord message to a posted
// completion, the way it actually happens: a duplicate delivery, hours of work
// observed from outside, a disconnect in the middle, a restart, an ambiguous
// deliverable that has to be asked about, and a document that lands late.
//
// It is written as one narrative on purpose. Each of these steps has a focused
// test of its own; what this one checks is that they compose — that no step
// quietly undoes an earlier one.
func TestFullLifecycle(t *testing.T) {
	h := newHarness(t)

	// --- the message arrives, twice (Discord retried the interaction) -----
	first := h.launch("discord-msg-77", "take a look at why the uploader keeps dying")
	dupe := h.launch("discord-msg-77", "take a look at why the uploader keeps dying")

	if !dupe.Duplicate || dupe.Task.ID != first.Task.ID {
		t.Fatal("the retry started separate work")
	}
	if len(h.wts.created) != 1 || h.herdr.promptCount() != 1 {
		t.Fatalf("retry duplicated work: %d worktrees, %d prompts",
			len(h.wts.created), h.herdr.promptCount())
	}
	if !first.Confirmed {
		t.Fatalf("the launch did not confirm the agent started: %s", first.Detail)
	}
	if first.Task.Worktree.Path == h.wts.root {
		t.Fatal("the agent is in the root checkout")
	}
	if first.Task.Gate != GateUndecided {
		t.Fatalf("gate is %s; 'take a look' does not say whether a write-up is owed",
			first.Task.Gate)
	}

	taskID := first.Task.ID
	pane := first.Task.Agent.PaneID
	m := h.monitor()

	// --- two hours of work, sampled every five minutes --------------------
	statusEvents := 0
	for i := 0; i < 24; i++ {
		h.clock.Advance(5 * time.Minute)
		// The agent blocks on a question a couple of times along the way.
		switch i {
		case 7:
			h.herdr.set(pane, "blocked")
		case 8:
			h.herdr.set(pane, "working")
		}
		obs, err := m.Observe(taskID)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if obs.Changed {
			statusEvents++
		}
		if obs.ReadyToComplete {
			t.Fatalf("sample %d reported a working agent as ready to complete", i)
		}
	}
	if statusEvents != 2 {
		t.Fatalf("recorded %d changes for 2 real transitions", statusEvents)
	}

	// --- Herdr restarts under us -----------------------------------------
	h.herdr.getErr = errAgentUnreachable
	for i := 0; i < missesBeforeLost-1; i++ {
		obs, err := m.Observe(taskID)
		if err != nil {
			t.Fatalf("observe during outage: %v", err)
		}
		if obs.Task.State == StateAgentLost {
			t.Fatal("a brief outage lost the agent")
		}
	}
	h.herdr.getErr = nil

	// --- and so does the bridge process -----------------------------------
	restarted := h.reopen()
	live, err := restarted.Store.Active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(live) != 1 || live[0].ID != taskID {
		t.Fatalf("a restarted process did not find the live task: %+v", live)
	}

	// --- the agent finishes ----------------------------------------------
	h.clock.Advance(20 * time.Minute)
	h.herdr.set(pane, "done")
	obs, err := restarted.Observe(taskID)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if obs.AskReadback == "" {
		t.Fatal("the ambiguous deliverable was not put to the requester")
	}
	if obs.Task.State != StateAwaitingReadbackDecision {
		t.Fatalf("state is %s, want %s", obs.Task.State, StateAwaitingReadbackDecision)
	}

	// Completing now must refuse, and must not claim anything landed.
	early, err := restarted.Complete(taskID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if early.Ready || strings.Contains(early.Message, "Task complete") {
		t.Fatalf("an unanswered gate produced a completion:\n%s", early.Message)
	}

	// --- the requester answers, late --------------------------------------
	h.clock.Advance(40 * time.Minute)
	gate, ok := ParseGateDecision("yes")
	if !ok {
		t.Fatal("'yes' was not read as a decision")
	}
	if _, err := restarted.ResolveGate(taskID, gate); err != nil {
		t.Fatalf("resolve gate: %v", err)
	}
	waiting, err := restarted.Complete(taskID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if waiting.Ready {
		t.Fatal("completed without the document the requester asked for")
	}

	// --- the document is published, and the task lands --------------------
	h.clock.Advance(15 * time.Minute)
	if _, err := restarted.RecordTests(taskID, TestsPassed, "go test ./... — ok"); err != nil {
		t.Fatalf("record tests: %v", err)
	}
	if _, err := restarted.RecordSummary(taskID,
		"the uploader dies on a nil multipart boundary; patched and covered"); err != nil {
		t.Fatalf("record summary: %v", err)
	}
	if _, err := restarted.RecordReadback(taskID, "herdr-"+taskID,
		"https://notes.kudcrafts.com/d/herdr-uploader"); err != nil {
		t.Fatalf("record readback: %v", err)
	}

	final := h.reopen() // one more restart, for luck
	done, err := final.Complete(taskID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !done.Ready {
		t.Fatalf("still not complete: %s", done.Reason)
	}
	for _, want := range []string{
		"Task complete", taskID, first.Task.Worktree.Branch, first.Task.Worktree.Path,
		"passed", "https://notes.kudcrafts.com/d/herdr-uploader",
		"nil multipart boundary",
	} {
		if !strings.Contains(done.Message, want) {
			t.Fatalf("completion is missing %q:\n%s", want, done.Message)
		}
	}

	// The record tells the whole story, and only from things that happened.
	stored := mustGet(t, h.store, taskID)
	if stored.State != StateCompleted {
		t.Fatalf("final state is %s", stored.State)
	}
	if stored.LaunchAttempts != 1 {
		t.Fatalf("launch attempts is %d; the retry should not have launched again",
			stored.LaunchAttempts)
	}
	for _, ev := range stored.Progress {
		if ev.Kind == "status" && ev.StateChangeSeq == 0 {
			t.Fatalf("a status event carries no supporting observation: %+v", ev)
		}
	}
	if h.herdr.promptCount() != 1 {
		t.Fatalf("the prompt was delivered %d times over the whole lifecycle",
			h.herdr.promptCount())
	}
}

type unreachable struct{}

func (unreachable) Error() string { return "herdr socket is not accepting connections" }

var errAgentUnreachable = unreachable{}
