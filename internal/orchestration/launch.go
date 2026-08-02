package orchestration

import (
	"errors"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/mux"
)

// Worktrees is the isolation boundary: the only way this package obtains a
// place for an agent to work. There is deliberately no method that returns an
// *existing* checkout, so no code path can hand an agent the root repository.
type Worktrees interface {
	// CreateFresh creates a brand-new worktree for project and returns it.
	CreateFresh(project string) (WorktreeRef, error)
	// RootPath is the project's root checkout — the one place a task must
	// never run.
	RootPath(project string) (string, error)
}

const (
	// maxPromptChars mirrors the bridge's cap; a prompt longer than this is a
	// file, not a message.
	maxPromptChars = 4000
	// defaultReadyBudget bounds the wait for Claude Code to reach an
	// interactive prompt. Booting an agent is seconds; this is generous enough
	// for a cold start and far short of "watching it work".
	defaultReadyBudget = 45 * time.Second
	// defaultConfirmBudget bounds the wait for the agent to *move* after the
	// prompt is delivered.
	defaultConfirmBudget = 20 * time.Second
	defaultPollInterval  = 500 * time.Millisecond
	// defaultLaunchLease is how long one launch attempt may hold the task
	// before a duplicate delivery is allowed to retry it. Longer than a normal
	// launch, shorter than a human's patience.
	defaultLaunchLease = 5 * time.Minute
)

// controlChars rejects anything a terminal would read as a keystroke. Newline
// is allowed (a multi-line prompt is ordinary); tab and carriage return are
// not, because they complete and submit.
var controlChars = regexp.MustCompile(`[\x00-\x09\x0b-\x1f\x7f]`)

// Launcher turns a chat request into a running agent.
type Launcher struct {
	Store     *Store
	Herdr     Herdr
	Worktrees Worktrees
	// Agent defaults to Claude Code.
	Agent codingagent.Agent

	Now           func() time.Time
	Sleep         func(time.Duration)
	ReadyBudget   time.Duration
	ConfirmBudget time.Duration
	PollInterval  time.Duration
	LaunchLease   time.Duration
}

// LaunchRequest is one chat message asking for work.
type LaunchRequest struct {
	Project string
	Prompt  string
	Origin  Origin
	// TaskID is optional; a stable id from the caller is preferred because it
	// is what a human sees in Herdr's pane list.
	TaskID string
	// Gate overrides the classifier. Empty means "classify the prompt".
	Gate ReadbackGate
}

// LaunchResult is what the caller reports back into the chat thread.
type LaunchResult struct {
	Task *Task
	// Duplicate means this originating message had already been launched and
	// nothing new was created.
	Duplicate bool
	// Confirmed means the agent was observed working or blocked after the
	// prompt — the only basis on which a caller may say the task started.
	Confirmed bool
	Detail    string
}

