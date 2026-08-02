package orchestration

import (
	"strings"
	"testing"
	"time"
)

// finish drives a launched task's agent to a settled state and observes it,
// which is the only way a task ever becomes eligible for completion.
func (h *harness) finish(res *LaunchResult) *Observation {
	h.t.Helper()
	h.clock.Advance(2 * time.Minute)
	h.herdr.set(res.Task.Agent.PaneID, "done")
	obs, err := h.monitor().Observe(res.Task.ID)
	if err != nil {
		h.t.Fatalf("observe: %v", err)
	}
	return obs
}

// Clearly code-only work completes without anybody being asked about a
// write-up. The gate exists to catch ambiguity, not to add a step to every task.
func TestCodeOnlyTaskCompletesWithoutTheReadbackQuestion(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "fix the crash in the uploader")
	if res.Task.Gate != GateNotNeeded {
		t.Fatalf("gate is %s, want %s", res.Task.Gate, GateNotNeeded)
	}

	obs := h.finish(res)
	if obs.AskReadback != "" {
		t.Fatalf("a code-only task asked about Readback: %s", obs.AskReadback)
	}
	if !obs.ReadyToComplete {
		t.Fatalf("a finished code-only task is not ready to complete: %s", obs.Task.Detail)
	}

	m := h.monitor()
	if _, err := m.RecordTests(res.Task.ID, TestsPassed, "go test ./... — ok"); err != nil {
		t.Fatalf("record tests: %v", err)
	}
	c, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !c.Ready {
		t.Fatalf("not ready: %s", c.Reason)
	}
	for _, want := range []string{"Task complete", res.Task.Worktree.Branch,
		res.Task.Worktree.Path, "passed", "not needed"} {
		if !strings.Contains(c.Message, want) {
			t.Fatalf("completion message is missing %q:\n%s", want, c.Message)
		}
	}
}

// An ambiguous request is not guessed at. The agent finishes, the requester is
// asked once, and until they answer the task is *not* reported complete — and
// nothing is stopped or cleaned up while the question is outstanding.
func TestAmbiguousTaskAsksOnceAndWaits(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "have a look at the uploader")
	if res.Task.Gate != GateUndecided {
		t.Fatalf("gate is %s, want %s", res.Task.Gate, GateUndecided)
	}

	obs := h.finish(res)
	if obs.AskReadback == "" {
		t.Fatal("an ambiguous finished task did not ask the requester anything")
	}
	if !strings.Contains(obs.AskReadback, res.Task.Worktree.Branch) {
		t.Fatalf("the question should name the branch:\n%s", obs.AskReadback)
	}
	if obs.Task.State != StateAwaitingReadbackDecision {
		t.Fatalf("state is %s, want %s", obs.Task.State, StateAwaitingReadbackDecision)
	}
	if obs.ReadyToComplete {
		t.Fatal("an unanswered gate must not report ready to complete")
	}

	// Asking must not stop the agent or free the worktree.
	if _, err := h.herdr.GetAgent(res.Task.Agent.PaneID); err != nil {
		t.Fatalf("the agent's pane was disturbed: %v", err)
	}
	if got := mustGet(t, h.store, res.Task.ID).Worktree.Path; got == "" {
		t.Fatal("the worktree record was dropped while waiting for an answer")
	}

	// Repeated observations and completion attempts never ask twice.
	m := h.monitor()
	for i := 0; i < 5; i++ {
		h.clock.Advance(time.Minute)
		again, err := m.Observe(res.Task.ID)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if again.AskReadback != "" {
			t.Fatal("the requester was asked a second time")
		}
	}
	c, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if c.Ready {
		t.Fatal("a task with an open gate was reported complete")
	}
	if c.AskReadback {
		t.Fatal("complete re-asked a question that was already put to the requester")
	}
	if !strings.Contains(c.Reason, "Readback") {
		t.Fatalf("the reason should name the outstanding decision: %s", c.Reason)
	}
}

func TestAmbiguousTaskCompletesAfterTheUserSaysNo(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "have a look at the uploader")
	h.finish(res)
	m := h.monitor()

	if _, err := m.ResolveGate(res.Task.ID, GateNotNeeded); err != nil {
		t.Fatalf("resolve gate: %v", err)
	}
	c, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !c.Ready {
		t.Fatalf("not ready after the user declined a write-up: %s", c.Reason)
	}
	if mustGet(t, h.store, res.Task.ID).GateDecidedBy != "user" {
		t.Fatal("the user's decision was not recorded as theirs")
	}
}

