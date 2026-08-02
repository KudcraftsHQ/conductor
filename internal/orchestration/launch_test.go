package orchestration

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLaunchRunsInAFreshWorktreeNotTheRoot(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "fix the login bug")

	if res.Task.Worktree.Path == h.wts.root {
		t.Fatalf("agent was placed in the root checkout %s", h.wts.root)
	}
	if len(h.wts.created) != 1 {
		t.Fatalf("expected exactly one worktree, got %d", len(h.wts.created))
	}
	if got := h.herdr.creates[0]; !strings.Contains(got, res.Task.Worktree.Path) {
		t.Fatalf("herdr workspace was not rooted at the worktree: %s", got)
	}
	if res.Task.Worktree.Branch == "" {
		t.Fatal("worktree has no branch recorded")
	}
}

// A worktree source that hands back the root checkout is a bug somewhere else;
// the launcher's job is to refuse it rather than run an agent on the branch a
// human is sitting on.
func TestLaunchRefusesTheRootCheckout(t *testing.T) {
	h := newHarness(t)
	h.wts.returnRoot = true

	_, err := h.launcher().Launch(LaunchRequest{
		Project: "demo", Prompt: "fix the login bug",
		Origin: Origin{RequestID: "msg-1"},
	})
	if err == nil {
		t.Fatal("expected the launch to be refused")
	}
	if !strings.Contains(err.Error(), "root checkout") {
		t.Fatalf("unexpected error: %v", err)
	}
	stored := mustGet(t, h.store, newTaskID("demo", "msg-1"))
	if stored.State != StateFailed {
		t.Fatalf("failure was not recorded, state is %s", stored.State)
	}
}

func TestLaunchAlwaysPassesSkipPermissions(t *testing.T) {
	h := newHarness(t)
	h.launch("msg-1", "fix the login bug")

	if len(h.herdr.runs) != 1 {
		t.Fatalf("expected one command in the pane, got %v", h.herdr.runs)
	}
	cmd := h.herdr.runs[0]
	for _, want := range []string{"--dangerously-skip-permissions", "CLAUDE_CODE_NO_FLICKER=1", "claude"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("launch command is missing %q: %s", want, cmd)
		}
	}
}

// The flag check is deliberately independent of codingagent, so removing it
// there fails the launch loudly instead of quietly producing an agent that
// stalls on the first permission prompt.
func TestAssertUnattendedClaudeRejectsMissingFlags(t *testing.T) {
	if err := assertUnattendedClaude([]string{"env", "CLAUDE_CODE_NO_FLICKER=1", "claude"}); err == nil {
		t.Fatal("expected a refusal when --dangerously-skip-permissions is absent")
	}
	if err := assertUnattendedClaude([]string{"claude", "--dangerously-skip-permissions"}); err == nil {
		t.Fatal("expected a refusal when CLAUDE_CODE_NO_FLICKER=1 is absent")
	}
	if err := assertUnattendedClaude([]string{
		"env", "CLAUDE_CODE_NO_FLICKER=1", "claude", "--dangerously-skip-permissions",
	}); err != nil {
		t.Fatalf("valid argv was rejected: %v", err)
	}
}

// The launch path must answer a Discord interaction, so it returns on the
// agent starting — not on the agent finishing. Here the agent never finishes.
func TestLaunchReturnsOnDispatchNotCompletion(t *testing.T) {
	h := newHarness(t)
	start := h.clock.Now()

	res := h.launch("msg-1", "fix the login bug")

	if !res.Confirmed {
		t.Fatalf("expected a confirmed dispatch, got %s: %s", res.Task.State, res.Detail)
	}
	if res.Task.State != StateWorking {
		t.Fatalf("state is %s, want %s", res.Task.State, StateWorking)
	}
	// The agent is still working; the launcher did not wait for it.
	info, _ := h.herdr.GetAgent(res.Task.Agent.PaneID)
	if info.Status != "working" {
		t.Fatalf("the fake agent should still be working, it is %s", info.Status)
	}
	if waited := h.clock.Now().Sub(start); waited > defaultConfirmBudget {
		t.Fatalf("launch consumed %s, more than the confirm budget %s", waited, defaultConfirmBudget)
	}
}