func (l *Launcher) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Launcher) sleep(d time.Duration) {
	if l.Sleep != nil {
		l.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (l *Launcher) agent() codingagent.Agent {
	if l.Agent == "" {
		return codingagent.ClaudeCode
	}
	return l.Agent
}

func (l *Launcher) budgets() (ready, confirm, poll, lease time.Duration) {
	ready, confirm = l.ReadyBudget, l.ConfirmBudget
	poll, lease = l.PollInterval, l.LaunchLease
	if ready <= 0 {
		ready = defaultReadyBudget
	}
	if confirm <= 0 {
		confirm = defaultConfirmBudget
	}
	if poll <= 0 {
		poll = defaultPollInterval
	}
	if lease <= 0 {
		lease = defaultLaunchLease
	}
	return
}

// Launch creates a fresh worktree, starts the coding agent in it, delivers the
// prompt, and returns as soon as the agent is confirmed working — or as soon as
// the confirm budget is spent, with an honest account of what it saw.
//
// It never waits for the task to finish. It never runs the agent anywhere but a
// new worktree. And it never starts a second agent for an originating message
// it has already handled.
func (l *Launcher) Launch(req LaunchRequest) (*LaunchResult, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	_, _, _, lease := l.budgets()

	task, duplicate, err := l.reserve(req, lease)
	if err != nil {
		return nil, err
	}
	if duplicate {
		return &LaunchResult{
			Task:      task,
			Duplicate: true,
			Confirmed: task.State == StateWorking || task.State == StateBlocked,
			Detail: fmt.Sprintf("request %s already launched task %s (%s); nothing new was created",
				req.Origin.RequestID, task.ID, task.State),
		}, nil
	}

	res, launchErr := l.run(task, req)
	if launchErr != nil {
		// The failure is recorded rather than swallowed: a duplicate delivery
		// of the same message is then allowed to retry it, and an operator can
		// see which worktree (if any) was left behind.
		stored, updErr := l.Store.Update(task.ID, func(t *Task) error {
			t.State = StateFailed
			t.Detail = launchErr.Error()
			t.LaunchLeaseUntil = time.Time{}
			if res != nil {
				t.Worktree = res.Worktree
				t.Agent = res.Agent
			}
			t.appendProgress(ProgressEvent{
				At: l.now(), Kind: "error", Detail: launchErr.Error()})
			return nil
		})
		if updErr != nil {
			return nil, fmt.Errorf("%w (and the failure could not be recorded: %v)", launchErr, updErr)
		}
		return &LaunchResult{Task: stored, Detail: launchErr.Error()}, launchErr
	}

	stored, err := l.Store.Update(task.ID, func(t *Task) error {
		t.Worktree = res.Worktree
		t.Agent = res.Agent
		t.State = res.State
		t.Detail = res.Detail
		t.HasWorked = res.HasWorked
		t.LastStatus = res.LastStatus
		t.LastSeq = res.LastSeq
		t.LastObservedAt = l.now()
		t.LaunchEnterSent = t.LaunchEnterSent || res.EnterSent
		t.LaunchLeaseUntil = time.Time{}
		t.appendProgress(ProgressEvent{
			At: l.now(), Kind: "launched", Status: res.LastStatus,
			StateChangeSeq: res.LastSeq,
			Detail: fmt.Sprintf("agent %s started in %s on branch %s; %s%s",
				t.Agent.PaneID, res.Worktree.Path, res.Worktree.Branch, res.Detail,
				setupNote(res.Worktree)),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &LaunchResult{
		Task:      stored,
		Confirmed: res.Confirmed,
		Detail:    res.Detail,
	}, nil
}

func validateRequest(req LaunchRequest) error {
	if strings.TrimSpace(req.Project) == "" {
		return errors.New("project is required")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return errors.New("prompt text is empty")
	}
	if strings.TrimSpace(req.Origin.RequestID) == "" {
		return errors.New("origin request id is required; it is the idempotency key for the originating message")
	}
	if len(req.Prompt) > maxPromptChars {
		return fmt.Errorf("prompt text is %d chars, limit is %d",
			len(req.Prompt), maxPromptChars)
	}
	if controlChars.MatchString(req.Prompt) || strings.Contains(req.Prompt, "\r") {
		return errors.New("prompt text contains control characters, which a terminal would interpret as keystrokes")
	}
	if req.Gate != "" && req.Gate != GateRequired && req.Gate != GateNotNeeded && req.Gate != GateUndecided {
		return fmt.Errorf("unknown readback gate %q", req.Gate)
	}
	return nil
}

// reserve claims the originating message under lock, before any slow work.
//
// The ordering is the whole point: a crash between "worktree created" and
// "agent started" leaves a record, and the retry that follows sees a lease
// rather than an empty store.
func (l *Launcher) reserve(req LaunchRequest, lease time.Duration) (*Task, bool, error) {
	now := l.now()
	existing, err := l.Store.GetByRequest(req.Origin.RequestID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	if existing != nil && err == nil {
		switch {
		case existing.Agent.PaneID != "":
			// An agent exists for this message. Never launch a second one,
			// whatever state it is in.
			return existing, true, nil
		case existing.State == StateLaunching && now.Before(existing.LaunchLeaseUntil):
			// Another delivery of the same message is mid-launch right now.
			return existing, true, nil
		case existing.State == StateLaunching || existing.State == StateFailed:
			// The previous attempt died without producing an agent; this
			// delivery is allowed to finish the job under the same task id.
			retried, uerr := l.Store.Update(existing.ID, func(t *Task) error {
				t.State = StateLaunching
				t.Detail = "retrying a launch that left no agent behind"
				t.LaunchAttempts++
				t.LaunchLeaseUntil = now.Add(lease)
				t.appendProgress(ProgressEvent{
					At: now, Kind: "launched",
					Detail: fmt.Sprintf("retry %d after an incomplete launch", t.LaunchAttempts)})
				return nil
			})
			return retried, false, uerr
		default:
			return existing, true, nil
		}
	}

	gate := req.Gate
	decidedBy := "user"
	if gate == "" {
		gate = ClassifyGate(req.Prompt)
		decidedBy = "classifier"
	}
	id := req.TaskID
	if id == "" {
		id = newTaskID(req.Project, req.Origin.RequestID)
	}
	t := &Task{
		ID:               id,
		Project:          req.Project,
		Prompt:           req.Prompt,
		Origin:           req.Origin,
		State:            StateLaunching,
		Gate:             gate,
		GateDecidedBy:    decidedBy,
		Tests:            TestsUnknown,
		LaunchAttempts:   1,
		LaunchLeaseUntil: now.Add(lease),
	}
	t.appendProgress(ProgressEvent{
		At: now, Kind: "gate",
		Detail: fmt.Sprintf("readback gate set to %s by %s", gate, decidedBy)})
	if err := l.Store.Create(t); err != nil {
		return nil, false, err
	}
	return t, false, nil
}

// launchOutcome is the result of the unlocked, slow part of a launch.
type launchOutcome struct {
	Worktree   WorktreeRef
	Agent      AgentRef
	State      TaskState
	Detail     string
	HasWorked  bool
	LastStatus string
	LastSeq    int64
	EnterSent  bool
	// Confirmed means the agent was observed moving after the prompt landed —
	// the only basis on which a caller may say the task started.
	Confirmed bool
}

func (l *Launcher) run(task *Task, req LaunchRequest) (*launchOutcome, error) {
	out := &launchOutcome{}

	wt, err := l.Worktrees.CreateFresh(req.Project)
	if err != nil {
		return out, fmt.Errorf("create worktree for %s: %w", req.Project, err)
	}
	if err := l.assertIsolated(req.Project, wt); err != nil {
		return out, err
	}
	out.Worktree = wt

	label := mux.Herdr().WindowName(req.Project, wt.Branch)
	paneID, workspaceID, err := l.Herdr.CreateWorkspace(label, wt.Path)
	if err != nil {
		return out, fmt.Errorf("create herdr workspace %q: %w", label, err)
	}
	out.Agent = AgentRef{PaneID: paneID, WorkspaceID: workspaceID}
	_ = l.Herdr.RenamePane(paneID, wt.Branch+" - "+l.agent().PaneLabel())

	if err := l.startAgent(paneID, wt); err != nil {
		return out, err
	}

	info, err := l.waitInteractive(paneID)
	if err != nil {
		return out, err
	}
	out.Agent.TerminalID = info.TerminalID
	out.Agent.Agent = info.Agent
	if info.WorkspaceID != "" {
		out.Agent.WorkspaceID = info.WorkspaceID
	}
	if err := l.Herdr.RenameAgent(paneID, task.ID); err == nil {
		out.Agent.AgentName = task.ID
	}

	before := info
	if err := l.Herdr.Prompt(paneID, req.Prompt); err != nil {
		return out, fmt.Errorf("deliver prompt to %s: %w", paneID, err)
	}
	l.confirm(task, out, before)
	return out, nil
}

// assertIsolated is the guard that requirement exists for: an agent must never
// run in the project's root checkout, and a worktree record with no path is not
// a worktree.
func (l *Launcher) assertIsolated(project string, wt WorktreeRef) error {
	if wt.Path == "" {
		return fmt.Errorf("worktree for %s has no path", project)
	}
	root, err := l.Worktrees.RootPath(project)
	if err != nil {
		return err
	}
	if root == "" {
		return fmt.Errorf("cannot determine the root checkout for %s; refusing to launch", project)
	}
	if sameDir(root, wt.Path) {
		return fmt.Errorf(
			"refusing to run an agent in the root checkout of %s (%s); a fresh worktree is required",
			project, root)
	}
	return nil
}

func sameDir(a, b string) bool {
	ca := filepath.Clean(a)
	cb := filepath.Clean(b)
	if ca == cb {
		return true
	}
	// Symlinked roots (/tmp vs /private/tmp on macOS) would otherwise slip past
	// a string compare.
	ra, errA := filepath.EvalSymlinks(ca)
	rb, errB := filepath.EvalSymlinks(cb)
	if errA == nil && errB == nil {
		return ra == rb
	}
	return false
}

// startAgent runs the coding agent in the pane. For Claude Code the argv always
// carries --dangerously-skip-permissions (an agent that stops to ask about
// every command is not usable unattended) and CLAUDE_CODE_NO_FLICKER=1.
func (l *Launcher) startAgent(paneID string, wt WorktreeRef) error {
	agent := l.agent()
	argv := agent.InteractiveArgs(mux.HerdrAgentPrompt())
	if agent == codingagent.ClaudeCode {
		if err := assertUnattendedClaude(argv); err != nil {
			return err
		}
	}
	if agent.UsesContextFile() {
		if err := codingagent.WriteContextFile(wt.Path, mux.HerdrAgentPrompt()); err != nil {
			return fmt.Errorf("write agent context file: %w", err)
		}
	}
	if err := l.Herdr.RunInPane(paneID, mux.ShellJoin(argv)); err != nil {
		return fmt.Errorf("start %s in pane %s: %w", agent.Label(), paneID, err)
	}
	return nil
}

// assertUnattendedClaude fails the launch rather than starting an agent that
// will stall on a permission prompt nobody is watching. It is a belt-and-braces
// check on codingagent's argv, so a future edit there cannot quietly remove the
// flag this contract depends on.
func assertUnattendedClaude(argv []string) error {
	var hasSkip, hasNoFlicker bool
	for _, a := range argv {
		switch a {
		case "--dangerously-skip-permissions":
			hasSkip = true
		case "CLAUDE_CODE_NO_FLICKER=1":
			hasNoFlicker = true
		}
	}
	if !hasSkip {
		return fmt.Errorf(
			"refusing to launch Claude Code without --dangerously-skip-permissions: "+
				"an orchestrated agent has no human to answer permission prompts (argv: %v)", argv)
	}
	if !hasNoFlicker {
		return fmt.Errorf(
			"refusing to launch Claude Code without CLAUDE_CODE_NO_FLICKER=1: "+
				"pane reads are how progress is observed (argv: %v)", argv)
	}
	return nil
}

// waitInteractive blocks until Herdr reports the agent ready for input, or the
// ready budget expires. This is the one genuinely necessary wait in a launch:
// prompting a shell that has not yet become an agent types the request into
// bash.
func (l *Launcher) waitInteractive(paneID string) (*AgentInfo, error) {
	ready, _, poll, _ := l.budgets()
	deadline := l.now().Add(ready)
	var last error
	for {
		info, err := l.Herdr.GetAgent(paneID)
		if err == nil && info.InteractiveReady {
			return info, nil
		}
		if err != nil {
			last = err
		}
		if !l.now().Before(deadline) {
			if last != nil {
				return nil, fmt.Errorf(
					"agent in pane %s never became ready within %s: %w", paneID, ready, last)
			}
			return nil, fmt.Errorf(
				"agent in pane %s never became ready for input within %s", paneID, ready)
		}
		l.sleep(poll)
	}
}

// confirm establishes whether the delivered prompt is actually running.
//
// Herdr accepting `agent prompt` is not evidence: for Claude Code the delivery
// is a paste, and a pasted prompt sits in the composer until something submits
// it. So this watches for a status *transition*, and if the agent has not moved
// it presses Enter exactly once — safe here, and only here, because the pane was
// created moments ago by this launch and nobody else has typed into it.
func (l *Launcher) confirm(task *Task, out *launchOutcome, before *AgentInfo) {
	_, budget, poll, _ := l.budgets()
	deadline := l.now().Add(budget)
	paneID := out.Agent.PaneID
	wasWorking := isWorking(before.Status)

	if info := l.awaitMovement(paneID, wasWorking, deadline, poll); info != nil {
		l.recordMovement(out, info, "the agent started working on the delivered prompt")
		return
	}

	if !task.LaunchEnterSent && !out.EnterSent {
		if err := l.Herdr.SendKeys(paneID, "enter"); err != nil {
			out.State = StateUnconfirmed
			out.Detail = fmt.Sprintf(
				"the prompt was delivered to %s but the agent did not move and Enter could not be sent (%v); "+
					"submit it in the pane — the text will not be sent again", paneID, err)
			// Reported as sent: the keystroke may have landed before the error,
			// and this path must never invite a second attempt.
			out.EnterSent = true
			return
		}
		out.EnterSent = true
		if info := l.awaitMovement(paneID, wasWorking, l.now().Add(budget), poll); info != nil {
			l.recordMovement(out, info,
				"the prompt was staged in the composer; sent Enter once and the agent started")
			return
		}
	}

	out.State = StateUnconfirmed
	out.Detail = fmt.Sprintf(
		"the prompt was delivered to %s but the agent was not observed working within %s; "+
			"the text will not be sent again — check the pane", paneID, budget)
	if info, err := l.Herdr.GetAgent(paneID); err == nil {
		out.LastStatus = info.Status
		out.LastSeq = info.StateChangeSeq
	}
}

// recordMovement stores the state the agent was actually observed in, so a
// monitor that samples a second later sees the same thing the launch reported
// rather than a launch-flavoured state it has to reconcile.
func (l *Launcher) recordMovement(out *launchOutcome, info *AgentInfo, detail string) {
	out.Confirmed = true
	if isBlocked(info.Status) {
		out.State = StateBlocked
	} else {
		out.State = StateWorking
	}
	out.Detail = detail
	out.LastStatus = info.Status
	out.LastSeq = info.StateChangeSeq
	out.HasWorked = isWorking(info.Status)
}

// awaitMovement polls for the transition a submitted prompt causes. A bare
// state-change bump is deliberately not enough: pasting text changes pane state
// without running anything.
func (l *Launcher) awaitMovement(paneID string, wasWorking bool, deadline time.Time,
	poll time.Duration) *AgentInfo {
	for {
		info, err := l.Herdr.GetAgent(paneID)
		if err == nil {
			if isWorking(info.Status) && !wasWorking {
				return info
			}
			if isBlocked(info.Status) {
				// An agent that went to blocked has read the prompt and is
				// asking about it — it ran.
				return info
			}
		}
		if !l.now().Before(deadline) {
			return nil
		}
		l.sleep(poll)
	}
}

// setupNote surfaces a setup script that did not finish cleanly. Silence here
// is how "the tests won't run in that worktree" becomes a mystery an hour later.
func setupNote(wt WorktreeRef) string {
	switch wt.SetupStatus {
	case "", "done":
		return ""
	default:
		return " (setup script: " + wt.SetupStatus + ")"
	}
}

func isWorking(status string) bool { return status == "working" }
func isBlocked(status string) bool { return status == "blocked" }

// isSettled reports a status in which the agent is waiting for a human rather
// than doing anything.
func isSettled(status string) bool {
	switch status {
	case "idle", "done":
		return true
	}
	return false
}

// newTaskID builds a readable, collision-resistant id from the project and the
// originating message.
//
// A Discord snowflake is nineteen digits and a task id ends up in a pane title,
// so long ids get shortened — but by hashing the whole thing rather than by
// slicing it, because two ids that differ only in the part that was sliced off
// would otherwise collide.
func newTaskID(project, requestID string) string {
	slug := slugify(project)
	suffix := slugify(requestID)
	if suffix == "" {
		suffix = "task"
	}
	if len(suffix) > maxIDSuffix {
		suffix = suffix[:maxIDSuffix-7] + "-" + shortHash(requestID)
	}
	return slug + "-" + suffix
}

const maxIDSuffix = 16

// shortHash is FNV-1a in hex: six characters, no dependencies, and stable
// across processes so the same message always names the same task.
func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%06x", h.Sum32()&0xffffff)
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}