func TestAmbiguousTaskWaitsForTheDocumentAfterTheUserSaysYes(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "have a look at the uploader")
	h.finish(res)
	m := h.monitor()

	if _, err := m.ResolveGate(res.Task.ID, GateRequired); err != nil {
		t.Fatalf("resolve gate: %v", err)
	}
	c, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if c.Ready {
		t.Fatal("a task that owes a document was reported complete without one")
	}
	if !strings.Contains(c.Message, "readback push") {
		t.Fatalf("the blocker should say how to unblock it:\n%s", c.Message)
	}

	// The document lands later — the delayed-Readback path.
	h.clock.Advance(20 * time.Minute)
	if _, err := m.RecordReadback(res.Task.ID, "herdr-uploader",
		"https://notes.kudcrafts.com/d/herdr-uploader"); err != nil {
		t.Fatalf("record readback: %v", err)
	}
	c, err = m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !c.Ready {
		t.Fatalf("not ready after the document was published: %s", c.Reason)
	}
	if !strings.Contains(c.Message, "https://notes.kudcrafts.com/d/herdr-uploader") {
		t.Fatalf("the completion does not carry the report link:\n%s", c.Message)
	}
	if !strings.Contains(c.Message, "herdr-uploader") {
		t.Fatalf("the completion does not carry the slug:\n%s", c.Message)
	}
}

// A request that clearly asks for research owes a document from the start; no
// question is put to anybody.
func TestResearchTaskRequiresReadbackWithoutAsking(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "research our Shopee competitors and write it up")
	if res.Task.Gate != GateRequired {
		t.Fatalf("gate is %s, want %s", res.Task.Gate, GateRequired)
	}

	obs := h.finish(res)
	if obs.AskReadback != "" {
		t.Fatalf("a clearly-report task asked whether it needed a report: %s", obs.AskReadback)
	}
	if obs.Task.State != StateAwaitingReadbackPublish {
		t.Fatalf("state is %s, want %s", obs.Task.State, StateAwaitingReadbackPublish)
	}

	m := h.monitor()
	c, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if c.Ready {
		t.Fatal("a research task completed with no document")
	}

	if _, err := m.RecordReadback(res.Task.ID, "herdr-shopee",
		"https://notes.kudcrafts.com/d/herdr-shopee"); err != nil {
		t.Fatalf("record readback: %v", err)
	}
	c, err = m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !c.Ready {
		t.Fatalf("not ready with the document published: %s", c.Reason)
	}
}

// The gate decision, the question-asked marker and the published URL all have
// to survive a restart, or a redelivered message re-asks a question the
// requester already answered.
func TestGateStateSurvivesARestart(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "have a look at the uploader")
	h.finish(res)

	// Restart: a fresh store handle over the same files.
	restarted := h.reopen()
	task, err := restarted.Store.Get(res.Task.ID)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if task.Gate != GateUndecided {
		t.Fatalf("gate is %s after restart, want %s", task.Gate, GateUndecided)
	}
	if task.GateQuestionSentAt.IsZero() {
		t.Fatal("the record of having asked did not survive the restart")
	}
	if task.State != StateAwaitingReadbackDecision {
		t.Fatalf("state is %s after restart, want %s", task.State, StateAwaitingReadbackDecision)
	}

	obs, err := restarted.Observe(res.Task.ID)
	if err != nil {
		t.Fatalf("observe after restart: %v", err)
	}
	if obs.AskReadback != "" {
		t.Fatal("a restarted monitor re-asked the Readback question")
	}

	if _, err := restarted.ResolveGate(res.Task.ID, GateRequired); err != nil {
		t.Fatalf("resolve gate: %v", err)
	}
	if _, err := restarted.RecordReadback(res.Task.ID, "herdr-uploader",
		"https://notes.kudcrafts.com/d/herdr-uploader"); err != nil {
		t.Fatalf("record readback: %v", err)
	}

	// Another restart, then complete.
	final := h.reopen()
	c, err := final.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !c.Ready {
		t.Fatalf("not ready after a restart: %s", c.Reason)
	}
	if !strings.Contains(c.Message, "herdr-uploader") {
		t.Fatalf("the completion lost its report link:\n%s", c.Message)
	}
}

// Completion is idempotent: a duplicate delivery of "it's done" re-renders the
// same message rather than posting a second, subtly different one.
func TestCompleteIsIdempotent(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "fix the crash in the uploader")
	h.finish(res)
	m := h.monitor()

	first, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	completedAt := mustGet(t, h.store, res.Task.ID).CompletedAt

	h.clock.Advance(time.Hour)
	second, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete again: %v", err)
	}
	if !second.Ready || second.Message != first.Message {
		t.Fatalf("the second completion differs:\n%s\n---\n%s", first.Message, second.Message)
	}
	if got := mustGet(t, h.store, res.Task.ID).CompletedAt; !got.Equal(completedAt) {
		t.Fatal("the completion time moved on a repeat call")
	}
}

