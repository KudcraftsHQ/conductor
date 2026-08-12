package mux

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hammashamzah/conductor/internal/t3"
)

func strptr(s string) *string { return &s }

const guardWorktree = "/home/u/.conductor/kudtrading/athens-2"

// A live thread on the worktree occupies it, and is the id worth reporting —
// the guard used to name an arbitrary marker entry instead, which could be a
// thread that no longer existed.
func TestThreadOccupyingReportsLiveThread(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{
		{ID: "live", WorktreePath: strptr(guardWorktree)},
	}}
	occupant, stale := threadOccupying(snapshot, guardWorktree, []string{"archived-1", "live"})
	assert.Equal(t, "live", occupant)
	assert.False(t, stale)
}

// The race the marker exists for: conductor wrote the id, T3 has not applied
// the create, so the snapshot does not know it yet. Creating again here would
// put two threads on one worktree.
func TestThreadOccupyingBlocksOnUnappliedCreate(t *testing.T) {
	snapshot := &t3.ShellSnapshot{}
	occupant, stale := threadOccupying(snapshot, guardWorktree, []string{"just-created"})
	assert.Equal(t, "just-created", occupant)
	assert.False(t, stale)
}

// The bug: the marker records archived threads on purpose (an archived thread
// still holds a worktree open, and the watcher rewrites them back), so reading
// any marker entry as "occupied" meant a worktree whose thread was archived in
// T3's UI could never get another one. The only recovery was deleting the
// marker file by hand.
func TestThreadOccupyingAllowsWorktreeWhoseThreadsAreAllArchived(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{
		{ID: "gone-1", WorktreePath: strptr(guardWorktree), ArchivedAt: strptr("2026-08-11T04:57:39Z")},
		{ID: "gone-2", WorktreePath: strptr(guardWorktree), DeletedAt: strptr("2026-08-11T04:57:39Z")},
	}}
	occupant, stale := threadOccupying(snapshot, guardWorktree, []string{"gone-1", "gone-2"})
	assert.Empty(t, occupant)
	// Stale, so the caller clears the marker instead of tripping over it again.
	assert.True(t, stale)
}

// A worktree T3 has never hosted is free, and has no marker to clear.
func TestThreadOccupyingAllowsUnmarkedWorktree(t *testing.T) {
	occupant, stale := threadOccupying(&t3.ShellSnapshot{}, guardWorktree, nil)
	assert.Empty(t, occupant)
	assert.False(t, stale)
}

// One live entry among archived ones still occupies the worktree.
func TestThreadOccupyingBlocksOnLiveMarkerEntryWithoutWorktreeBinding(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{
		{ID: "gone", ArchivedAt: strptr("2026-08-11T04:57:39Z")},
		{ID: "still-running"},
	}}
	occupant, stale := threadOccupying(snapshot, guardWorktree, []string{"gone", "still-running"})
	assert.Equal(t, "still-running", occupant)
	assert.False(t, stale)
}
