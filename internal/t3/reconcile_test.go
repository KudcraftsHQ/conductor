package t3

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hostedWorktree(t *testing.T, threadID string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, WriteMarker(dir, threadID))
	return dir
}

func TestMarkerRoundTrip(t *testing.T) {
	dir := hostedWorktree(t, "thread-1")

	got, ok := ReadMarker(dir)
	require.True(t, ok)
	assert.Equal(t, "thread-1", got)
	assert.True(t, HasMarker(dir))

	RemoveMarker(dir)
	assert.False(t, HasMarker(dir))
}

func TestMarkerAbsent(t *testing.T) {
	_, ok := ReadMarker(t.TempDir())
	assert.False(t, ok)
}

// An empty marker is treated as absent rather than as a thread called "".
func TestMarkerEmptyFileIsAbsent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, MarkerFileName), []byte("  \n"), 0644))
	assert.False(t, HasMarker(dir))
}

// The regression that matters: worktrees created under tmux or herdr carry no
// marker, so they must never be reported as drift. Without this filter every
// legacy worktree looks archived and `--archive` would destroy all of them.
func TestReconcileIgnoresUnmarkedWorktrees(t *testing.T) {
	legacy := Drift{Project: "old", Worktree: "one", WorktreePath: t.TempDir()}

	client := &Client{}
	drifted, err := client.Reconcile(t.Context(), []Drift{legacy})
	require.NoError(t, err)
	assert.Empty(t, drifted, "an unmarked worktree is not T3-hosted and cannot have drifted")
}

func TestCountHosted(t *testing.T) {
	worktrees := []Drift{
		{WorktreePath: hostedWorktree(t, "a")},
		{WorktreePath: t.TempDir()},
		{WorktreePath: hostedWorktree(t, "b")},
	}
	assert.Equal(t, 2, CountHosted(worktrees))
}

func TestNormalizePath(t *testing.T) {
	assert.Equal(t, normalizePath("/a/b/"), normalizePath("/a/b"))
	assert.Equal(t, normalizePath("/a/./b"), normalizePath("/a/b"))
}
