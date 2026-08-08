package mux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/session"
	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/hammashamzah/conductor/internal/tmux"
)

// t3Mux drives T3 Code (https://github.com/pingdotgg/t3code) over its local
// HTTP and WebSocket API.
//
// Model mapping: a conductor worktree window is split across two hosts.
//
//   - The coding agent is a T3 *thread* bound to the worktree path. T3 runs the
//     agent itself, so conductor spawns no agent process.
//   - The dev server is a tmux window holding a single pane.
//
// The dev server is deliberately not a T3 terminal. T3 spawns its terminals
// from the server process, so they live in its cgroup and every dev server
// dies when T3 restarts or updates. The tmux server is independent and
// long-lived, so a dev server hosted there survives all of that. One worktree
// gets one dev window.
type t3Mux struct{}

// T3 returns the T3 Code-backed Multiplexer.
func T3() Multiplexer { return t3Mux{} }

func (t3Mux) Kind() Kind { return KindT3 }

// CheckInstalled reports whether a T3 server is running and conductor is
// authorized against it. Unlike tmux and herdr there is no binary to look for:
// what matters is a reachable server, so this is a connectivity check.
func (t3Mux) CheckInstalled() error {
	client, err := t3.New()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.Ping(ctx)
}

func (t3Mux) InstallGuide() string {
	tokenPath, _ := t3.TokenPath()
	return fmt.Sprintf(`T3 Code is not reachable.

1. Make sure the T3 Code server is running.

2. Issue a token for conductor and save it:
     t3 auth session issue --label conductor --ttl 365d --token-only > %s
     chmod 600 %s

Override discovery with CONDUCTOR_T3_ORIGIN and CONDUCTOR_T3_TOKEN if needed.`,
		tokenPath, tokenPath)
}

// IsInsideSession reports whether conductor is running inside a T3 terminal.
// T3 exports the thread id into every pty it spawns.
func (t3Mux) IsInsideSession() bool {
	return os.Getenv("T3CODE_HOME") != "" || os.Getenv("T3_THREAD_ID") != ""
}

// IsInsideConductorSession is equivalent to IsInsideSession here: T3 has no
// named sessions, so there is no separate conductor-owned one to distinguish.
func (m t3Mux) IsInsideConductorSession() bool { return m.IsInsideSession() }

func (t3Mux) SessionName() string { return "t3" }

// StartSession opens the T3 web UI. There is no terminal client to exec into,
// so unlike tmux and herdr this returns rather than replacing the process.
func (t3Mux) StartSession() error {
	origin, err := t3.DiscoverOrigin()
	if err != nil {
		return err
	}
	fmt.Printf("T3 Code is at %s\n", origin)
	return nil
}

// DetachSession is a no-op: T3 threads run server-side regardless of who is
// looking at them.
func (t3Mux) DetachSession() error { return nil }

func (t3Mux) WindowName(project, branch string) string {
	return fmt.Sprintf("%s/%s", project, branch)
}

func (m t3Mux) WindowExists(project, branch string) bool {
	_, _, err := m.findThread(project, branch)
	return err == nil
}

// ListWindowNames returns "project/branch" for every live worktree-bound
// thread, derived from the worktree path rather than the thread title — T3
// rewrites titles from the conversation, so they are not stable identifiers.
func (m t3Mux) ListWindowNames() []string {
	client, err := t3.New()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snapshot, err := client.Shell(ctx)
	if err != nil {
		return nil
	}

	base, err := config.ConductorDir()
	if err != nil {
		return nil
	}

	var names []string
	for _, thread := range snapshot.LiveThreadsWithWorktrees() {
		if name, ok := windowNameFromWorktree(base, thread.Worktree()); ok {
			names = append(names, name)
		}
	}
	return names
}

