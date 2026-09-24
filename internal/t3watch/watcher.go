package t3watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/stray"
	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/hammashamzah/conductor/internal/tmux"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// Defaults for the watcher loop.
const (
	// DefaultInterval is how often T3 is polled. The snapshot is small and
	// local; this is nowhere near expensive enough to justify a subscription.
	DefaultInterval = 5 * time.Second
	// DefaultDebounce is how long a worktree sits with no live thread before
	// its resources are released.
	DefaultDebounce = 10 * time.Minute
	// DefaultMaxTeardowns is the batch above which deletions are treated as an
	// accident — a deleted project, most likely — rather than an instruction.
	DefaultMaxTeardowns = 3
	// DefaultSettleDebounce is how long a worktree's threads must all stay
	// settled before its dev server is stopped.
	DefaultSettleDebounce = 2 * time.Minute
	// DevSweepInterval is how often every worktree's dev server is checked
	// against what its threads want, not only the ones whose verdict changed.
	// That catches a server killed from outside, or one an agent started by
	// hand, without probing tmux for a hundred worktrees every five seconds.
	DevSweepInterval = time.Minute
)

// Watcher follows T3's threads and keeps conductor's worktrees in step.
type Watcher struct {
	store   *store.Store
	client  *t3.Client
	decider *Decider

	Interval time.Duration
	// DryRun reports what it would do and does none of it.
	DryRun bool
	Log    io.Writer

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	lastErr error

	// devApplied is the last dev verdict carried out per worktree, so a tick
	// only touches tmux for the worktrees whose verdict changed.
	devApplied map[string]DevWant
	lastSweep  time.Time
	// lastRefused is the suppressed teardown count last reported, so the
	// refusal is logged when it changes rather than on every tick.
	lastRefused int
}

// New builds a watcher over the given store.
func New(s *store.Store) (*Watcher, error) {
	client, err := t3.New()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		store:      s,
		client:     client,
		decider:    NewDecider(DefaultDebounce, DefaultMaxTeardowns),
		Interval:   DefaultInterval,
		Log:        os.Stdout,
		devApplied: make(map[string]DevWant),
	}, nil
}

// SetDebounce overrides how long hibernation waits.
func (w *Watcher) SetDebounce(d time.Duration) { w.decider.Debounce = d }

// SetSettleDebounce overrides how long a settled worktree keeps its dev server.
func (w *Watcher) SetSettleDebounce(d time.Duration) { w.decider.SettleDebounce = d }

// SetMaxTeardowns overrides the batch-deletion cap.
func (w *Watcher) SetMaxTeardowns(n int) { w.decider.MaxTeardowns = n }

func (w *Watcher) logf(format string, args ...any) {
	if w.Log == nil {
		return
	}
	fmt.Fprintf(w.Log, "[t3watch] "+format+"\n", args...)
}

// Start runs the loop until Stop is called.
func (w *Watcher) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return fmt.Errorf("watcher is already running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err := w.client.Ping(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("T3 Code is not reachable: %w", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan struct{})

	go w.loop(ctx)
	return nil
}

// Stop ends the loop and waits for the current tick to finish.
func (w *Watcher) Stop() {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.cancel = nil
	w.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (w *Watcher) loop(ctx context.Context) {
	defer close(w.done)

	// The first pass is a catch-up: nothing replays what happened while the
	// watcher was down, so every worktree is judged from scratch on boot.
	if err := w.Tick(ctx); err != nil {
		w.logf("first pass failed: %v", err)
	}

	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.Tick(ctx); err != nil {
				w.mu.Lock()
				w.lastErr = err
				w.mu.Unlock()
				w.logf("tick failed: %v", err)
			}
		}
	}
}

