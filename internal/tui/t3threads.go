package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/hammashamzah/conductor/internal/t3watch"
)

// fetchT3Threads asks T3 Code how many threads each worktree carries.
//
// This is the number the whole lifecycle turns on — a worktree lives while a
// thread is bound to it, hibernates when only archived ones remain and is torn
// down when the last is deleted — so it belongs on screen rather than being
// inferred from ports that happen to be missing.
func fetchT3Threads() tea.Cmd {
	return func() tea.Msg {
		client, err := t3.New()
		if err != nil {
			// Not a T3 setup, or no token. The column simply stays empty.
			return T3ThreadsFetchedMsg{Counts: map[string]t3watch.Counts{}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		snapshot, err := client.Snapshot(ctx)
		if err != nil {
			return T3ThreadsFetchedMsg{Counts: map[string]t3watch.Counts{}}
		}
		return T3ThreadsFetchedMsg{Counts: t3watch.CountThreads(snapshot)}
	}
}

// threadSummary renders a worktree's thread counts, e.g. "2L·1A".
//
// A hosted worktree with no threads at all is shown as "0" rather than blank,
// because that is not nothing: it is a worktree the watcher is about to remove.
func (m *Model) threadSummary(name string, wt *config.Worktree) string {
	if wt == nil || wt.Path == "" || wt.Archived {
		return ""
	}
	counts, known := m.t3ThreadCounts[t3watch.NormalizePath(wt.Path)]
	if !known {
		if !wt.IsT3Hosted() {
			return "" // Made under tmux or herdr; threads are not its measure.
		}
		return "0"
	}
	if counts.Archived == 0 {
		return fmt.Sprintf("%dL", counts.Live)
	}
	return fmt.Sprintf("%dL·%dA", counts.Live, counts.Archived)
}
