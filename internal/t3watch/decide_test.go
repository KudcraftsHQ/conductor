package t3watch

import (
	"testing"
	"time"

	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wtPath = "/home/u/.t3/worktrees/kudtrading/feat-x"

func thread(id, path string, archivedAt, deletedAt *string) t3.Thread {
	worktree := path
	return t3.Thread{
		ID:           id,
		WorktreePath: &worktree,
		ArchivedAt:   archivedAt,
		DeletedAt:    deletedAt,
	}
}

func at(s string) *string { return &s }

func hosted(threads ...string) Worktree {
	return Worktree{
		Project:      "kudtrading",
		Name:         "sydney",
		Path:         wtPath,
		KnownThreads: threads,
	}
}

func decider(now time.Time) *Decider {
	d := NewDecider(10*time.Minute, 3)
	d.Now = func() time.Time { return now }
	return d
}

func only(t *testing.T, decisions []Decision) Decision {
	t.Helper()
	require.Len(t, decisions, 1)
	return decisions[0]
}

func TestLiveThreadLeavesWorktreeAlone(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", wtPath, nil, nil)}}

	decision := only(t, decider(time.Now()).Decide([]Worktree{hosted("a")}, snapshot))

	assert.Equal(t, ActionNone, decision.Action)
	assert.Equal(t, 1, decision.Live)
}

// Several threads on one worktree is the normal case once T3 starts reusing
// worktrees, and archiving one of them must change nothing.
func TestArchivingOneOfSeveralThreadsChangesNothing(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{
		thread("a", wtPath, nil, nil),
		thread("b", wtPath, at("2026-08-09T00:00:00Z"), nil),
	}}

	decision := only(t, decider(time.Now()).Decide([]Worktree{hosted("a", "b")}, snapshot))

	assert.Equal(t, ActionNone, decision.Action)
	assert.Equal(t, 1, decision.Live)
	assert.Equal(t, 1, decision.Archive)
}

// Hibernation waits out the debounce, so archiving by misclick costs a click to
// undo rather than a database clone.
func TestHibernateOnlyAfterDebounce(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{
		thread("a", wtPath, at("2026-08-09T00:00:00Z"), nil),
	}}

	start := time.Now()
	d := decider(start)

	first := only(t, d.Decide([]Worktree{hosted("a")}, snapshot))
	assert.Equal(t, ActionNone, first.Action, "first tick starts the clock, it does not act")

	d.Now = func() time.Time { return start.Add(9 * time.Minute) }
	assert.Equal(t, ActionNone, only(t, d.Decide([]Worktree{hosted("a")}, snapshot)).Action)

	d.Now = func() time.Time { return start.Add(11 * time.Minute) }
	assert.Equal(t, ActionHibernate, only(t, d.Decide([]Worktree{hosted("a")}, snapshot)).Action)
}

// Unarchiving inside the debounce window must cancel the pending hibernation
// outright, not merely postpone it.
func TestUnarchivingCancelsPendingHibernation(t *testing.T) {
	archived := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", wtPath, at("2026-08-09T00:00:00Z"), nil)}}
	live := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", wtPath, nil, nil)}}

	start := time.Now()
	d := decider(start)
	d.Decide([]Worktree{hosted("a")}, archived)

	d.Now = func() time.Time { return start.Add(5 * time.Minute) }
	assert.Equal(t, ActionNone, only(t, d.Decide([]Worktree{hosted("a")}, live)).Action)

	// Well past the original deadline, and it must still not fire.
	d.Now = func() time.Time { return start.Add(30 * time.Minute) }
	assert.Equal(t, ActionNone, only(t, d.Decide([]Worktree{hosted("a")}, live)).Action)
}

func TestWakeWhenThreadReturnsToHibernatedWorktree(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", wtPath, nil, nil)}}
	worktree := hosted("a")
	worktree.Hibernated = true

	decision := only(t, decider(time.Now()).Decide([]Worktree{worktree}, snapshot))

	assert.Equal(t, ActionWake, decision.Action)
}

