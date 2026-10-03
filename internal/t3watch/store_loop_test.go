package t3watch

import (
	"os"
	"testing"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/stretchr/testify/require"
)

// These drive Decide through the real store and collect, the way Tick does,
// rather than with hand-built Worktree values. The hand-built ones are why a
// snapshot that dropped Hibernated went unnoticed: every decide test set the
// flag directly, and the store never got a say.

// loopStore registers wt the way a T3-hosted worktree is registered on this
// machine: in conductor.json and in the tree's own marker file.
func loopStore(t *testing.T, wt *config.Worktree) *store.Store {
	t.Helper()
	wt.Path = t.TempDir()
	require.NoError(t, t3.WriteMarker(wt.Path, wt.T3Threads))
	s := store.New(config.NewConfig(), store.WithDisableSave())
	t.Cleanup(func() { _, _ = s.Close() })
	require.NoError(t, s.AddProject("kudtrading", &config.Project{Path: "/repo", Worktrees: map[string]*config.Worktree{}}))
	require.NoError(t, s.AddWorktree("kudtrading", "chicago-4", wt))
	return s
}

func pass(t *testing.T, d *Decider, s *store.Store, snapshot *t3.ShellSnapshot) Decision {
	t.Helper()
	decisions := d.Decide(collect(s.GetConfigSnapshot()), snapshot)
	require.Len(t, decisions, 1)
	return decisions[0]
}

func pathOf(s *store.Store) string {
	return s.GetConfigSnapshot().Projects["kudtrading"].Worktrees["chicago-4"].Path
}

// The live bug: kudtrading/chicago-4 was hibernated once and then again on
// every five-second tick, re-running archive.sh and the remote DROP DATABASE.
func TestArchivedWorktreeHibernatesExactlyOnce(t *testing.T) {
	s := loopStore(t, &config.Worktree{
		Branch: "fix", Ports: []int{3000}, DatabaseName: "dev_chicago_4",
		SetupStatus: config.SetupStatusDone, T3Threads: []string{"a"},
	})
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", pathOf(s), at("2026-10-03T07:46:26Z"), nil)}}
	start := time.Now()
	d := decider(start)

	hibernations := 0
	for tick := 0; tick < 200; tick++ { // ~16 minutes of 5s ticks
		now := start.Add(time.Duration(tick) * DefaultInterval)
		d.Now = func() time.Time { return now }
		if pass(t, d, s, snapshot).Action == ActionHibernate {
			hibernations++
			require.NoError(t, s.HibernateWorktree("kudtrading", "chicago-4")) // what ReleaseResources records
		}
	}

	require.Equal(t, 1, hibernations)
}

// The wake half never ran either: with Hibernated always false, a thread
// coming back to a hibernated worktree was treated as an ordinary live one
// and got a dev server with no ports and no database.
func TestReturningThreadWakesHibernatedWorktreeOnce(t *testing.T) {
	s := loopStore(t, &config.Worktree{
		Branch: "fix", DatabaseName: "dev_chicago_4",
		SetupStatus: config.SetupStatusDone, T3Threads: []string{"a"},
	})
	require.NoError(t, s.HibernateWorktree("kudtrading", "chicago-4"))
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", pathOf(s), nil, nil)}}
	d := decider(time.Now())

	require.Equal(t, ActionWake, pass(t, d, s, snapshot).Action)
	require.NoError(t, s.WakeWorktree("kudtrading", "chicago-4", []int{3000}, "dev_chicago_4", "postgres://x"))
	require.Equal(t, ActionNone, pass(t, d, s, snapshot).Action)
}

// The double-provision guard reads SetupPID through the snapshot, which used
// to drop it — so every in-progress setup looked stalled and the guard never
// held.
func TestWakeWaitsForAProvisionInProgress(t *testing.T) {
	s := loopStore(t, &config.Worktree{
		Branch: "fix", T3Threads: []string{"a"},
		SetupStatus: config.SetupStatusRunning, SetupPID: os.Getpid(), SetupStartedAt: time.Now(),
	})
	require.NoError(t, s.HibernateWorktree("kudtrading", "chicago-4"))
	snapshot := &t3.ShellSnapshot{Threads: []t3.Thread{thread("a", pathOf(s), nil, nil)}}

	require.Equal(t, ActionNone, pass(t, decider(time.Now()), s, snapshot).Action)
}
