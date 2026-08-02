package orchestration

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeHerdr stands in for the Herdr server. It is scriptable rather than
// stateful-by-magic: a test says what the agent does and when, which is the
// only way to exercise "the agent worked for three hours" without waiting.
type fakeHerdr struct {
	mu sync.Mutex

	agents     map[string]*AgentInfo
	workspaces map[string]string
	paneN      int

	// createErr, promptErr and keysErr inject failures.
	createErr error
	promptErr error
	keysErr   error
	// getErr, when set, makes every GetAgent fail — a disconnected Herdr.
	getErr error

	// readyAfterGets makes the agent report interactive_ready only from the
	// Nth GetAgent onwards, modelling a Claude Code that takes a moment to boot.
	readyAfterGets int

	// workOnPrompt / workOnEnter say whether delivering the prompt (or the
	// single Enter) actually starts the agent. blockOnPrompt models an agent
	// that reads the prompt and immediately asks a question about it.
	workOnPrompt  bool
	workOnEnter   bool
	blockOnPrompt bool

	// counters and transcripts the tests assert on.
	gets    int
	runs    []string
	prompts []string
	keys    []string
	renames []string
	creates []string
}

func newFakeHerdr() *fakeHerdr {
	return &fakeHerdr{
		agents:       map[string]*AgentInfo{},
		workspaces:   map[string]string{},
		workOnPrompt: true,
	}
}

func (f *fakeHerdr) CreateWorkspace(label, cwd string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", "", f.createErr
	}
	f.paneN++
	ws := fmt.Sprintf("w%d", f.paneN)
	pane := ws + ":p1"
	f.workspaces[label] = ws
	f.agents[pane] = &AgentInfo{
		PaneID:      pane,
		WorkspaceID: ws,
		TerminalID:  fmt.Sprintf("term_%d", f.paneN),
		Agent:       "claude",
		Status:      "idle",
		CWD:         cwd,
	}
	f.creates = append(f.creates, label+" @ "+cwd)
	return pane, ws, nil
}

func (f *fakeHerdr) FindWorkspace(label string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.workspaces[label]
	return id, ok
}

func (f *fakeHerdr) RenamePane(paneID, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renames = append(f.renames, "pane:"+paneID+":"+title)
	return nil
}

func (f *fakeHerdr) RunInPane(paneID, command string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, command)
	return nil
}

func (f *fakeHerdr) GetAgent(target string) (*AgentInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrAgentGone, f.getErr)
	}
	a, ok := f.agents[target]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAgentGone, target)
	}
	cp := *a
	cp.InteractiveReady = f.gets >= f.readyAfterGets
	return &cp, nil
}

func (f *fakeHerdr) RenameAgent(target, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renames = append(f.renames, "agent:"+target+":"+name)
	if a, ok := f.agents[target]; ok {
		a.Name = name
	}
	return nil
}

func (f *fakeHerdr) Prompt(target, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.promptErr != nil {
		return f.promptErr
	}
	f.prompts = append(f.prompts, text)
	switch {
	case f.blockOnPrompt:
		f.setLocked(target, "blocked")
	case f.workOnPrompt:
		f.setLocked(target, "working")
	}
	return nil
}

func (f *fakeHerdr) SendKeys(target string, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, keys...)
	if f.keysErr != nil {
		return f.keysErr
	}
	if f.workOnEnter {
		f.setLocked(target, "working")
	}
	return nil
}

// set drives the agent's observable state, standing in for the passage of real
// work: a test calls it to say "and now the agent finished".
func (f *fakeHerdr) set(pane, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setLocked(pane, status)
}

func (f *fakeHerdr) setLocked(pane, status string) {
	a, ok := f.agents[pane]
	if !ok {
		return
	}
	a.Status = status
	a.StateChangeSeq++
}

// recycle simulates the pane being reused by a different terminal.
func (f *fakeHerdr) recycle(pane string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.agents[pane]; ok {
		a.TerminalID = a.TerminalID + "-recycled"
		a.StateChangeSeq++
	}
}

func (f *fakeHerdr) promptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prompts)
}

// fakeWorktrees hands out a new directory per call and can be told to
// (wrongly) return the root checkout, so the guard against that has something
// to catch.
type fakeWorktrees struct {
	root       string
	n          int
	err        error
	returnRoot bool
	created    []WorktreeRef
}

func (w *fakeWorktrees) CreateFresh(project string) (WorktreeRef, error) {
	if w.err != nil {
		return WorktreeRef{}, w.err
	}
	w.n++
	ref := WorktreeRef{
		Name:     fmt.Sprintf("city%d", w.n),
		Path:     filepath.Join(w.root, fmt.Sprintf("wt%d", w.n)),
		Branch:   fmt.Sprintf("city%d", w.n),
		RepoRoot: w.root,
	}
	if w.returnRoot {
		ref.Path = w.root
		ref.Branch = "main"
	}
	w.created = append(w.created, ref)
	return ref, nil
}

func (w *fakeWorktrees) RootPath(project string) (string, error) {
	return w.root, nil
}

// testClock is a hand-wound clock. Every wait in this package is budgeted, and
// a budget is only testable if time moves when the test says so.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// Sleep moves the clock instead of blocking, so a 45-second budget costs a
// test nothing.
func (c *testClock) Sleep(d time.Duration) { c.Advance(d) }

// harness wires a store, a fake Herdr and a fake worktree source over a temp
// directory, with time under the test's control.
type harness struct {
	t     *testing.T
	dir   string
	store *Store
	herdr *fakeHerdr
	wts   *fakeWorktrees
	clock *testClock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	clock := newTestClock()
	st := NewStore(filepath.Join(dir, "state"))
	st.SetClock(clock.Now)
	return &harness{
		t:     t,
		dir:   dir,
		store: st,
		herdr: newFakeHerdr(),
		wts:   &fakeWorktrees{root: filepath.Join(dir, "repo")},
		clock: clock,
	}
}

func (h *harness) launcher() *Launcher {
	return &Launcher{
		Store:     h.store,
		Herdr:     h.herdr,
		Worktrees: h.wts,
		Now:       h.clock.Now,
		Sleep:     h.clock.Sleep,
	}
}

func (h *harness) monitor() *Monitor {
	return &Monitor{Store: h.store, Herdr: h.herdr, Now: h.clock.Now}
}

// reopen returns a monitor built on a *fresh* store handle over the same files,
// which is what a restarted process gets.
func (h *harness) reopen() *Monitor {
	st := NewStore(filepath.Join(h.dir, "state"))
	st.SetClock(h.clock.Now)
	return &Monitor{Store: st, Herdr: h.herdr, Now: h.clock.Now}
}

func (h *harness) launch(requestID, prompt string) *LaunchResult {
	h.t.Helper()
	res, err := h.launcher().Launch(LaunchRequest{
		Project: "demo",
		Prompt:  prompt,
		Origin:  Origin{RequestID: requestID, Platform: "discord", ChannelID: "c1", ThreadID: "t1"},
	})
	if err != nil {
		h.t.Fatalf("launch: %v", err)
	}
	return res
}

func mustGet(t *testing.T, s *Store, id string) *Task {
	t.Helper()
	task, err := s.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return task
}

func isGoneErr(err error) bool { return errors.Is(err, ErrAgentGone) }
