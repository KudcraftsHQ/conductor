package t3

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hostedWorktree(t *testing.T, threadIDs ...string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, WriteMarker(dir, threadIDs))
	return dir
}

func TestMarkerRoundTrip(t *testing.T) {
	dir := hostedWorktree(t, "thread-1")

	got, ok := ReadMarker(dir)
	require.True(t, ok)
	assert.Equal(t, []string{"thread-1"}, got)
	assert.True(t, HasMarker(dir))

	RemoveMarker(dir)
	assert.False(t, HasMarker(dir))
}

// A worktree carrying several threads is the normal case once T3 starts reusing
// worktrees, so the marker has to survive the round trip as a set.
func TestMarkerHoldsSeveralThreads(t *testing.T) {
	dir := hostedWorktree(t, "thread-1", "thread-2", "thread-3")

	got, ok := ReadMarker(dir)
	require.True(t, ok)
	assert.Equal(t, []string{"thread-1", "thread-2", "thread-3"}, got)
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
	legacy := Candidate{Project: "old", Worktree: "one", WorktreePath: t.TempDir()}

	client := &Client{}
	drifted, err := client.Reconcile(t.Context(), []Candidate{legacy})
	require.NoError(t, err)
	assert.Empty(t, drifted, "an unmarked worktree is not T3-hosted and cannot have drifted")
}

func TestCountHosted(t *testing.T) {
	worktrees := []Candidate{
		{WorktreePath: hostedWorktree(t, "a")},
		{WorktreePath: t.TempDir()},
		{WorktreePath: hostedWorktree(t, "b")},
	}
	assert.Equal(t, 2, CountHosted(worktrees))
}

func str(s string) *string { return &s }

// settled builds a thread the way the server actually projects one: a
// `thread.settle` writes settledOverride AND settledAt together, and every one
// of the 46 settled threads in the live snapshot carried both. A fixture with a
// bare settledAt does not occur, and asserting on it would test a state T3
// cannot produce.
func settledThread(id, path string) Thread {
	return Thread{
		ID:              id,
		WorktreePath:    &path,
		SettledAt:       str("2026-08-20T02:00:00Z"),
		SettledOverride: str(SettledOverrideSettled),
	}
}

// The bug this whole change exists to fix: T3 records a finished thread by
// setting settledAt, not by archiving it. Reading only archivedAt reported every
// settled worktree as active and left its dev server running forever.
func TestThreadSettled(t *testing.T) {
	assert.False(t, Thread{}.Settled(), "a fresh thread is not settled")
	assert.True(t, Thread{SettledAt: str("2026-08-20T02:00:00Z")}.Settled())

	// An override is authoritative in both directions, so a thread the user has
	// deliberately reopened stays active despite its stale settledAt.
	reopened := Thread{
		SettledAt:       str("2026-08-20T02:00:00Z"),
		SettledOverride: str(SettledOverrideActive),
	}
	assert.False(t, reopened.Settled(), "an explicit active override reopens the thread")

	forced := Thread{SettledOverride: str(SettledOverrideSettled)}
	assert.True(t, forced.Settled(), "an explicit settled override needs no timestamp")

	// Settled and archived are different things and must stay so: a settled
	// worktree keeps its database, an archived one does not.
	settled := settledThread("done", "/tmp/x")
	assert.False(t, settled.Archived(), "settling is not archiving")
	assert.True(t, settled.Done())
}

// classifyWith runs the decision against a snapshot of the given threads.
func classifyWith(t *testing.T, threads []Thread, worktrees []Candidate) []Assessment {
	t.Helper()
	return ClassifySnapshot(&ShellSnapshot{Threads: threads}, worktrees)
}

func TestClassifySortsWorktreesByThreadState(t *testing.T) {
	activePath := hostedWorktree(t, "live")
	settledPath := hostedWorktree(t, "done")
	driftedPath := hostedWorktree(t, "gone")

	threads := []Thread{
		{ID: "live", WorktreePath: &activePath},
		settledThread("done", settledPath),
		{ID: "gone", WorktreePath: &driftedPath, ArchivedAt: str("2026-08-20T02:00:00Z")},
	}
	got := classifyWith(t, threads, []Candidate{
		{Worktree: "active", WorktreePath: activePath},
		{Worktree: "settled", WorktreePath: settledPath},
		{Worktree: "drifted", WorktreePath: driftedPath},
	})

	require.Len(t, got, 3)
	assert.Equal(t, StateActive, got[0].State)
	assert.Equal(t, StateSettled, got[1].State)
	assert.Equal(t, StateDrifted, got[2].State,
		"an archived thread releases its worktree, which is what makes it drifted")
}

// The case that would cause real damage: T3 reuses a worktree for a new thread
// on the same branch, so one finished piece of work says nothing about the
// others. Stopping the shared dev server here breaks the threads still running.
func TestClassifyKeepsWorktreeActiveWhileAnyThreadIsUnsettled(t *testing.T) {
	path := hostedWorktree(t, "one", "two")
	threads := []Thread{
		settledThread("one", path),
		{ID: "two", WorktreePath: &path},
	}

	got := classifyWith(t, threads, []Candidate{{Worktree: "shared", WorktreePath: path}})

	require.Len(t, got, 1)
	assert.Equal(t, StateActive, got[0].State)
	assert.Equal(t, 2, got[0].Threads)
	assert.Equal(t, 1, got[0].Settled, "the counts explain the verdict to the user")
}

