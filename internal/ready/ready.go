// Package ready answers one question: is this worktree safe to work in yet?
//
// It exists because the answer used to be a file. `conductor adopt` wrote
// `.conductor-provisioning` while it worked and removed it when it finished,
// and agents were told to spin on it. That failed in six directions at once:
// only `adopt` ever wrote it, so the five other paths that provision a worktree
// left the check passing instantly; it was removed whether setup succeeded or
// failed; a killed provisioner left it behind forever; and it was written *after*
// conductor's own startup, so an agent whose first turn began immediately — which
// is exactly what T3 does — could check before it existed.
//
// The deeper problem is that absence meant two different things. "Ready" and
// "nobody ever set this up" were the same state, so every new caller had to
// remember to opt in, and forgetting failed open.
//
// So readiness is derived from state conductor already keeps — the worktree's
// SetupStatus in conductor.json, written by every provisioning path — rather
// than from a marker anyone has to remember to drop. The one case that state
// alone cannot cover is a worktree that exists on disk but has not been
// registered yet, which is the window T3 opens the first turn in. That is
// resolved by looking at the repository: a git worktree whose main repo is a
// registered conductor project, with no entry of its own, is about to be
// provisioned, not finished.
package ready

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/hammashamzah/conductor/internal/config"
)

// StaleAfter is how long an in-progress setup with no live process behind it,
// or no process recorded at all, is believed.
//
// A remote database clone is minutes, so this is generous. It only matters for
// entries written before conductor recorded the provisioning pid; anything
// newer is caught by the pid check immediately.
const StaleAfter = 45 * time.Minute

// Phase is one thing that has to be true before a worktree is usable.
type Phase string

const (
	// PhaseSetup is the database clone and the project's setup script.
	PhaseSetup Phase = "setup"
	// PhaseDB is the worktree's database accepting a query. This is the check
	// that matters for reading or writing data: setup returning is not the same
	// as the database being there.
	PhaseDB Phase = "db"
	// PhaseServer is the dev server accepting connections. Deliberately not a
	// default: a task whose job is to fix a dev server that will not boot must
	// not deadlock waiting for it to boot.
	PhaseServer Phase = "server"
)

// DefaultPhases is what `conductor wait` checks when asked for nothing
// specific.
var DefaultPhases = []Phase{PhaseSetup, PhaseDB}

// AllPhases is every phase, in the order they become true.
var AllPhases = []Phase{PhaseSetup, PhaseDB, PhaseServer}

// ParsePhases reads a comma-separated list, with "all" and "" as shorthands.
func ParsePhases(raw string) ([]Phase, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultPhases, nil
	}
	if raw == "all" {
		return AllPhases, nil
	}
	var phases []Phase
	for _, part := range strings.Split(raw, ",") {
		switch phase := Phase(strings.TrimSpace(part)); phase {
		case PhaseSetup, PhaseDB, PhaseServer:
			phases = append(phases, phase)
		case "":
			continue
		default:
			return nil, fmt.Errorf("unknown phase %q (want setup, db, server or all)", part)
		}
	}
	if len(phases) == 0 {
		return DefaultPhases, nil
	}
	return phases, nil
}

// State is how far along a worktree is.
type State string

const (
	// StateImminent is the window T3 starts the first turn in: the directory is
	// a worktree of a registered project but conductor has not registered it
	// yet, so provisioning is about to begin. Reported as not ready, which is
	// the whole point — the old file check reported ready here.
	StateImminent State = "imminent"
	// StateProvisioning is setup running, with a live process behind it.
	StateProvisioning State = "provisioning"
	// StateStalled is setup that claims to be running but whose process is gone.
	StateStalled State = "stalled"
	// StateFailed is a setup that ran and failed.
	StateFailed State = "failed"
	// StatePending is a phase that has not come true yet — the database is not
	// answering, or the dev server is not listening — while setup itself is done.
	StatePending State = "pending"
	// StateReady is every requested phase satisfied.
	StateReady State = "ready"
	// StateUnmanaged is a registered worktree that has never recorded a setup
	// status: everything created before conductor tracked it. Treated as ready,
	// because refusing to work in worktrees that predate this would be worse
	// than the problem it solves.
	StateUnmanaged State = "unmanaged"
	// StateUnknown is a path conductor has nothing to say about.
	StateUnknown State = "unknown"
)

