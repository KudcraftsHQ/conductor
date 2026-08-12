package mux

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/ready"
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

	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return nil
	}

	var names []string
	seen := make(map[string]bool)
	for _, thread := range snapshot.LiveThreadsWithWorktrees() {
		name, ok := windowNameFromWorktree(cfg, thread.Worktree())
		if !ok || seen[name] {
			// Several threads sharing one worktree are one window, not several.
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// windowNameFromWorktree turns a worktree path back into "project/branch".
//
// It used to require the path to sit under ~/.conductor, which was true while
// conductor created every worktree itself. T3 Code places the ones it creates
// under its own root, so the registered entry — which records where a worktree
// actually is — is the only reliable way back. A path conductor does not know
// about belongs to somebody else's thread and is skipped.
func windowNameFromWorktree(cfg *config.Config, worktreePath string) (string, bool) {
	if worktreePath == "" {
		return "", false
	}
	projectName, project, worktree, err := cfg.DetectProject(worktreePath)
	if err != nil || project == nil || worktree == nil || worktree.IsRoot {
		return "", false
	}
	return projectName + "/" + worktree.Branch, true
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

	if existing, err := existingThread(ctx, client, project, branch, worktreePath); err != nil {
		return err
	} else if existing != "" {
		return fmt.Errorf("a T3 thread for %s/%s already exists (%s)", project, branch, existing)
	}

	// Always write the context file, unlike the tmux and herdr backends.
	//
	// Those launch the agent themselves and can pass a system prompt on the
	// command line, so they only write a file for agents that need one. T3 runs
	// the agent through its own provider registry — there is no argv to append
	// to — so a file is the only channel conductor has, whichever agent it is.
	if err := codingagent.WriteContextFile(worktreePath, T3AgentPrompt(project, branch)); err != nil {
		return fmt.Errorf("failed to write agent context file: %w", err)
	}
	_ = agent // The agent only selects wording; T3 decides what actually runs.

	// The project is rooted at the worktree, not the main repo, so each
	// worktree gets its own file tree, scripts and preview in the T3 UI.
	projectID, err := client.EnsureProject(ctx, m.WindowName(project, branch), worktreePath)
	if err != nil {
		return err
	}

	// The worktree's own project is brand new and has no default model, so the
	// main repository's project is the better hint: it carries whatever the user
	// picked for this codebase. Both are only hints — ResolveModelSelection
	// validates them against the running build's provider registry, which
	// matters because a project default can name a disabled instance.
	model, err := client.ResolveModelSelection(ctx,
		client.ProjectDefaultModels(ctx, projectID, mainRepoProjectID(ctx, client, project))...)
	if err != nil {
		return fmt.Errorf("cannot open a T3 thread for %s/%s: %w", project, branch, err)
	}

	threadID, err := client.CreateThread(ctx, t3.CreateThreadOptions{
		ProjectID:    projectID,
		Title:        m.WindowName(project, branch),
		Branch:       branch,
		WorktreePath: worktreePath,
		Model:        model,
		TaskPrompt:   taskPrompt,
		ReadyGate:    readyGate(worktreePath),
	})
	if err != nil {
		// A thread whose turn never started is reported, not swallowed: the
		// marker still gets written below so the thread is not orphaned.
		if threadID != "" {
			if markErr := t3.WriteMarker(worktreePath, []string{threadID}); markErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not mark %s as T3-hosted: %v\n", worktreePath, markErr)
			}
		}
		return err
	}

	// Mark the worktree as T3-hosted so reconciliation can tell it apart from
	// worktrees created under tmux or herdr.
	if err := t3.WriteMarker(worktreePath, []string{threadID}); err != nil {
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
	worktreePath, err := config.ResolveWorktreePath(project, branch)
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

// existingThread returns the id of a thread already hosting the worktree, or ""
// when the worktree is free for a new one.
//
// Two sources, because neither alone is sufficient:
//
//   - The snapshot is authoritative about state but lags a create, so a second
//     call arriving immediately after the first sees nothing and would produce a
//     duplicate thread on one worktree.
//   - The marker is written synchronously, so it closes that window — but it
//     deliberately records archived thread ids too, because an archived thread
//     still holds a worktree open and reconciliation needs to know. So a marker
//     id is not by itself proof that a live thread exists.
//
// Reading the marker as "occupied" is therefore wrong, and was: archiving a
// worktree's thread in T3's UI left a marker no code cleared, and every later
// create refused with "already exists" naming a thread that was gone. The only
// way out was deleting the marker file by hand. A marker id blocks only while
// the snapshot either shows it live or has not caught up to it yet.
// The full snapshot is required, not the lighter Shell one: Shell omits
// archived threads, so a marker id belonging to an archived thread would come
// back "unknown" and be mistaken for a create still in flight — which is
// precisely the reading that made this refuse forever.
func existingThread(ctx context.Context, client *t3.Client, project, branch, worktreePath string) (string, error) {
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	ids, _ := t3.ReadMarker(worktreePath)
	occupant, stale := threadOccupying(snapshot, worktreePath, ids)
	if stale {
		// Every recorded thread is archived, so the worktree is free. Clear the
		// stale marker rather than leaving the next call trip over it again.
		// The watcher may write those ids back — it records archived threads on
		// purpose — which is why the check above does not trust the marker alone.
		t3.RemoveMarker(worktreePath)
	}
	return occupant, nil
}

// threadOccupying reports the thread still holding a worktree, and whether the
// marker is stale (recorded threads that are all archived).
func threadOccupying(snapshot *t3.ShellSnapshot, worktreePath string, markerIDs []string) (occupant string, stale bool) {
	if thread, ok := snapshot.FindThreadByWorktree(worktreePath); ok {
		return thread.ID, false
	}
	if len(markerIDs) == 0 {
		return "", false
	}
	for _, id := range markerIDs {
		thread, known := snapshot.FindThreadByID(id)
		if !known {
			// Recorded locally but absent from the full snapshot: the create has
			// not been applied yet. This is the race the marker exists for.
			return id, false
		}
		if !thread.Archived() {
			// A live thread whose worktree binding differs from this path; still
			// occupied.
			return id, false
		}
	}
	return "", true
}

// mainRepoProjectID returns the id of the T3 project rooted at the project's
// main repository, or "" when T3 has no project there.
//
// This is where the user's model choice for a codebase lives. A worktree's own
// project is created by conductor moments earlier and has no default, so
// without this every conductor thread would fall back to a generic guess.
func mainRepoProjectID(ctx context.Context, client *t3.Client, project string) string {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return ""
	}
	entry, ok := cfg.GetProject(project)
	if !ok || entry == nil || entry.Path == "" {
		return ""
	}
	snapshot, err := client.Shell(ctx)
	if err != nil {
		return ""
	}
	if found, ok := snapshot.FindProjectByRoot(entry.Path); ok {
		return found.ID
	}
	return ""
}

// findThread resolves a worktree window to its live T3 thread.
func (m t3Mux) findThread(project, branch string) (*t3.Client, string, error) {
	worktreePath, err := config.ResolveWorktreePath(project, branch)
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

// readyGate returns the function CreateThread calls before submitting a
// thread's first turn.
//
// Everything conductor dispatches goes through here: the TUI, `conductor
// build`, the ClickUp dispatcher and `conductor t3 create --prompt`. On those
// paths the worktree may have been created seconds earlier, with its setup
// still running in the background queue — so the turn waits rather than the
// agent being asked to.
//
// The one path this cannot cover is a thread started in T3's own composer,
// where T3 launches the provisioning hook and opens the first turn itself and
// conductor is a bystander. That path still relies on the context file telling
// the agent to run `conductor wait`.
func readyGate(worktreePath string) func(context.Context) error {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, readyGateTimeout)
		defer cancel()

		announced := false
		status, err := ready.Wait(ctx, worktreePath, ready.DefaultPhases, 2*time.Second,
			func(status ready.Status) {
				// Silent when it is already ready, which is the common case;
				// worth a line the moment it is not, because the thread appears
				// to hang otherwise.
				if status.State.Done() && !announced {
					return
				}
				announced = true
				fmt.Fprintf(os.Stderr, "waiting for %s: %s\n", worktreePath, status)
			})
		if err != nil {
			return fmt.Errorf("gave up after %s: %s", readyGateTimeout, status)
		}
		if !status.State.Done() {
			return fmt.Errorf("%s", status)
		}
		if announced {
			fmt.Fprintf(os.Stderr, "%s is ready\n", worktreePath)
		}
		return nil
	}
}

// readyGateTimeout bounds the hold on a first turn. A remote database clone is
// minutes; beyond this something is wrong and a late turn beats no turn.
const readyGateTimeout = 20 * time.Minute

// T3AgentPrompt is the system prompt handed to coding agents that read a
// context file.
//
// It is conductor's only *standing* channel to an agent T3 is running: there is
// no argv to append to, and thread.activity.append is internal to T3 and
// rejected from outside. Turns are a channel — thread.turn.start works, and
// CreateThread uses it for the task prompt — but a turn is a message in the
// conversation, not environment that applies to every later turn, which is what
// this is for. Everything an agent could get wrong about this environment —
// starting a second dev server, hardcoding a port that changes on every wake,
// working against a database that is still being cloned — has to be said here.
func T3AgentPrompt(project, branch string) string {
	window := fmt.Sprintf("%s:%s/%s", tmux.SessionName, project, branch)
	return fmt.Sprintf(`## Conductor T3 Code Integration

This worktree is managed by conductor. T3 Code owns its lifecycle; conductor
owns its ports, database, tunnel and dev server.

### Wait for provisioning before touching data
The environment is built after this worktree appears, not before — the dev
database is a full clone and takes minutes — and T3 may start your first turn
while that is still running. Before any migration, seed, query or test:

  conductor wait

It blocks until the setup script has finished and the database answers, then
exits 0. Exit 1 means setup failed and prints the tail of its log; 2 means it
gave up waiting. Add --for all if you also need the dev server listening.
Do not poll for a file — readiness is state, not a marker.

### Do not start a dev server
One is already running, in tmux window %q, shared by every thread bound to this
worktree. It survives T3 Code restarts because tmux owns it. Starting a second
one collides on the port.

  conductor t3 logs -f                 # follow it (inferred from the cwd)
  conductor t3 logs -n 1000            # more history
  conductor t3 logs %s %s              # explicit

Those wrap tmux, which you can also drive directly:

  tmux capture-pane -p -S -200 -t %s   # read output
  tmux send-keys -t %s C-c             # stop the dev server; it reruns itself

### Never hardcode ports or database names
Read them from the environment — CONDUCTOR_PORT, CONDUCTOR_PORTS,
CONDUCTOR_PORT_<LABEL> — or from the .env the setup script writes. Archiving
every thread on this worktree releases its ports and drops its database;
unarchiving one rebuilds both, and the new ports are not the old ones. The
working tree and branch are never touched by that, so your uncommitted work
survives it.

Run 'conductor status' to see the current allocation.`,
		window,
		project, branch,
		window, window)
}
