package mux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// tuiWindowName is the window hosting the conductor TUI itself. It is recreated
// on restart, so it is never saved.
const tuiWindowName = "conductor"

// SessionState captures the open worktree windows so they can be verified after
// an auto-update restart.
type SessionState struct {
	Version string        `json:"version"`
	SavedAt time.Time     `json:"saved_at"`
	Mux     Kind          `json:"mux"`
	Windows []WindowState `json:"windows"`
}

// WindowState captures a single open window.
type WindowState struct {
	Name string `json:"name"` // e.g. "myproject/feature-x"
}

// stateFilePath returns the path to the session state file.
func stateFilePath() (string, error) {
	dir, err := config.ConductorDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session-state.json"), nil
}

// SaveSessionState captures m's current windows to disk.
func SaveSessionState(m Multiplexer, version string) error {
	state := SessionState{
		Version: version,
		SavedAt: time.Now(),
		Mux:     m.Kind(),
	}
	for _, name := range m.ListWindowNames() {
		if name == tuiWindowName {
			continue
		}
		state.Windows = append(state.Windows, WindowState{Name: name})
	}

	path, err := stateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadSessionState reads the saved session state, or nil if there is none.
func LoadSessionState() *SessionState {
	path, err := stateFilePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return &state
}

// ClearSessionState removes the session state file.
func ClearSessionState() {
	path, err := stateFilePath()
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

// VerifyWindows returns the saved windows that are still open in m.
func (s *SessionState) VerifyWindows(m Multiplexer) []string {
	if s == nil {
		return nil
	}
	existing := make(map[string]bool)
	for _, name := range m.ListWindowNames() {
		existing[name] = true
	}
	var alive []string
	for _, w := range s.Windows {
		if existing[w.Name] {
			alive = append(alive, w.Name)
		}
	}
	return alive
}
