package ready

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hammashamzah/conductor/internal/config"
)

// setupOnly keeps the tests off the database and the network: those phases have
// their own probes and are not what the state machine is for.
var setupOnly = []Phase{PhaseSetup}

func cfgWith(t *testing.T, repoRoot, worktreePath string, worktree *config.Worktree) *config.Config {
	t.Helper()
	worktrees := map[string]*config.Worktree{
		"root": {Path: repoRoot, IsRoot: true, Branch: "main"},
	}
	if worktree != nil {
		worktree.Path = worktreePath
		worktrees["bandung"] = worktree
	}
	return &config.Config{
		Projects: map[string]*config.Project{
			"demo": {Path: repoRoot, Worktrees: worktrees},
		},
	}
}

// The case the old sentinel got wrong. A worktree of a registered project that
// conductor has not registered yet is about to be provisioned — reporting it as
// ready is how an agent ends up querying a database that does not exist.
func TestUnregisteredWorktreeOfKnownProjectIsImminent(t *testing.T) {
	repo, worktree := gitRepoWithWorktree(t)
	cfg := cfgWith(t, repo, worktree, nil)

	status := Resolve(cfg, worktree, setupOnly)
	assert.Equal(t, StateImminent, status.State)
	assert.Equal(t, "demo", status.Project)
	assert.False(t, status.State.Done())
	assert.False(t, status.State.Terminal(), "an imminent worktree is worth waiting for")
}

// A directory that has nothing to do with conductor must not block forever —
// it is an error, not a wait.
func TestUnrelatedDirectoryIsUnknown(t *testing.T) {
	cfg := cfgWith(t, t.TempDir(), "", nil)

	status := Resolve(cfg, t.TempDir(), setupOnly)
	assert.Equal(t, StateUnknown, status.State)
	assert.True(t, status.State.Terminal())
}

// The project's own checkout is not pending anything.
func TestMainCheckoutIsNotImminent(t *testing.T) {
	repo, _ := gitRepoWithWorktree(t)
	cfg := &config.Config{Projects: map[string]*config.Project{
		"demo": {Path: repo, Worktrees: map[string]*config.Worktree{}},
	}}

	status := Resolve(cfg, repo, setupOnly)
	assert.True(t, status.State.Done())
}

func TestSetupStates(t *testing.T) {
	repo, path := gitRepoWithWorktree(t)

	for _, tc := range []struct {
		name   string
		status config.SetupStatus
		want   State
		done   bool
	}{
		{"running blocks", config.SetupStatusRunning, StateProvisioning, false},
		{"creating blocks", config.SetupStatusCreating, StateProvisioning, false},
		{"failed is terminal", config.SetupStatusFailed, StateFailed, false},
		{"done is ready", config.SetupStatusDone, StateReady, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wt := &config.Worktree{Branch: "feature"}
			wt.MarkSetup(tc.status)
			cfg := cfgWith(t, repo, path, wt)

			status := Resolve(cfg, path, setupOnly)
			assert.Equal(t, tc.want, status.State)
			assert.Equal(t, tc.done, status.State.Done())
		})
	}
}

// "running" written by a process that has since been killed is the failure the
// old sentinel had no answer for: it waited forever. It has to be terminal.
func TestStalledSetupIsTerminal(t *testing.T) {
	repo, path := gitRepoWithWorktree(t)

	wt := &config.Worktree{Branch: "feature"}
	wt.MarkSetup(config.SetupStatusRunning)
	wt.SetupPID = deadPID(t)
	cfg := cfgWith(t, repo, path, wt)

	status := Resolve(cfg, path, setupOnly)
	assert.Equal(t, StateStalled, status.State)
	assert.True(t, status.State.Terminal())
	assert.Contains(t, status.Detail, "is gone")
}

// The three ways of establishing that nothing is provisioning a worktree that
// says it is being provisioned.
func TestSetupStalledRecognisesEveryAbandonedShape(t *testing.T) {
	// A live pid is the strongest evidence there is that it is fine.
	fresh := &config.Worktree{SetupStatus: config.SetupStatusRunning,
		SetupPID: os.Getpid(), SetupStartedAt: time.Now()}
	assert.False(t, fresh.SetupStalled(StaleAfter))

	// A dead pid settles it regardless of how recent the timestamp is.
	crashed := &config.Worktree{SetupStatus: config.SetupStatusRunning,
		SetupPID: deadPID(t), SetupStartedAt: time.Now()}
	assert.True(t, crashed.SetupStalled(StaleAfter))

	// No pid but a timestamp: age decides.
	recent := &config.Worktree{SetupStatus: config.SetupStatusRunning, SetupStartedAt: time.Now()}
	assert.False(t, recent.SetupStalled(StaleAfter))
	old := &config.Worktree{SetupStatus: config.SetupStatusRunning,
		SetupStartedAt: time.Now().Add(-2 * StaleAfter)}
	assert.True(t, old.SetupStalled(StaleAfter))

	// No ownership at all can only be an entry from before MarkSetup existed,
	// which means it has been sitting there since before this binary was
	// installed. Believing it is how a worktree blocks forever.
	legacy := &config.Worktree{SetupStatus: config.SetupStatusRunning}
	assert.True(t, legacy.SetupStalled(StaleAfter))

	// A finished status is never stalled, whatever the pid says.
	done := &config.Worktree{SetupStatus: config.SetupStatusDone, SetupPID: deadPID(t)}
	assert.False(t, done.SetupStalled(StaleAfter))
}

