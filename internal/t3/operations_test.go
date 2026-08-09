package t3

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strptr(s string) *string { return &s }

func TestFindThreadByWorktree(t *testing.T) {
	snapshot := &ShellSnapshot{Threads: []Thread{
		{ID: "a", WorktreePath: strptr("/home/u/.conductor/proj/one")},
		{ID: "b", WorktreePath: strptr("/home/u/.conductor/proj/two")},
	}}

	found, ok := snapshot.FindThreadByWorktree("/home/u/.conductor/proj/two")
	require.True(t, ok)
	assert.Equal(t, "b", found.ID)

	_, ok = snapshot.FindThreadByWorktree("/home/u/.conductor/proj/three")
	assert.False(t, ok)
}

// An archived thread is a closed window: it must not be resolved, or conductor
// would refuse to recreate a worktree the user has already finished with.
func TestFindThreadByWorktreeSkipsArchived(t *testing.T) {
	snapshot := &ShellSnapshot{Threads: []Thread{
		{ID: "a", WorktreePath: strptr("/w/one"), ArchivedAt: strptr("2026-01-01T00:00:00Z")},
	}}
	_, ok := snapshot.FindThreadByWorktree("/w/one")
	assert.False(t, ok)
}

func TestFindThreadByWorktreeIgnoresTrailingSlash(t *testing.T) {
	snapshot := &ShellSnapshot{Threads: []Thread{
		{ID: "a", WorktreePath: strptr("/w/one/")},
	}}
	found, ok := snapshot.FindThreadByWorktree("/w/one")
	require.True(t, ok)
	assert.Equal(t, "a", found.ID)
}

// Threads with no worktree are ordinary chats, not conductor windows.
func TestThreadsWithoutWorktreesAreIgnored(t *testing.T) {
	snapshot := &ShellSnapshot{Threads: []Thread{
		{ID: "a"},
		{ID: "b", WorktreePath: strptr("/w/one")},
	}}
	assert.Len(t, snapshot.LiveThreadsWithWorktrees(), 1)

	_, ok := snapshot.FindThreadByWorktree("")
	assert.False(t, ok)
}

func TestFindProjectByRoot(t *testing.T) {
	snapshot := &ShellSnapshot{Projects: []Project{
		{ID: "p1", WorkspaceRoot: "/repo/one"},
		{ID: "p2", WorkspaceRoot: "/repo/two", DeletedAt: strptr("2026-01-01T00:00:00Z")},
	}}

	found, ok := snapshot.FindProjectByRoot("/repo/one")
	require.True(t, ok)
	assert.Equal(t, "p1", found.ID)

	// A deleted project must not be reused, or thread.create targets a tombstone.
	_, ok = snapshot.FindProjectByRoot("/repo/two")
	assert.False(t, ok)
}

// branch and worktreePath are NullOr on the server, not optional: omitting them
// is a decode error, so they must serialize as explicit nulls when empty.
func TestThreadCreateEncodesNullsExplicitly(t *testing.T) {
	encoded, err := json.Marshal(ThreadCreateCommand{
		Type:         "thread.create",
		Branch:       ptr(""),
		WorktreePath: ptr(""),
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	branch, present := decoded["branch"]
	require.True(t, present, "branch must be present even when empty")
	assert.Nil(t, branch)

	worktree, present := decoded["worktreePath"]
	require.True(t, present, "worktreePath must be present even when empty")
	assert.Nil(t, worktree)
}

func TestThreadCreateEncodesValues(t *testing.T) {
	encoded, err := json.Marshal(ThreadCreateCommand{
		Type:         "thread.create",
		Branch:       ptr("feature-x"),
		WorktreePath: ptr("/w/feature-x"),
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, "feature-x", decoded["branch"])
	assert.Equal(t, "/w/feature-x", decoded["worktreePath"])
}

func TestThreadArchivedReportsDeleted(t *testing.T) {
	assert.True(t, Thread{DeletedAt: strptr("x")}.Archived())
	assert.True(t, Thread{ArchivedAt: strptr("x")}.Archived())
	assert.False(t, Thread{}.Archived())
}
