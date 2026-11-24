package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Event is one line of the action log (safety rule 5: every action is logged),
// ~/.local/state/conductor/lifecycle.log, JSONL.
type Event struct {
	Time   time.Time `json:"time"`
	Action Action    `json:"action"` // slim | needs-you-snapshot | retire | revive | hydrate
	Path   string    `json:"path"`
	// Result: ok | skipped | failed | dry-run.
	Result     string `json:"result"`
	Reason     string `json:"reason,omitempty"`
	Error      string `json:"error,omitempty"`
	RecordID   string `json:"recordId,omitempty"`
	SHA        string `json:"sha,omitempty"`
	FreedBytes int64  `json:"freedBytes,omitempty"`
	Forced     bool   `json:"forced,omitempty"`
}

// Extra actions that only the log and inventory use.
const (
	ActionRevive  Action = "revive"
	ActionHydrate Action = "hydrate"
)

// LogEvent appends one event and fsyncs. A log write that fails is returned so
// executors can refuse to act unlogged.
func (e *Env) LogEvent(ev Event) error {
	if ev.Time.IsZero() {
		ev.Time = e.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(e.LogPath()), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(e.LogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
