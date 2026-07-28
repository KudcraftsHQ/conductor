package session

import (
	"sort"
	"sync"
	"time"
)

// ScanInterval is how often the tracker scans for session changes
const ScanInterval = 3 * time.Second

// Tracker manages all known agent sessions and their status
type Tracker struct {
	mu          sync.RWMutex
	sessions    []*Session
	tmuxSession string // The tmux session to scan (e.g., "conductor")

	// Callback for notifying the TUI of changes
	onChange func(sessions []*Session)

	// Optional side-effect hook for updating tmux window names with status
	// icons. Set via SetTitleUpdater. Called with the latest snapshot on
	// every meaningful change.
	titleUpdater func(sessions []*Session)

	stopCh chan struct{}
}

// SetTitleUpdater installs a function that will be called after every scan
// with the latest sessions (used by the tmux package to rewrite window names
// to include status icons).
func (t *Tracker) SetTitleUpdater(fn func(sessions []*Session)) {
	t.titleUpdater = fn
}

// NewTracker creates a new session tracker
func NewTracker(tmuxSession string, onChange func(sessions []*Session)) *Tracker {
	return &Tracker{
		tmuxSession: tmuxSession,
		onChange:    onChange,
		stopCh:      make(chan struct{}),
	}
}

// Start begins the scan loop in a goroutine
func (t *Tracker) Start() {
	go t.scanLoop()
}

// Stop stops the scan loop
func (t *Tracker) Stop() {
	close(t.stopCh)
}

// Sessions returns a snapshot of current sessions
func (t *Tracker) Sessions() []*Session {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*Session, len(t.sessions))
	copy(out, t.sessions)
	return out
}

// scanLoop runs the full scan at regular intervals
func (t *Tracker) scanLoop() {
	// Run immediately on start
	t.scan()

	ticker := time.NewTicker(ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.scan()
		}
	}
}

// scan performs a full pane scan + status update cycle
func (t *Tracker) scan() {
	panes := ScanPanes(t.tmuxSession)
	now := time.Now()

	t.mu.Lock()

	// Build a map of existing sessions by pane ID for fast lookup
	existing := make(map[string]*Session)
	for _, s := range t.sessions {
		existing[s.PaneID] = s
	}

	var updated []*Session

	for _, pane := range panes {
		agent, isAgent := DetectAgent(pane)
		if !isAgent {
			continue
		}

		s, exists := existing[pane.PaneID]
		if !exists {
			// New session discovered
			dir := GetWorkingDir(pane.PaneID)
			branch := GetGitBranch(dir)
			jsonlPath := ""
			if agent == AgentClaudeCode {
				jsonlPath = FindJSONLPath(dir)
			}

			s = &Session{
				Name:       extractSessionName(pane.WindowName),
				Agent:      agent,
				WindowName: pane.WindowName,
				PaneID:     pane.PaneID,
				PanePID:    pane.PanePID,
				Dir:        dir,
				Branch:     branch,
				Status:     StatusIdle,
				Alive:      true,
				JSONLPath:  jsonlPath,
				UpdatedAt:  now,
			}
		} else {
			// Existing session — update liveness
			s.Alive = true
		}

		// Update status from JSONL (Claude Code)
		if s.Agent == AgentClaudeCode && s.JSONLPath != "" {
			newStatus, newSize := ReadJSONLStatus(s.JSONLPath, s.LastFileSize)
			if newStatus != "" {
				if newSize > s.LastFileSize {
					s.LastGrowthAt = now
				}
				if newStatus == StatusToolRunning {
					s.ToolUseSeenAt = now
				}
				s.Status = newStatus
				s.LastFileSize = newSize
				s.UpdatedAt = now
			} else {
				// No new data — check for time-based status inference
				s.Status = InferTimeBasedStatus(s, now)
			}
		}

		// Retry JSONL path discovery if still empty (session may have started after first scan)
		if s.Agent == AgentClaudeCode && s.JSONLPath == "" && s.Dir != "" {
			s.JSONLPath = FindJSONLPath(s.Dir)
		}

		updated = append(updated, s)
	}

	// Sort by window name for stable ordering
	sort.Slice(updated, func(i, j int) bool {
		return updated[i].WindowName < updated[j].WindowName
	})

	changed := t.hasChanged(updated)
	t.sessions = updated
	t.mu.Unlock()

	if changed {
		snapshot := make([]*Session, len(updated))
		copy(snapshot, updated)

		if t.titleUpdater != nil {
			t.titleUpdater(snapshot)
		}
		if t.onChange != nil {
			t.onChange(snapshot)
		}
	}
}

// hasChanged checks if the session list has meaningfully changed
func (t *Tracker) hasChanged(newSessions []*Session) bool {
	if len(t.sessions) != len(newSessions) {
		return true
	}
	for i, s := range t.sessions {
		n := newSessions[i]
		if s.PaneID != n.PaneID || s.Status != n.Status || s.Alive != n.Alive || s.WindowName != n.WindowName {
			return true
		}
	}
	return false
}

// extractSessionName extracts a display name from a tmux window name
// e.g., "myproject/feature-login" → "feature-login"
func extractSessionName(windowName string) string {
	// Use the part after the last "/"
	parts := splitLast(windowName, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return windowName
}

func splitLast(s, sep string) []string {
	idx := lastIndex(s, sep)
	if idx < 0 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+len(sep):]}
}

func lastIndex(s, substr string) int {
	for i := len(s) - len(substr); i >= 0; i-- {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
