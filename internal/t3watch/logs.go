package t3watch

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hammashamzah/conductor/internal/t3"
)

// LogTerminalID is the terminal conductor opens on a thread to show the shared
// dev server's output.
//
// The id is fixed rather than generated so that re-running this is harmless:
// opening a terminal that already exists returns the existing one instead of
// stacking up duplicates every time a worktree wakes.
const LogTerminalID = "conductor-dev-logs"

// OpenLogTerminal gives one thread on a worktree a view of the dev server.
//
// The server itself runs in one tmux window shared by the whole worktree,
// because a T3 terminal belongs to a single thread — sessions are keyed by
// (threadId, terminalId) and the UI only renders the active thread's — so a
// second thread running the dev script would collide on the port rather than
// share the server. What the thread gets instead is a read-only follow of the
// shared window's output, in its own terminal tab.
//
// Only one thread gets it. Opening it on every thread cost a shell and a
// `logs -f` per thread, and a worktree with dozens of subagent threads ended
// up with dozens of terminals that never closed, since a running follow keeps
// a terminal busy. The terminal is opened with stopOnSettle so T3 closes it
// when the thread settles, and it is left alone when it is already following.
//
// Nothing is opened for a worktree whose directory is gone: T3 recreates a
// missing worktree when a terminal is opened on one of its threads, which
// brought deleted worktrees back to life.
//
// Failures are reported and swallowed. A missing log view is a comfort lost,
// not a reason to fail a wake.
func OpenLogTerminal(client *t3.Client, project, branch, worktreePath, threadID string) {
	if client == nil || threadID == "" || worktreePath == "" {
		return
	}
	if !isDir(worktreePath) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if running, err := logTerminalRunning(ctx, client, threadID); err == nil && running {
		return
	} else if err != nil {
		// Without the terminal list there is no telling whether a follow is
		// already running in it, so close it rather than stack a second one.
		if conn, dialErr := client.Dial(ctx); dialErr == nil {
			_ = conn.TerminalClose(ctx, threadID, LogTerminalID)
			_ = conn.Close()
		}
	}

	conn, err := client.Dial(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open the dev log view: %v\n", err)
		return
	}
	defer func() { _ = conn.Close() }()

	command := fmt.Sprintf("conductor t3 logs %s %s -f", project, branch)
	if _, err := conn.TerminalRunCommand(ctx, threadID, LogTerminalID, worktreePath, worktreePath, command,
		t3.TerminalOpenOptions{StopOnSettle: true}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open the dev log view on thread %s: %v\n", threadID, err)
	}
}

// logTerminalRunning reports whether threadID's log terminal exists and is
// busy, which for this terminal means the follow is already running in it.
//
// The metadata stream is read on its own connection: a subscription keeps
// sending frames, and the connection it was read on is closed rather than
// reused for the calls that follow.
func logTerminalRunning(ctx context.Context, client *t3.Client, threadID string) (bool, error) {
	conn, err := client.Dial(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()

	terminals, err := conn.TerminalList(ctx)
	if err != nil {
		return false, err
	}
	for _, terminal := range terminals {
		if terminal.ThreadID == threadID && terminal.TerminalID == LogTerminalID {
			return terminal.Running(), nil
		}
	}
	return false, nil
}
