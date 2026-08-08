package mux

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/tmux"
)

func TestT3ResolvesByKind(t *testing.T) {
	assert.Equal(t, KindT3, Resolve(KindT3).Kind())
	assert.Equal(t, KindT3, ParseKind("t3"))
}

// Running inside T3 should select it, mirroring how HERDR_PANE_ID selects herdr.
func TestAutoPrefersT3WhenInsideIt(t *testing.T) {
	clearMuxEnv(t)
	t.Setenv("T3CODE_HOME", "/home/u/.t3")
	t.Setenv("HERDR_PANE_ID", "")
	assert.Equal(t, KindT3, auto().Kind())
}

// A herdr pane must still win when T3 is not in the environment.
func TestAutoPrefersHerdrWhenInsideIt(t *testing.T) {
	t.Setenv("T3CODE_HOME", "")
	t.Setenv("T3_THREAD_ID", "")
	t.Setenv("HERDR_PANE_ID", "pane-1")
	assert.Equal(t, KindHerdr, auto().Kind())
}

func TestT3WindowName(t *testing.T) {
	assert.Equal(t, "proj/branch", T3().WindowName("proj", "branch"))
}

func TestWindowNameFromWorktree(t *testing.T) {
	name, ok := windowNameFromWorktree("/home/u/.conductor", "/home/u/.conductor/kudtrading/sydney")
	require.True(t, ok)
	assert.Equal(t, "kudtrading/sydney", name)
}

// Threads bound to worktrees conductor did not create must not appear as
// conductor windows, or KillWindow would archive somebody else's work.
func TestWindowNameFromWorktreeRejectsOutsidePaths(t *testing.T) {
	_, ok := windowNameFromWorktree("/home/u/.conductor", "/home/u/Projects/elsewhere")
	assert.False(t, ok)
}

// A path directly under the conductor dir is a project, not a worktree.
func TestWindowNameFromWorktreeRejectsWrongDepth(t *testing.T) {
	_, ok := windowNameFromWorktree("/home/u/.conductor", "/home/u/.conductor/kudtrading")
	assert.False(t, ok)

	_, ok = windowNameFromWorktree("/home/u/.conductor", "/home/u/.conductor/a/b/c")
	assert.False(t, ok)
}

// StartAgentPane has no meaning against T3, and must say so rather than
// silently returning an empty pane id the dispatcher would then poll.
func TestT3StartAgentPaneUnsupported(t *testing.T) {
	_, err := T3().StartAgentPane("w", "/tmp", []string{"claude"}, "title")
	require.Error(t, err)

	var unsupported *ErrUnsupported
	require.ErrorAs(t, err, &unsupported)
	assert.Equal(t, KindT3, unsupported.Kind)
}

func TestT3TracksAgentStatus(t *testing.T) {
	assert.True(t, T3().TracksAgentStatus())
}

// TestT3Integration exercises the real server. It is skipped unless
// CONDUCTOR_T3_INTEGRATION=1, since it needs a running T3 and a valid token.
func TestT3Integration(t *testing.T) {
	if os.Getenv("CONDUCTOR_T3_INTEGRATION") != "1" {
		t.Skip("set CONDUCTOR_T3_INTEGRATION=1 to run against a live T3 Code server")
	}

	m := T3()
	require.NoError(t, m.CheckInstalled(), "server unreachable or token rejected")

	// Read-only: proves auth, origin discovery and snapshot decoding.
	names := m.ListWindowNames()
	t.Logf("live conductor windows in T3: %v", names)

	assert.False(t, m.WindowExists("definitely", "not-a-real-worktree"))
}

// TestT3CreateWindowIntegration drives the whole path against a live server:
// project, thread, dev-server terminal, then teardown. It creates a scratch
// worktree directory under the conductor dir and archives what it made.
func TestT3CreateWindowIntegration(t *testing.T) {
	if os.Getenv("CONDUCTOR_T3_INTEGRATION") != "1" {
		t.Skip("set CONDUCTOR_T3_INTEGRATION=1 to run against a live T3 Code server")
	}

	const project, branch = "t3probe", "scratch"

	worktreePath, err := config.WorktreePath(project, branch)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(worktreePath, 0755))

	m := T3()
	t.Cleanup(func() {
		_ = m.KillWindow(project, branch)
		base, err := config.WorktreeBasePath(project)
		if err == nil {
			_ = os.RemoveAll(base)
		}
	})

	require.NoError(t, m.CreateCodingWindow(project, branch, worktreePath, codingagent.ClaudeCode))

	assert.True(t, m.WindowExists(project, branch), "thread should be bound to the worktree")
	assert.Contains(t, m.ListWindowNames(), project+"/"+branch)

	// The dev server must live in tmux, not in a T3 terminal — that is what
	// makes it survive a T3 restart.
	assert.True(t, tmux.WindowExists(project, branch),
		"dev server should be hosted in a tmux window")

	// Creating the same window twice must fail rather than opening a duplicate
	// thread against one worktree.
	assert.Error(t, m.CreateCodingWindow(project, branch, worktreePath, codingagent.ClaudeCode))

	require.NoError(t, m.KillWindow(project, branch))
	assert.False(t, m.WindowExists(project, branch), "archived thread should not resolve")
	assert.False(t, tmux.WindowExists(project, branch),
		"dev window should be killed with the thread, or it holds ports open")
}