// Done reports whether a state means work can start.
func (s State) Done() bool {
	return s == StateReady || s == StateUnmanaged
}

// Terminal reports whether waiting any longer is pointless.
func (s State) Terminal() bool {
	return s.Done() || s == StateFailed || s == StateStalled || s == StateUnknown
}

// Status is a readiness answer about one worktree.
type Status struct {
	State    State  `json:"state"`
	Phase    Phase  `json:"phase,omitempty"`
	Project  string `json:"project,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	Path     string `json:"path"`
	Detail   string `json:"detail,omitempty"`
	// SetupLog is where the setup script's output went, when there is one to
	// read. A failure the caller cannot see the reason for is barely better
	// than no failure at all.
	SetupLog string `json:"setupLog,omitempty"`
}

func (s Status) String() string {
	if s.Detail == "" {
		return string(s.State)
	}
	return fmt.Sprintf("%s — %s", s.State, s.Detail)
}

// Resolve reports how ready the worktree containing path is, checking the
// phases given in order and stopping at the first that is not satisfied.
func Resolve(cfg *config.Config, path string, phases []Phase) Status {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Status{State: StateUnknown, Path: path, Detail: err.Error()}
	}
	status := Status{Path: absPath}

	projectName, _, worktree, err := cfg.DetectProject(absPath)
	if err != nil || worktree == nil {
		return imminentOrUnknown(cfg, absPath)
	}
	status.Project, status.Worktree = projectName, worktreeNameOf(cfg, projectName, worktree)
	status.SetupLog = SetupLogPath(status.Project, status.Worktree)

	for _, phase := range phases {
		if satisfied, next := checkPhase(phase, worktree, status); !satisfied {
			return next
		}
	}
	if worktree.SetupStatus == config.SetupStatusNone {
		status.State = StateUnmanaged
		status.Detail = "no setup has ever been recorded for this worktree"
		return status
	}
	status.State = StateReady
	return status
}

// checkPhase evaluates one phase, returning the status to report when it is not
// yet satisfied.
func checkPhase(phase Phase, worktree *config.Worktree, status Status) (bool, Status) {
	status.Phase = phase
	switch phase {
	case PhaseSetup:
		switch {
		case worktree.SetupStalled(StaleAfter):
			status.State = StateStalled
			status.Detail = stalledReason(worktree) + "; re-run 'conductor adopt' in this worktree to provision it"
			return false, status
		case worktree.SetupStatus == config.SetupStatusFailed:
			status.State = StateFailed
			status.Detail = "the setup script failed"
			return false, status
		case worktree.SetupStatus.InProgress():
			status.State = StateProvisioning
			status.Detail = fmt.Sprintf("setup is %s", worktree.SetupStatus)
			if !worktree.SetupStartedAt.IsZero() {
				status.Detail += fmt.Sprintf(" (%s elapsed)", time.Since(worktree.SetupStartedAt).Truncate(time.Second))
			}
			return false, status
		}
	case PhaseDB:
		if worktree.DatabaseURL == "" {
			return true, status // No database configured; nothing to wait for.
		}
		if err := pingDatabase(worktree.DatabaseURL); err != nil {
			status.State = StatePending
			status.Detail = fmt.Sprintf("database %s is not answering: %v", worktree.DatabaseName, err)
			return false, status
		}
	case PhaseServer:
		port := devPort(worktree)
		if port == 0 {
			return true, status // No port allocated; nothing to dial.
		}
		if !portListening(port) {
			status.State = StatePending
			status.Detail = fmt.Sprintf("nothing is listening on port %d yet", port)
			return false, status
		}
	}
	return true, status
}

// stalledReason says which of the three ways of being abandoned this is, so the
// message distinguishes a crash from an entry left by an older conductor.
func stalledReason(worktree *config.Worktree) string {
	switch {
	case worktree.SetupPID > 0:
		return fmt.Sprintf("setup is recorded as %q but the process that started it (pid %d) is gone",
			worktree.SetupStatus, worktree.SetupPID)
	case !worktree.SetupStartedAt.IsZero():
		return fmt.Sprintf("setup has been %q since %s, far longer than any real setup takes",
			worktree.SetupStatus, worktree.SetupStartedAt.Format(time.RFC3339))
	default:
		return fmt.Sprintf("setup is recorded as %q with no owning process, so it was left behind by an earlier conductor",
			worktree.SetupStatus)
	}
}

// imminentOrUnknown decides what an unregistered path means.
//
// This is the case the old sentinel got wrong. A directory that is a git
// worktree of a repository conductor knows about, but has no entry of its own,
// is one T3 has just created and whose provisioning hook has not got far enough
// to register it. Reporting that as ready is how an agent ends up running
// migrations against a database that does not exist.
func imminentOrUnknown(cfg *config.Config, absPath string) Status {
	status := Status{Path: absPath, State: StateUnknown,
		Detail: "not inside a conductor worktree"}

	root, err := repoRoot(absPath)
	if err != nil {
		return status
	}
	projectName, _, ok := cfg.GetProjectByPath(root)
	if !ok {
		return status
	}
	// The main checkout of a registered project is not pending anything.
	if sameDir(root, absPath) {
		status.State = StateUnmanaged
		status.Detail = "this is the project's main checkout"
		status.Project = projectName
		return status
	}
	status.State = StateImminent
	status.Project = projectName
	status.Phase = PhaseSetup
	status.Detail = "a worktree of " + projectName + " that conductor has not registered yet — provisioning has not started"
	return status
}

// Wait blocks until every requested phase is satisfied, or until the state
// becomes one that waiting cannot fix.
//
// onChange is called whenever the reported status changes, so a caller can show
// progress without polling itself. It is never called twice for the same
// status, which keeps a fifteen-minute database clone to one line of output.
func Wait(ctx context.Context, path string, phases []Phase, interval time.Duration, onChange func(Status)) (Status, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	var last string
	for {
		cfg, err := config.Load()
		if err != nil {
			return Status{State: StateUnknown, Path: path}, err
		}
		// Reloaded every tick on purpose: the whole point is to observe another
		// process finishing, and a snapshot taken once would never change.
		status := Resolve(cfg, path, phases)
		if onChange != nil && status.String() != last {
			onChange(status)
			last = status.String()
		}
		if status.State.Terminal() {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// SetupLogPath is where runSetupSync writes the setup script's output.
func SetupLogPath(projectName, worktreeName string) string {
	if projectName == "" || worktreeName == "" {
		return ""
	}
	dir, err := config.ConductorDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "logs", projectName, worktreeName+"-setup.log")
}

// TailSetupLog returns the last n lines of a setup log, for reporting why a
// failed setup failed.
func TailSetupLog(path string, n int) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// pingDatabase opens the worktree's database and runs a trivial query.
//
// Connecting is not enough on its own: a Postgres server answers on its port
// long before a particular database exists on it, and "the database does not
// exist yet" is the exact failure this package was written for.
func pingDatabase(url string) error {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var one int
	return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// portListening reports whether anything accepts a connection on the port.
func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// devPort is the port the dev server is expected on: the first allocated one,
// which is the same one the setup script writes into the project's .env.
func devPort(worktree *config.Worktree) int {
	if len(worktree.Ports) == 0 {
		return 0
	}
	return worktree.Ports[0]
}

// worktreeNameOf recovers the map key for a worktree, which DetectProject does
// not return but the setup log path is keyed by.
func worktreeNameOf(cfg *config.Config, projectName string, worktree *config.Worktree) string {
	project, ok := cfg.GetProject(projectName)
	if !ok {
		return ""
	}
	for name, candidate := range project.Worktrees {
		if candidate == worktree {
			return name
		}
	}
	return ""
}

// repoRoot returns the main repository a path belongs to, whether the path is
// the main checkout or one of its worktrees.
//
// This duplicates workspace.GetRootPath rather than calling it: workspace
// imports mux, mux gates thread creation on this package, and that is a cycle.
// The alternative — a gate that cannot ask whether a worktree is registered —
// would be worse than ten lines of git.
func repoRoot(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(path, gitDir)
	}
	return filepath.Dir(gitDir), nil
}

func sameDir(a, b string) bool {
	return strings.TrimRight(a, string(filepath.Separator)) ==
		strings.TrimRight(b, string(filepath.Separator))
}
