package mux

import (
	"testing"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMux is a Multiplexer whose window list is controlled by the test.
type fakeMux struct {
	Multiplexer
	windows []string
}

func (f *fakeMux) Kind() Kind                { return KindTmux }
func (f *fakeMux) ListWindowNames() []string { return f.windows }

// Unused interface methods, present so fakeMux satisfies Multiplexer without
// embedding a nil that could be called by accident.
func (f *fakeMux) CheckInstalled() error              { return nil }
func (f *fakeMux) InstallGuide() string               { return "" }
func (f *fakeMux) IsInsideSession() bool              { return false }
func (f *fakeMux) IsInsideConductorSession() bool     { return false }
func (f *fakeMux) SessionName() string                { return "conductor" }
func (f *fakeMux) StartSession() error                { return nil }
func (f *fakeMux) DetachSession() error               { return nil }
func (f *fakeMux) WindowName(p, b string) string      { return p + "/" + b }
func (f *fakeMux) WindowExists(p, b string) bool      { return false }
func (f *fakeMux) KillWindow(p, b string) error       { return nil }
func (f *fakeMux) FocusWindow(p, b string) error      { return nil }
func (f *fakeMux) KillOtherWindows()                  {}
func (f *fakeMux) PaneExists(string) bool             { return false }
func (f *fakeMux) GetPaneCommand(string) string       { return "" }
func (f *fakeMux) UpdateTabTitles([]*session.Session) {}
func (f *fakeMux) TracksAgentStatus() bool            { return false }

func (f *fakeMux) CreateCodingWindow(p, b, w string, a codingagent.Agent) error { return nil }
func (f *fakeMux) CreateCodingWindowWithTask(p, b, w, t string, a codingagent.Agent) error {
	return nil
}
func (f *fakeMux) StartAgentPane(w, d string, argv []string, title string) (string, error) {
	return "", nil
}

func TestSessionStateSaveLoadClear(t *testing.T) {
	t.Setenv("CONDUCTOR_CONFIG_DIR", t.TempDir())

	m := &fakeMux{windows: []string{"conductor", "proj/alpha", "proj/beta"}}
	require.NoError(t, SaveSessionState(m, "1.2.3.4"))

	got := LoadSessionState()
	require.NotNil(t, got)
	assert.Equal(t, "1.2.3.4", got.Version)
	assert.Equal(t, KindTmux, got.Mux)

	// The TUI's own window is excluded — it gets recreated on restart.
	names := make([]string, 0, len(got.Windows))
	for _, w := range got.Windows {
		names = append(names, w.Name)
	}
	assert.Equal(t, []string{"proj/alpha", "proj/beta"}, names)

	ClearSessionState()
	assert.Nil(t, LoadSessionState())
}

func TestLoadSessionStateMissingFile(t *testing.T) {
	t.Setenv("CONDUCTOR_CONFIG_DIR", t.TempDir())
	assert.Nil(t, LoadSessionState())
}

func TestVerifyWindows(t *testing.T) {
	state := &SessionState{Windows: []WindowState{
		{Name: "proj/alpha"},
		{Name: "proj/beta"},
		{Name: "proj/gone"},
	}}

	m := &fakeMux{windows: []string{"conductor", "proj/alpha", "proj/beta"}}
	assert.Equal(t, []string{"proj/alpha", "proj/beta"}, state.VerifyWindows(m))
}

func TestVerifyWindowsNoneAlive(t *testing.T) {
	state := &SessionState{Windows: []WindowState{{Name: "proj/alpha"}}}
	assert.Empty(t, state.VerifyWindows(&fakeMux{}))
}

func TestVerifyWindowsNilState(t *testing.T) {
	var state *SessionState
	assert.Nil(t, state.VerifyWindows(&fakeMux{windows: []string{"proj/alpha"}}))
}
