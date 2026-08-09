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

// OpenLogTerminals gives every thread on a worktree a view of the dev server.
//
// The server itself runs in one tmux window shared by the whole worktree,
// because a T3 terminal belongs to a single thread — sessions are keyed by
// (threadId, terminalId) and the UI only renders the active thread's — so a
// second thread running the dev script would collide on the port rather than
// share the server. What each thread gets instead is this: a read-only follow
// of the shared window's output, in its own terminal tab.
//
// Failures are reported and swallowed. A missing log view is a comfort lost,
// not a reason to fail a wake.
func OpenLogTerminals(client *t3.Client, project, branch, worktreePath string, threadIDs []string) {
	if client == nil || len(threadIDs) == 0 || worktreePath == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := client.Dial(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open the dev log view: %v\n", err)
		return
	}
	defer func() { _ = conn.Close() }()

	command := fmt.Sprintf("conductor t3 logs %s %s -f", project, branch)
	for _, threadID := range threadIDs {
		if _, err := conn.TerminalRunCommand(ctx, threadID, LogTerminalID, worktreePath, worktreePath, command); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open the dev log view on thread %s: %v\n", threadID, err)
		}
	}
}