// Completion is refused while the agent is still working, however long that is.
func TestCompleteRefusesAWorkingAgent(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "migrate the billing schema")
	m := h.monitor()

	for i := 0; i < 12; i++ {
		h.clock.Advance(10 * time.Minute)
		if _, err := m.Observe(res.Task.ID); err != nil {
			t.Fatalf("observe: %v", err)
		}
		c, err := m.Complete(res.Task.ID)
		if err != nil {
			t.Fatalf("complete: %v", err)
		}
		if c.Ready {
			t.Fatal("a working agent was reported complete")
		}
		if !strings.Contains(c.Reason, "still working") {
			t.Fatalf("unexpected reason: %s", c.Reason)
		}
	}
}

// A lost agent has nothing to complete, and the message says so without
// pretending the work landed.
func TestCompleteOnALostAgentReportsTheLoss(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "fix the crash in the uploader")
	h.herdr.recycle(res.Task.Agent.PaneID)
	if _, err := h.monitor().Observe(res.Task.ID); err != nil {
		t.Fatalf("observe: %v", err)
	}

	c, err := h.monitor().Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if c.Ready {
		t.Fatal("a lost agent was reported complete")
	}
	if !strings.Contains(c.Message, "lost its agent") ||
		!strings.Contains(c.Message, "untouched") {
		t.Fatalf("the message should report the loss honestly:\n%s", c.Message)
	}
}

// The completion carries what somebody actually asks: which worktree, which
// branch, did the tests pass, where is the write-up.
func TestCompletionMessageCarriesTheEssentials(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "research the margin drop and write it up")
	h.finish(res)
	m := h.monitor()

	if _, err := m.RecordTests(res.Task.ID, TestsFailed, "2 of 40 failing"); err != nil {
		t.Fatalf("record tests: %v", err)
	}
	if _, err := m.RecordSummary(res.Task.ID, "the 4pp fall is all in shipping"); err != nil {
		t.Fatalf("record summary: %v", err)
	}
	if _, err := m.RecordReadback(res.Task.ID, "herdr-margins",
		"https://notes.kudcrafts.com/d/herdr-margins"); err != nil {
		t.Fatalf("record readback: %v", err)
	}
	c, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !c.Ready {
		t.Fatalf("not ready: %s", c.Reason)
	}
	for _, want := range []string{
		res.Task.ID, "demo", res.Task.Worktree.Branch, res.Task.Worktree.Path,
		"failed", "2 of 40 failing",
		"https://notes.kudcrafts.com/d/herdr-margins",
		"the 4pp fall is all in shipping",
	} {
		if !strings.Contains(c.Message, want) {
			t.Fatalf("completion is missing %q:\n%s", want, c.Message)
		}
	}
	if len(c.Message) > 1500 {
		t.Fatalf("the completion is %d chars; it is meant to be concise", len(c.Message))
	}
}

// Unknown tests are reported as unknown. Saying "passed" because nothing said
// otherwise is the claim this whole contract exists to prevent.
func TestUnknownTestsAreReportedAsUnknown(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "fix the crash in the uploader")
	h.finish(res)

	c, err := h.monitor().Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !strings.Contains(c.Message, "unknown") {
		t.Fatalf("the completion should admit it does not know:\n%s", c.Message)
	}
	if strings.Contains(c.Message, "passed") {
		t.Fatalf("the completion claimed passing tests:\n%s", c.Message)
	}
}

// Publishing a document answers the question the gate was going to ask.
func TestPublishingSettlesAnUndecidedGate(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "have a look at the uploader")
	h.finish(res)
	m := h.monitor()

	task, err := m.RecordReadback(res.Task.ID, "herdr-uploader",
		"https://notes.kudcrafts.com/d/herdr-uploader")
	if err != nil {
		t.Fatalf("record readback: %v", err)
	}
	if task.Gate != GateRequired {
		t.Fatalf("gate is %s after publishing, want %s", task.Gate, GateRequired)
	}
	c, err := m.Complete(res.Task.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !c.Ready {
		t.Fatalf("not ready after publishing: %s", c.Reason)
	}
}

func TestResolveGateRejectsANonDecision(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "have a look at the uploader")
	if _, err := h.monitor().ResolveGate(res.Task.ID, GateUndecided); err == nil {
		t.Fatal("'still undecided' is not a decision")
	}
}

// Answering the question while the agent is still working is allowed and
// changes nothing about the agent — it just means no question needs asking
// later.
func TestGateCanBeDecidedWhileTheAgentIsStillWorking(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "have a look at the uploader")
	m := h.monitor()

	if _, err := m.ResolveGate(res.Task.ID, GateNotNeeded); err != nil {
		t.Fatalf("resolve gate: %v", err)
	}
	if got := mustGet(t, h.store, res.Task.ID).State; got != StateWorking {
		t.Fatalf("deciding the gate changed the agent state to %s", got)
	}

	obs := h.finish(res)
	if obs.AskReadback != "" {
		t.Fatal("a task with an answered gate still asked the question")
	}
	if !obs.ReadyToComplete {
		t.Fatalf("not ready to complete: %s", obs.Task.Detail)
	}
}