// Tick runs one reconciliation pass. Exported so `conductor t3 watch once` can
// run exactly one, which is also how the whole thing is exercised by hand.
func (w *Watcher) Tick(ctx context.Context) error {
	if err := w.store.Reload(); err != nil {
		return fmt.Errorf("failed to reload conductor state: %w", err)
	}

	snapshot, err := w.client.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("failed to read the T3 snapshot: %w", err)
	}

	cfg := w.store.GetConfigSnapshot()
	worktrees := collect(cfg)
	decisions := w.decider.Decide(worktrees, snapshot)

	// Decide suppresses an implausibly large batch of deletions rather than
	// acting on it, but staying silent about that would look identical to a
	// quiet tick — and this is exactly the moment somebody needs to know.
	// It is said once per change of count, though: repeated every five seconds
	// it grew the log to tens of megabytes and buried everything else.
	refused := countGoneWorktrees(worktrees, snapshot)
	if refused <= w.decider.MaxTeardowns {
		refused = 0
	}
	if refused != w.lastRefused && refused > 0 {
		w.logf("refusing to tear down %d worktrees at once — deleting a T3 project deletes every thread in it. "+
			"Run 'conductor t3 reconcile --archive' if this really was intended.", refused)
	}
	w.lastRefused = refused

	for _, decision := range decisions {
		w.apply(cfg, decision)
	}
	w.syncDevServers(decisions)
	return nil
}

// syncDevServers makes each live worktree's dev server match its threads:
// running while any is unsettled, stopped once all are settled.
//
// The server must be the supervised one in the worktree's tmux window, never
// one an agent started by hand. A server on a worktree's port while its window
// is not serving is a stray: it is killed whatever the verdict, and a worktree
// that should be running then gets its supervised server in its place.
func (w *Watcher) syncDevServers(decisions []Decision) {
	if w.devApplied == nil {
		w.devApplied = make(map[string]DevWant)
	}
	now := time.Now()
	sweep := now.Sub(w.lastSweep) >= DevSweepInterval

	var due []Decision
	for _, decision := range decisions {
		key := decision.Worktree.Key()
		if decision.Dev == DevKeep {
			if decision.Action != ActionNone {
				delete(w.devApplied, key) // Judge it afresh once it settles down.
			}
			continue
		}
		if sweep || w.devApplied[key] != decision.Dev {
			due = append(due, decision)
		}
	}
	if sweep {
		w.lastSweep = now
	}
	if len(due) == 0 {
		return
	}

	listeners := stray.ListenersByPort()
	for _, decision := range due {
		if w.syncDevServer(decision, listeners) {
			w.devApplied[decision.Worktree.Key()] = decision.Dev
		}
	}
}

// syncDevServer carries out one worktree's verdict, and reports whether the
// worktree now matches it.
func (w *Watcher) syncDevServer(decision Decision, listeners map[int][]int) bool {
	worktree := decision.Worktree
	serving := tmux.WindowExists(worktree.Project, worktree.Branch) &&
		!tmux.DevServerStopped(worktree.Project, worktree.Branch)

	if !serving {
		var pids []int
		for _, port := range worktree.Ports {
			pids = append(pids, listeners[port]...)
		}
		if decision.Dev == DevStop && len(pids) == 0 {
			// An agent's `bun run dev` takes the project's default port, not
			// the worktree's. Once the work is settled, anything serving from
			// inside the tree outside tmux is one of those.
			pids = stray.Rooted(worktree.Path, listeners)
		}
		if len(pids) > 0 {
			w.logf("killing unsupervised dev server for %s (pid %v) — it belongs in the conductor window",
				worktree.Key(), pids)
			if !w.DryRun {
				stray.Kill(pids)
			}
		}
	}

	switch decision.Dev {
	case DevStop:
		if !serving {
			return true
		}
		w.logf("stopping dev server for %s — every thread on it is settled", worktree.Key())
		if w.DryRun {
			return false
		}
		if err := tmux.StopDevServer(worktree.Project, worktree.Branch); err != nil {
			w.logf("could not stop dev server for %s: %v", worktree.Key(), err)
			return false
		}
		return true

	case DevRun:
		if serving {
			return true
		}
		w.logf("starting dev server for %s — a thread on it is unsettled", worktree.Key())
		if w.DryRun {
			return false
		}
		if _, err := tmux.EnsureDevServer(worktree.Project, worktree.Branch, worktree.Path); err != nil {
			w.logf("could not start dev server for %s: %v", worktree.Key(), err)
			return false
		}
		return true
	}
	return true
}