// Worktrees that predate status tracking must not be refused: that would be a
// worse failure than the one this package fixes.
func TestWorktreeWithNoStatusEverRecordedIsUsable(t *testing.T) {
	repo, path := gitRepoWithWorktree(t)
	cfg := cfgWith(t, repo, path, &config.Worktree{Branch: "feature"})

	status := Resolve(cfg, path, setupOnly)
	assert.Equal(t, StateUnmanaged, status.State)
	assert.True(t, status.State.Done())
}

// MarkSetup owns the pid, so a finished status must not leave a stale one
// behind for the next in-progress status to inherit.
func TestMarkSetupClearsOwnershipWhenFinished(t *testing.T) {
	wt := &config.Worktree{}

	wt.MarkSetup(config.SetupStatusRunning)
	assert.Equal(t, os.Getpid(), wt.SetupPID)
	assert.False(t, wt.SetupStartedAt.IsZero())

	wt.MarkSetup(config.SetupStatusDone)
	assert.Zero(t, wt.SetupPID)
	assert.True(t, wt.SetupStartedAt.IsZero())
}

func TestParsePhases(t *testing.T) {
	phases, err := ParsePhases("")
	require.NoError(t, err)
	assert.Equal(t, DefaultPhases, phases)

	// The dev server is deliberately not a default: a task whose job is to fix
	// a server that will not boot must not deadlock waiting for it to boot.
	assert.NotContains(t, DefaultPhases, PhaseServer)

	phases, err = ParsePhases("all")
	require.NoError(t, err)
	assert.Equal(t, AllPhases, phases)

	phases, err = ParsePhases(" db , server ")
	require.NoError(t, err)
	assert.Equal(t, []Phase{PhaseDB, PhaseServer}, phases)

	_, err = ParsePhases("everything")
	assert.Error(t, err)
}

// Phases are checked in order and reporting stops at the first unmet one, so
// the caller is told what it is actually waiting for rather than the last thing
// in the list.
func TestResolveReportsTheFirstUnmetPhase(t *testing.T) {
	repo, path := gitRepoWithWorktree(t)
	wt := &config.Worktree{Branch: "feature", Ports: []int{1}}
	wt.MarkSetup(config.SetupStatusRunning)
	cfg := cfgWith(t, repo, path, wt)

	status := Resolve(cfg, path, AllPhases)
	assert.Equal(t, PhaseSetup, status.Phase)
	assert.Equal(t, StateProvisioning, status.State)
}

// A worktree with no database configured has nothing to wait for, and must not
// be held up by a phase that does not apply to it.
func TestDatabasePhaseSkippedWhenNoneConfigured(t *testing.T) {
	repo, path := gitRepoWithWorktree(t)
	wt := &config.Worktree{Branch: "feature"}
	wt.MarkSetup(config.SetupStatusDone)
	cfg := cfgWith(t, repo, path, wt)

	status := Resolve(cfg, path, []Phase{PhaseSetup, PhaseDB})
	assert.Equal(t, StateReady, status.State)
}

// The database probe has to be a query, not a connection: a Postgres server
// answers on its port long before a particular database exists on it, and "the
// database does not exist yet" is the failure this package was written for.
func TestDatabasePhaseBlocksWhenUnreachable(t *testing.T) {
	repo, path := gitRepoWithWorktree(t)
	wt := &config.Worktree{Branch: "feature", DatabaseName: "dev_nowhere",
		// Port 1 is reserved and never listening, so this fails fast.
		DatabaseURL: "postgres://u:p@127.0.0.1:1/dev_nowhere?sslmode=disable&connect_timeout=1"}
	wt.MarkSetup(config.SetupStatusDone)
	cfg := cfgWith(t, repo, path, wt)

	status := Resolve(cfg, path, []Phase{PhaseSetup, PhaseDB})
	assert.Equal(t, StatePending, status.State)
	assert.Equal(t, PhaseDB, status.Phase)
	assert.Contains(t, status.Detail, "dev_nowhere")
	assert.False(t, status.State.Terminal(), "an absent database is worth waiting for")
}

// Nothing listens on port 1, so the server phase must not report ready.
func TestServerPhaseBlocksWhenNothingListens(t *testing.T) {
	repo, path := gitRepoWithWorktree(t)
	wt := &config.Worktree{Branch: "feature", Ports: []int{1}}
	wt.MarkSetup(config.SetupStatusDone)
	cfg := cfgWith(t, repo, path, wt)

	status := Resolve(cfg, path, []Phase{PhaseServer})
	assert.Equal(t, StatePending, status.State)
	assert.Contains(t, status.Detail, "port 1")
}

func TestTailSetupLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.log")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0644))

	assert.Equal(t, "three\nfour", TailSetupLog(path, 2))
	assert.Equal(t, "one\ntwo\nthree\nfour", TailSetupLog(path, 99))
	assert.Empty(t, TailSetupLog(filepath.Join(t.TempDir(), "missing.log"), 5))
	assert.Empty(t, TailSetupLog("", 5))
}

// gitRepoWithWorktree builds a real repository and a real linked worktree,
// because the imminent-vs-unknown decision is answered by git rather than by
// conductor's own config.
func gitRepoWithWorktree(t *testing.T) (repo, worktree string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	base := t.TempDir()
	repo = filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(repo, 0755))

	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run(repo, "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README"), []byte("x\n"), 0644))
	run(repo, "add", "README")
	run(repo, "commit", "-m", "init")

	worktree = filepath.Join(base, "wt")
	run(repo, "worktree", "add", "-b", "feature", worktree)

	// t.TempDir can sit behind a symlink (/tmp on macOS); Resolve works in
	// absolute paths, so the expectations have to as well.
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	resolvedWorktree, err := filepath.EvalSymlinks(worktree)
	require.NoError(t, err)
	return resolvedRepo, resolvedWorktree
}

// deadPID returns a pid that is certainly not running: a child that has been
// waited on. Picking a large number instead would be a guess.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}