func TestClassifySettlesOnlyWhenEveryThreadIs(t *testing.T) {
	path := hostedWorktree(t, "one", "two")
	threads := []Thread{
		settledThread("one", path),
		{ID: "two", WorktreePath: &path, SettledOverride: str(SettledOverrideSettled)},
	}

	got := classifyWith(t, threads, []Candidate{{Worktree: "shared", WorktreePath: path}})

	require.Len(t, got, 1)
	assert.Equal(t, StateSettled, got[0].State)
	assert.Equal(t, 2, got[0].Settled)
}

// A settled worktree must never be offered for archiving: the user finished the
// work but kept the tree, and its database is still theirs.
func TestReconcileDoesNotReportSettledWorktreesAsDrift(t *testing.T) {
	path := hostedWorktree(t, "done")
	threads := []Thread{settledThread("done", path)}

	got := ClassifySnapshot(&ShellSnapshot{Threads: threads},
		[]Candidate{{Worktree: "settled", WorktreePath: path}})

	require.Len(t, got, 1)
	assert.NotEqual(t, StateDrifted, got[0].State,
		"settling stops the dev server; it does not drop the database")
	assert.Empty(t, InState(got, StateDrifted))
}

func TestInState(t *testing.T) {
	assessments := []Assessment{
		{Candidate: Candidate{Worktree: "a"}, State: StateActive},
		{Candidate: Candidate{Worktree: "b"}, State: StateSettled},
		{Candidate: Candidate{Worktree: "c"}, State: StateSettled},
	}
	assert.Len(t, InState(assessments, StateSettled), 2)
	assert.Len(t, InState(assessments, StateDrifted), 0)
}

func TestNormalizePath(t *testing.T) {
	assert.Equal(t, normalizePath("/a/b/"), normalizePath("/a/b"))
	assert.Equal(t, normalizePath("/a/./b"), normalizePath("/a/b"))
}

// T3 settles server-side as of upstream f32f9a2f4: ThreadSettlementReactor
// stamps settledAt and settledOverride, including the auto-settle-on-merge arm
// conductor used to reproduce with a gh lookup. So the projected fields are the
// whole answer, and a thread with neither set is genuinely still active.
func TestSettledReadsTheServersRuling(t *testing.T) {
	assert.False(t, Thread{}.Settled(), "no ruling from the server means active")
	assert.True(t, Thread{SettledAt: str("2026-08-20T02:00:00Z")}.Settled())
	assert.True(t, Thread{SettledOverride: str(SettledOverrideSettled)}.Settled(),
		"an explicit settle needs no timestamp")
	assert.False(t, Thread{
		SettledAt:       str("2026-08-20T02:00:00Z"),
		SettledOverride: str(SettledOverrideActive),
	}.Settled(), "the keep-active pin outranks a stale settledAt")
}

// The one piece of policy conductor keeps on its own side. The server will not
// auto-settle a running or blocked thread, but an explicit settle can still land
// on one — and conductor acts on this verdict by stopping a dev server. Pulling
// the port out from under a live turn, or from under someone about to answer an
// approval prompt, is worse than leaving the server up.
func TestSettledKeepsBlockedThreadsActive(t *testing.T) {
	for _, blocked := range []Thread{
		{HasPendingApprovals: true, SettledOverride: str(SettledOverrideSettled)},
		{HasPendingUserInput: true, SettledOverride: str(SettledOverrideSettled)},
		{Session: &ThreadSession{Status: "running"}, SettledOverride: str(SettledOverrideSettled)},
		{Session: &ThreadSession{Status: "starting"}, SettledAt: str("2026-08-20T02:00:00Z")},
	} {
		assert.False(t, blocked.Settled(),
			"a blocked thread keeps its dev server however settled it is marked")
	}
}

// A stopped session is the ordinary state of a finished thread, not a blocker;
// treating it as one would settle nothing and leave every server running.
func TestSettledStoppedSessionIsNotABlocker(t *testing.T) {
	thread := Thread{
		Session:         &ThreadSession{Status: "stopped"},
		SettledOverride: str(SettledOverrideSettled),
	}
	assert.True(t, thread.Settled())
}

// Classification follows the server's ruling: a worktree whose only thread the
// server has settled is settled, and one it has not is active.
func TestClassifyFollowsServerSettlement(t *testing.T) {
	donePath := hostedWorktree(t, "done")
	livePath := hostedWorktree(t, "live")

	got := ClassifySnapshot(&ShellSnapshot{Threads: []Thread{
		settledThread("a", donePath),
		{ID: "b", WorktreePath: &livePath},
	}}, []Candidate{
		{Worktree: "done", WorktreePath: donePath},
		{Worktree: "live", WorktreePath: livePath},
	})

	require.Len(t, got, 2)
	assert.Equal(t, StateSettled, got[0].State)
	assert.Equal(t, StateActive, got[1].State)
}