// A hibernated worktree whose threads are all still archived is already where
// it should be.
func TestHibernatedWorktreeStaysPut(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", wtPath, at("2026-08-09T00:00:00Z"), nil)}}
	worktree := hosted("a")
	worktree.Hibernated = true

	assert.Equal(t, ActionNone, only(t, decider(time.Now()).Decide([]Worktree{worktree}, snapshot)).Action)
}

// Deletion is the only thing that removes a tree, and it needs no debounce:
// the thread is already gone for good by the time we see this.
func TestTeardownWhenEveryThreadIsDeleted(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{}}

	decision := only(t, decider(time.Now()).Decide([]Worktree{hosted("a")}, snapshot))

	assert.Equal(t, ActionTeardown, decision.Action)
}

// Deleting a T3 project cascades into deleting every thread it holds. With one
// project per repository that is the whole repository arriving as deletions at
// once, and it must not be mistaken for somebody tidying up.
func TestBatchDeletionIsSuppressed(t *testing.T) {
	var worktrees []Worktree
	for _, name := range []string{"sydney", "berlin", "dublin", "osaka", "venice"} {
		worktrees = append(worktrees, Worktree{
			Project:      "kudtrading",
			Name:         name,
			Path:         "/home/u/.t3/worktrees/kudtrading/" + name,
			KnownThreads: []string{"t-" + name},
		})
	}

	decisions := decider(time.Now()).Decide(worktrees, &t3.ShellSnapshot{})

	assert.Equal(t, 0, TeardownCount(decisions), "five simultaneous deletions must not tear anything down")
}

// Below the cap, deletions are ordinary work and go through.
func TestSmallBatchDeletionProceeds(t *testing.T) {
	var worktrees []Worktree
	for _, name := range []string{"sydney", "berlin"} {
		worktrees = append(worktrees, Worktree{
			Project:      "kudtrading",
			Name:         name,
			Path:         "/home/u/.t3/worktrees/kudtrading/" + name,
			KnownThreads: []string{"t-" + name},
		})
	}

	decisions := decider(time.Now()).Decide(worktrees, &t3.ShellSnapshot{})

	assert.Equal(t, 2, TeardownCount(decisions))
}

// Worktrees made before T3 have no bound threads. Reading that as "every thread
// was deleted" would destroy all of them on the first tick.
func TestWorktreesWithoutThreadsAreIgnored(t *testing.T) {
	legacy := Worktree{Project: "kudtrading", Name: "london", Path: "/home/u/.conductor/kudtrading/london"}

	decisions := decider(time.Now()).Decide([]Worktree{legacy}, &t3.ShellSnapshot{})

	assert.Empty(t, decisions)
}

func TestRootAndArchivedWorktreesAreIgnored(t *testing.T) {
	root := hosted("a")
	root.IsRoot = true
	tombstone := hosted("a")
	tombstone.Archived = true

	decisions := decider(time.Now()).Decide([]Worktree{root, tombstone}, &t3.ShellSnapshot{})

	assert.Empty(t, decisions)
}

// The stored index is what lets a future deletion be traced back to a worktree,
// so it has to include archived threads and stay current on quiet ticks.
func TestThreadsAreReportedForRebinding(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{
		thread("a", wtPath, nil, nil),
		thread("b", wtPath, at("2026-08-09T00:00:00Z"), nil),
	}}

	decision := only(t, decider(time.Now()).Decide([]Worktree{hosted("a")}, snapshot))

	assert.ElementsMatch(t, []string{"a", "b"}, decision.Threads)
}

// A thread T3 has soft-deleted but not yet dropped from the snapshot is gone,
// whatever its archived flag says.
func TestDeletedThreadsDoNotHoldAWorktreeOpen(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{
		thread("a", wtPath, nil, at("2026-08-09T00:00:00Z")),
	}}

	assert.Equal(t, ActionTeardown, only(t, decider(time.Now()).Decide([]Worktree{hosted("a")}, snapshot)).Action)
}

// Paths differing only by a trailing separator are the same worktree.
func TestPathComparisonToleratesTrailingSeparator(t *testing.T) {
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", wtPath+"/", nil, nil)}}

	assert.Equal(t, ActionNone, only(t, decider(time.Now()).Decide([]Worktree{hosted("a")}, snapshot)).Action)
}