// A long-running agent — one that works for hours — must not extend the launch
// call by a single tick beyond confirmation.
func TestLaunchDoesNotWaitOutALongRunningAgent(t *testing.T) {
	h := newHarness(t)
	res := h.launch("msg-1", "migrate the billing schema")
	paneID := res.Task.Agent.PaneID

	// Three simulated hours of work, observed the way a monitor would.
	m := h.monitor()
	for i := 0; i < 36; i++ {
		h.clock.Advance(5 * time.Minute)
		if _, err := m.Observe(res.Task.ID); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	task := mustGet(t, h.store, res.Task.ID)
	if task.State != StateWorking {
		t.Fatalf("after three hours of working the state is %s, want %s", task.State, StateWorking)
	}
	if task.CompletedAt != (time.Time{}) {
		t.Fatal("a still-working task must not carry a completion time")
	}

	// It finishes only when the agent actually settles.
	h.herdr.set(paneID, "done")
	obs, err := m.Observe(res.Task.ID)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if obs.Task.State == StateWorking {
		t.Fatal("the finished agent was not noticed")
	}
}

// Redelivery of the same Discord message is routine. It must never produce a
// second worktree, a second agent, or a second copy of the prompt.
func TestDuplicateOriginatingMessageDoesNotDuplicateWork(t *testing.T) {
	h := newHarness(t)
	first := h.launch("msg-1", "fix the login bug")
	second := h.launch("msg-1", "fix the login bug")

	if !second.Duplicate {
		t.Fatal("the second delivery was not recognised as a duplicate")
	}
	if second.Task.ID != first.Task.ID {
		t.Fatalf("duplicate produced a different task: %s vs %s", second.Task.ID, first.Task.ID)
	}
	if len(h.wts.created) != 1 {
		t.Fatalf("expected one worktree, got %d", len(h.wts.created))
	}
	if got := h.herdr.promptCount(); got != 1 {
		t.Fatalf("the prompt was delivered %d times, want once", got)
	}
	if len(h.herdr.creates) != 1 {
		t.Fatalf("expected one herdr workspace, got %d", len(h.herdr.creates))
	}
}

// A different message asking for the same thing is different work, and gets its
// own worktree — idempotency is a property of the message id, not of the text.
func TestDifferentMessagesGetSeparateWorktrees(t *testing.T) {
	h := newHarness(t)
	a := h.launch("msg-1", "fix the login bug")
	b := h.launch("msg-2", "fix the login bug")

	if a.Task.ID == b.Task.ID {
		t.Fatal("two messages collapsed into one task")
	}
	if a.Task.Worktree.Path == b.Task.Worktree.Path {
		t.Fatal("two tasks share a worktree")
	}
}

// A launch that died before producing an agent leaves a record with an expired
// lease. The retry finishes the job under the same task id rather than
// stranding the request.
func TestCrashedLaunchIsRetriedByARedelivery(t *testing.T) {
	h := newHarness(t)
	h.herdr.createErr = errors.New("herdr socket closed")

	_, err := h.launcher().Launch(LaunchRequest{
		Project: "demo", Prompt: "fix the login bug",
		Origin: Origin{RequestID: "msg-1"},
	})
	if err == nil {
		t.Fatal("expected the first launch to fail")
	}
	failed := mustGet(t, h.store, newTaskID("demo", "msg-1"))
	if failed.State != StateFailed {
		t.Fatalf("state is %s, want %s", failed.State, StateFailed)
	}

	h.herdr.createErr = nil
	retry := h.launch("msg-1", "fix the login bug")
	if retry.Duplicate {
		t.Fatal("a failed launch should be retryable, not treated as a live duplicate")
	}
	if retry.Task.ID != failed.ID {
		t.Fatalf("retry used a new task id %s", retry.Task.ID)
	}
	if retry.Task.State != StateWorking {
		t.Fatalf("retry state is %s, want %s", retry.Task.State, StateWorking)
	}
	if retry.Task.LaunchAttempts != 2 {
		t.Fatalf("launch attempts is %d, want 2", retry.Task.LaunchAttempts)
	}
}

// While one delivery is mid-launch, a second must not start a parallel agent.
func TestRedeliveryDuringAnInFlightLaunchIsRefused(t *testing.T) {
	h := newHarness(t)
	// Reserve the request the way a launch in progress would, without
	// completing it.
	l := h.launcher()
	task, dup, err := l.reserve(LaunchRequest{
		Project: "demo", Prompt: "fix it", Origin: Origin{RequestID: "msg-1"},
	}, defaultLaunchLease)
	if err != nil || dup {
		t.Fatalf("reserve: dup=%v err=%v", dup, err)
	}
	if task.State != StateLaunching {
		t.Fatalf("state is %s, want %s", task.State, StateLaunching)
	}

	res := h.launch("msg-1", "fix it")
	if !res.Duplicate {
		t.Fatal("a redelivery during an in-flight launch must not launch again")
	}
	if len(h.wts.created) != 0 {
		t.Fatalf("a worktree was created behind the in-flight launch: %v", h.wts.created)
	}
}

// Herdr accepting the prompt is not evidence it ran: for Claude Code the
// delivery is a paste. One Enter is allowed; the text is never sent twice.
func TestStagedPromptIsSubmittedWithASingleEnter(t *testing.T) {
	h := newHarness(t)
	h.herdr.workOnPrompt = false
	h.herdr.workOnEnter = true

	res := h.launch("msg-1", "fix the login bug")
	if !res.Confirmed {
		t.Fatalf("expected confirmation after Enter, got %s: %s", res.Task.State, res.Detail)
	}
	if got := h.herdr.promptCount(); got != 1 {
		t.Fatalf("prompt text was sent %d times, want once", got)
	}
	if len(h.herdr.keys) != 1 || h.herdr.keys[0] != "enter" {
		t.Fatalf("expected exactly one Enter, got %v", h.herdr.keys)
	}
	if !res.Task.LaunchEnterSent {
		t.Fatal("the Enter was not recorded, so a retry could press it again")
	}
}

// If the agent never moves, the launch says so instead of claiming success —
// and does not resend the prompt.
func TestUnmovedAgentIsReportedUnconfirmed(t *testing.T) {
	h := newHarness(t)
	h.herdr.workOnPrompt = false
	h.herdr.workOnEnter = false

	res := h.launch("msg-1", "fix the login bug")
	if res.Confirmed {
		t.Fatal("an agent that never moved was reported as confirmed")
	}
	if res.Task.State != StateUnconfirmed {
		t.Fatalf("state is %s, want %s", res.Task.State, StateUnconfirmed)
	}
	if got := h.herdr.promptCount(); got != 1 {
		t.Fatalf("prompt text was sent %d times, want once", got)
	}
	if !strings.Contains(res.Detail, "will not be sent again") {
		t.Fatalf("the detail should say the text is not resent: %s", res.Detail)
	}
}

// A blocked agent has read the prompt and is asking about it — that counts as
// started, and must not trigger the Enter fallback, which would answer the
// question rather than submit anything.
func TestBlockedAgentCountsAsDispatched(t *testing.T) {
	h := newHarness(t)
	h.herdr.workOnPrompt = false
	h.herdr.blockOnPrompt = true

	res := h.launch("msg-1", "which database should the worker write to?")
	if !res.Confirmed {
		t.Fatalf("a blocked agent should count as dispatched, got %s: %s", res.Task.State, res.Detail)
	}
	if len(h.herdr.keys) != 0 {
		t.Fatalf("Enter was pressed on a blocked agent: %v", h.herdr.keys)
	}
	if res.Task.HasWorked {
		t.Fatal("blocked is not working; HasWorked must stay false until the agent runs")
	}
}

func TestLaunchValidatesTheRequest(t *testing.T) {
	h := newHarness(t)
	cases := map[string]LaunchRequest{
		"no project":    {Prompt: "x", Origin: Origin{RequestID: "m"}},
		"no prompt":     {Project: "demo", Origin: Origin{RequestID: "m"}},
		"no request id": {Project: "demo", Prompt: "x"},
		"control chars": {Project: "demo", Prompt: "rm -rf /\x1b[A", Origin: Origin{RequestID: "m"}},
		"carriage ret":  {Project: "demo", Prompt: "yes\rno", Origin: Origin{RequestID: "m"}},
		"too long":      {Project: "demo", Prompt: strings.Repeat("x", maxPromptChars+1), Origin: Origin{RequestID: "m"}},
		"bad gate":      {Project: "demo", Prompt: "x", Origin: Origin{RequestID: "m"}, Gate: "maybe"},
	}
	for name, req := range cases {
		if _, err := h.launcher().Launch(req); err == nil {
			t.Fatalf("%s: expected a validation error", name)
		}
	}
	if len(h.wts.created) != 0 {
		t.Fatalf("a rejected request still created a worktree: %v", h.wts.created)
	}
}

// The gate is decided from the request text before anything is dispatched, so a
// dispatcher that dies mid-send still leaves the completion side able to tell a
// report from a code change.
func TestGateIsRecordedAtLaunch(t *testing.T) {
	h := newHarness(t)
	report := h.launch("msg-1", "research our Shopee competitors and write it up")
	if report.Task.Gate != GateRequired {
		t.Fatalf("gate is %s, want %s", report.Task.Gate, GateRequired)
	}
	code := h.launch("msg-2", "fix the crash in the uploader")
	if code.Task.Gate != GateNotNeeded {
		t.Fatalf("gate is %s, want %s", code.Task.Gate, GateNotNeeded)
	}
	vague := h.launch("msg-3", "have a look at the uploader")
	if vague.Task.Gate != GateUndecided {
		t.Fatalf("gate is %s, want %s", vague.Task.Gate, GateUndecided)
	}
}

func TestExplicitGateOverridesTheClassifier(t *testing.T) {
	h := newHarness(t)
	res, err := h.launcher().Launch(LaunchRequest{
		Project: "demo", Prompt: "fix the crash in the uploader",
		Gate:   GateRequired,
		Origin: Origin{RequestID: "msg-1"},
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if res.Task.Gate != GateRequired || res.Task.GateDecidedBy != "user" {
		t.Fatalf("gate is %s by %s, want %s by user", res.Task.Gate, res.Task.GateDecidedBy, GateRequired)
	}
}

// A slow-booting agent is normal; prompting the shell before Claude Code is up
// would type the request into bash.
func TestLaunchWaitsForInteractiveReadiness(t *testing.T) {
	h := newHarness(t)
	h.herdr.readyAfterGets = 4

	res := h.launch("msg-1", "fix the login bug")
	if !res.Confirmed {
		t.Fatalf("expected confirmation, got %s: %s", res.Task.State, res.Detail)
	}
	if h.herdr.gets < 4 {
		t.Fatalf("only %d agent reads; the launcher did not wait for readiness", h.herdr.gets)
	}
}

func TestLaunchFailsWhenTheAgentNeverBecomesReady(t *testing.T) {
	h := newHarness(t)
	h.herdr.readyAfterGets = 1 << 30 // never

	_, err := h.launcher().Launch(LaunchRequest{
		Project: "demo", Prompt: "fix it", Origin: Origin{RequestID: "msg-1"},
	})
	if err == nil {
		t.Fatal("expected a failure when the agent never becomes ready")
	}
	if !strings.Contains(err.Error(), "never became ready") {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.herdr.promptCount() != 0 {
		t.Fatal("the prompt was delivered to a pane that was not an agent yet")
	}
}

func TestGoneAgentErrorIsRecognisable(t *testing.T) {
	h := newHarness(t)
	_, err := h.herdr.GetAgent("nope:p1")
	if !isGoneErr(err) {
		t.Fatalf("expected ErrAgentGone, got %v", err)
	}
}

// A long originating message id is shortened by hashing, not by slicing: two
// snowflakes that share a prefix must not name the same task.
func TestTaskIDsStayUniqueWhenShortened(t *testing.T) {
	a := newTaskID("demo", "1402998877665544332")
	b := newTaskID("demo", "1402998877665544399")
	if a == b {
		t.Fatalf("two different messages produced the same task id: %s", a)
	}
	if len(a) > len("demo-")+maxIDSuffix {
		t.Fatalf("task id %q is too long for a pane title", a)
	}
	if newTaskID("demo", "1402998877665544332") != a {
		t.Fatal("task ids are not stable across calls")
	}
	if got := newTaskID("demo", "msg-77"); got != "demo-msg-77" {
		t.Fatalf("a short readable id was mangled: %s", got)
	}
}