// apply carries out one decision. Failures are logged and the pass continues:
// one worktree that cannot be woken must not stop the others being tidied.
func (w *Watcher) apply(cfg *config.Config, decision Decision) {
	worktree := decision.Worktree
	manager := workspace.NewManagerWithStore(cfg, w.store)

	// The thread index is refreshed on every tick, whatever the action. Once a
	// thread is deleted there is nothing left to associate it with a worktree,
	// so the index has to be current *before* that happens.
	w.rebind(worktree, decision.Threads)

	switch decision.Action {
	case ActionNone:
		return

	case ActionHibernate:
		w.logf("hibernating %s (%d archived thread(s), no live ones)", worktree.Key(), decision.Archive)
		if w.DryRun {
			return
		}
		if err := manager.ReleaseResources(worktree.Project, worktree.Name); err != nil {
			w.logf("could not hibernate %s: %v", worktree.Key(), err)
		}

	case ActionWake:
		w.logf("waking %s (%d live thread(s))", worktree.Key(), decision.Live)
		if w.DryRun {
			return
		}
		if err := manager.Provision(worktree.Project, worktree.Name); err != nil {
			w.logf("could not wake %s: %v", worktree.Key(), err)
		}
		OpenLogTerminals(w.client, worktree.Project, worktree.Branch, worktree.Path, decision.Threads)

	case ActionTeardown:
		w.logf("tearing down %s — every thread bound to it was deleted", worktree.Key())
		if w.DryRun {
			return
		}
		// Resources first, then the tree. The branch is deliberately kept: T3's
		// own removal leaves it alone, and a deleted thread must never be able
		// to destroy commits.
		if !worktree.Hibernated {
			if err := manager.ReleaseResources(worktree.Project, worktree.Name); err != nil {
				w.logf("could not release %s: %v", worktree.Key(), err)
			}
		}
		if err := manager.RemoveTree(worktree.Project, worktree.Name, false); err != nil {
			w.logf("could not remove %s: %v", worktree.Key(), err)
		}
		t3.RemoveMarker(worktree.Path)
	}
}

// rebind stores the current thread set for a worktree, in conductor's state and
// in the worktree's own marker file.
func (w *Watcher) rebind(worktree Worktree, threads []string) {
	if sameStrings(worktree.KnownThreads, threads) {
		return
	}
	if w.DryRun {
		return
	}
	if err := w.store.SetWorktreeThreads(worktree.Project, worktree.Name, threads); err != nil {
		w.logf("could not record threads for %s: %v", worktree.Key(), err)
	}
	if len(threads) > 0 {
		_ = t3.WriteMarker(worktree.Path, threads)
	}
}

// collect turns conductor's state into the watcher's view of it.
func collect(cfg *config.Config) []Worktree {
	if cfg == nil {
		return nil
	}
	var out []Worktree
	for projectName, project := range cfg.Projects {
		if project == nil {
			continue
		}
		for name, worktree := range project.Worktrees {
			if worktree == nil {
				continue
			}
			threads := worktree.T3Threads
			if len(threads) == 0 && worktree.Path != "" {
				// Fall back to the marker, so a worktree adopted by a version
				// that only wrote the file is still recognised.
				if ids, ok := t3.ReadMarker(worktree.Path); ok {
					threads = ids
				}
			}
			out = append(out, Worktree{
				Project:      projectName,
				Name:         name,
				Branch:       worktree.Branch,
				Path:         worktree.Path,
				Hibernated:   worktree.Hibernated,
				Archived:     worktree.Archived,
				IsRoot:       worktree.IsRoot,
				KnownThreads: threads,
				Ports:        worktree.Ports,
			})
		}
	}
	return out
}

// countGoneWorktrees counts how many hosted worktrees have lost every thread,
// before the batch cap is applied.
func countGoneWorktrees(worktrees []Worktree, snapshot *t3.ShellSnapshot) int {
	live, archived := indexThreads(snapshot)
	n := 0
	for _, worktree := range worktrees {
		if worktree.IsRoot || worktree.Archived || worktree.Path == "" || len(worktree.KnownThreads) == 0 {
			continue
		}
		key := normalize(worktree.Path)
		if len(live[key]) == 0 && len(archived[key]) == 0 {
			n++
		}
	}
	return n
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
		if seen[v] < 0 {
			return false
		}
	}
	return true
}