// windowNameFromWorktree turns ~/.conductor/<project>/<branch> back into
// "project/branch". Worktrees outside the conductor directory are ignored:
// they belong to threads conductor did not create.
func windowNameFromWorktree(conductorDir, worktreePath string) (string, bool) {
	rel, err := filepath.Rel(conductorDir, worktreePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

func (m t3Mux) CreateCodingWindow(project, branch, worktreePath string, agent codingagent.Agent) error {
	return m.createWindow(project, branch, worktreePath, agent, "")
}

func (m t3Mux) CreateCodingWindowWithTask(project, branch, worktreePath, taskPrompt string, agent codingagent.Agent) error {
	return m.createWindow(project, branch, worktreePath, agent, taskPrompt)
}

// createWindow opens the thread for a worktree and starts its dev server.
//
// The agent is not launched as a subprocess the way tmux and herdr do it — T3
// runs the agent itself, driven by the thread's configured provider. The agent
// argument therefore only selects the context file, if that agent needs one.
func (m t3Mux) createWindow(project, branch, worktreePath string, agent codingagent.Agent, taskPrompt string) error {
	client, err := t3.New()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, _, err := m.findThread(project, branch); err == nil {
		return fmt.Errorf("a T3 thread for %s/%s already exists", project, branch)
	}

	// Always write the context file, unlike the tmux and herdr backends.
	//
	// Those launch the agent themselves and can pass a system prompt on the
	// command line, so they only write a file for agents that need one. T3 runs
	// the agent through its own provider registry — there is no argv to append
	// to — so a file is the only channel conductor has, whichever agent it is.
	if err := codingagent.WriteContextFile(worktreePath, t3AgentPrompt(project, branch)); err != nil {
		return fmt.Errorf("failed to write agent context file: %w", err)
	}
	_ = agent // The agent only selects wording; T3 decides what actually runs.

	// The project is rooted at the worktree, not the main repo, so each
	// worktree gets its own file tree, scripts and preview in the T3 UI.
	projectID, err := client.EnsureProject(ctx, m.WindowName(project, branch), worktreePath)
	if err != nil {
		return err
	}

	threadID, err := client.CreateThread(ctx, t3.CreateThreadOptions{
		ProjectID:    projectID,
		Title:        m.WindowName(project, branch),
		Branch:       branch,
		WorktreePath: worktreePath,
		Model:        client.ProjectModel(ctx, projectID),
		TaskPrompt:   taskPrompt,
	})
	if err != nil {
		return err
	}

	// Mark the worktree as T3-hosted so reconciliation can tell it apart from
	// worktrees created under tmux or herdr.
	if err := t3.WriteMarker(worktreePath, threadID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not mark %s as T3-hosted: %v\n", worktreePath, err)
	}

	// The dev server goes in tmux, not in a T3 terminal.
	//
	// A T3 terminal is spawned by the T3 server process and lives in its
	// cgroup, so restarting T3 kills every dev server with it. The tmux server
	// is independent and long-lived, so a dev server there survives T3
	// restarts, updates and crashes. One worktree gets one dev window.
	//
	// A failed dev server should not discard the thread the user is about to
	// work in, so this is reported as a warning rather than an error.
	if err := tmux.CreateDevWindow(project, branch, worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not start the dev server: %v\n", err)
	}
	return nil
}

// KillWindow archives the thread, removes the project conductor created for the
// worktree, and kills the tmux dev window.
//
// The dev window is killed first: leaving a dev server running against an
// archived worktree would hold its ports and keep writing to a tree that is
// about to be removed.
func (m t3Mux) KillWindow(project, branch string) error {
	worktreePath, err := config.WorktreePath(project, branch)
	if err != nil {
		return err
	}
	_ = tmux.KillWindow(project, branch)

	client, err := t3.New()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.CloseWorktree(ctx, worktreePath)
}

// FocusWindow prints the thread's URL. T3's UI is a web app conductor cannot
// raise from a terminal, so this reports where to look instead of pretending
// to have focused something.
func (m t3Mux) FocusWindow(project, branch string) error {
	client, threadID, err := m.findThread(project, branch)
	if err != nil {
		return fmt.Errorf("no T3 thread for %s/%s", project, branch)
	}
	fmt.Printf("%s/thread/%s\n", client.Origin, threadID)
	return nil
}

// KillOtherWindows is a no-op. On tmux this exists so the TUI can quit without
// orphaning panes, but T3 threads are server-side and are meant to outlive any
// client, so closing them all on exit would destroy work.
func (t3Mux) KillOtherWindows() {}

// StartAgentPane is unsupported: T3 runs agents as threads through its own
// provider registry rather than as processes conductor spawns, so there is no
// argv to run and no pane id to return.
func (t3Mux) StartAgentPane(windowName, workDir string, argv []string, paneTitle string) (string, error) {
	return "", &ErrUnsupported{Kind: KindT3, Op: "StartAgentPane"}
}

// PaneExists reports whether a thread id is still live. The dispatcher holds
// what StartAgentPane returned; since that is unsupported, this only ever sees
// ids from elsewhere and answers conservatively.
func (t3Mux) PaneExists(paneID string) bool {
	client, err := t3.New()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := client.Shell(ctx)
	if err != nil {
		return false
	}
	for _, thread := range snapshot.Threads {
		if thread.ID == paneID {
			return !thread.Archived()
		}
	}
	return false
}

// GetPaneCommand returns "" — T3 surfaces agent status through its own UI, and
// conductor's session tracker is disabled against this backend.
func (t3Mux) GetPaneCommand(paneID string) string { return "" }

// UpdateTabTitles is a no-op: T3 renders thread status itself.
func (t3Mux) UpdateTabTitles([]*session.Session) {}

// TracksAgentStatus reports true so conductor does not run its own session
// scanner against threads it cannot poll.
func (t3Mux) TracksAgentStatus() bool { return true }

// --- helpers ---

// findThread resolves a worktree window to its live T3 thread.
func (m t3Mux) findThread(project, branch string) (*t3.Client, string, error) {
	worktreePath, err := config.WorktreePath(project, branch)
	if err != nil {
		return nil, "", err
	}

	client, err := t3.New()
	if err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snapshot, err := client.Shell(ctx)
	if err != nil {
		return nil, "", err
	}
	thread, ok := snapshot.FindThreadByWorktree(worktreePath)
	if !ok {
		return nil, "", fmt.Errorf("no T3 thread bound to %s", worktreePath)
	}
	return client, thread.ID, nil
}

// t3AgentPrompt is the system prompt handed to coding agents that read a
// context file, so they know where the dev server lives.
func t3AgentPrompt(project, branch string) string {
	window := fmt.Sprintf("%s:%s/%s", tmux.SessionName, project, branch)
	return fmt.Sprintf(`## Conductor T3 Code Integration

This worktree is managed by conductor:
- You are the agent session in a T3 Code thread.
- The dev server runs in a tmux window, not in this thread. It lives in
  tmux window %q, and survives T3 Code restarts because tmux owns it.

### Reading the dev server logs
The dev server is already running. Do not start a second one.

  conductor t3 logs %s %s              # last 200 lines
  conductor t3 logs %s %s -n 1000      # more history
  conductor t3 logs %s %s -f           # follow

Those wrap tmux, which you can also drive directly:

  tmux capture-pane -p -S -200 -t %s   # read output
  tmux send-keys -t %s C-c             # stop the dev server; it reruns itself

Ports are allocated by conductor; run 'conductor status' to see them.`,
		window,
		project, branch,
		project, branch,
		project, branch,
		window, window)
}
