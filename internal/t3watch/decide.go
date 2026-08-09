// Package t3watch keeps conductor's worktrees in step with T3 Code's threads.
//
// T3 owns the lifecycle: you create a worktree by starting a thread on a new
// branch, and you dispose of one by deleting the last thread bound to it.
// Conductor owns everything around that — ports, databases, tunnels, dev
// servers — and so has to follow along. This package is the following.
//
// The mapping is three states:
//
//	at least one live thread   → active      (provisioned)
//	only archived threads      → hibernated  (resources released, tree kept)
//	no threads at all          → gone        (tree removed, entry dropped)
//
// Archiving is therefore reversible and deleting is not, which matches what T3
// itself does: its server only stops sessions and closes terminals on archive,
// and touches no files.
package t3watch

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/t3"
)

// Action is what the watcher decided to do about one worktree.
type Action string

const (
	// ActionNone means the worktree is in the state its threads imply.
	ActionNone Action = "none"
	// ActionHibernate releases ports, database and tunnel, keeping the tree.
	ActionHibernate Action = "hibernate"
	// ActionWake re-provisions a hibernated worktree after a thread came back.
	ActionWake Action = "wake"
	// ActionTeardown removes the tree: its last thread was deleted.
	ActionTeardown Action = "teardown"
)

// Worktree is the watcher's view of one registered worktree.
type Worktree struct {
	Project string
	Name    string
	// Branch is what the dev-server window is keyed by, so it is not
	// interchangeable with Name: the window is project/branch while the entry
	// is registered under a city.
	Branch       string
	Path         string
	Hibernated   bool
	Archived     bool
	IsRoot       bool
	KnownThreads []string
}

// Key identifies a worktree across ticks.
func (w Worktree) Key() string { return w.Project + "/" + w.Name }

// Decision is one worktree's outcome for this tick.
type Decision struct {
	Worktree Worktree
	Action   Action
	// Threads is every thread now bound to the worktree, live and archived,
	// and replaces the stored index. It is recorded even when the action is
	// none, because a deleted thread cannot be traced back to a worktree
	// afterwards — the index has to be current before the deletion happens.
	Threads []string
	Live    int
	Archive int
}

// Clock lets tests drive the debounce without sleeping.
type Clock func() time.Time

// Decider turns snapshots into actions. It holds the only state that spans
// ticks: when each worktree was last seen with no live threads.
type Decider struct {
	// Debounce is how long a worktree must sit with no live threads before it
	// hibernates. Archiving a thread by mistake should cost a click to undo,
	// not a multi-minute database clone.
	Debounce time.Duration
	// MaxTeardowns bounds how many worktrees may be torn down in one tick.
	//
	// Deleting a T3 *project* expands, in its decider, into a thread.delete for
	// every thread it holds. With one project per repository that is every
	// worktree of that repository arriving as a deletion at once. Past this
	// count the watcher does nothing and says so, and the operator decides.
	MaxTeardowns int
	// Now defaults to time.Now.
	Now Clock

	zeroLiveSince map[string]time.Time
}

// NewDecider returns a Decider with the given debounce and teardown cap.
func NewDecider(debounce time.Duration, maxTeardowns int) *Decider {
	return &Decider{
		Debounce:      debounce,
		MaxTeardowns:  maxTeardowns,
		Now:           time.Now,
		zeroLiveSince: make(map[string]time.Time),
	}
}

func (d *Decider) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Decide returns what to do with each worktree given the current snapshot.
//
// Worktrees T3 does not host are skipped entirely rather than reported as
// having no threads: a tree made under tmux or herdr never had one, and
// treating that as a deletion would destroy every worktree predating T3.
func (d *Decider) Decide(worktrees []Worktree, snapshot *t3.ShellSnapshot) []Decision {
	if d.zeroLiveSince == nil {
		d.zeroLiveSince = make(map[string]time.Time)
	}
	now := d.now()
	live, archived := indexThreads(snapshot)

	decisions := make([]Decision, 0, len(worktrees))
	teardowns := 0
	for _, worktree := range worktrees {
		if worktree.IsRoot || worktree.Archived || worktree.Path == "" {
			continue
		}
		if len(worktree.KnownThreads) == 0 {
			// Not T3-hosted. Invisible to this watcher, by design.
			continue
		}

		key := normalize(worktree.Path)
		liveIDs := live[key]
		archivedIDs := archived[key]

		decision := Decision{
			Worktree: worktree,
			Action:   ActionNone,
			Threads:  append(append([]string{}, liveIDs...), archivedIDs...),
			Live:     len(liveIDs),
			Archive:  len(archivedIDs),
		}

		switch {
		case len(liveIDs) == 0 && len(archivedIDs) == 0:
			// Every thread that ever held this worktree is gone from T3
			// altogether, which only happens by deletion.
			delete(d.zeroLiveSince, key)
			decision.Action = ActionTeardown
			teardowns++

		case len(liveIDs) > 0:
			delete(d.zeroLiveSince, key)
			if worktree.Hibernated {
				decision.Action = ActionWake
			}

		default: // Only archived threads remain.
			if worktree.Hibernated {
				break // Already asleep.
			}
			since, seen := d.zeroLiveSince[key]
			if !seen {
				d.zeroLiveSince[key] = now
				break
			}
			if now.Sub(since) >= d.Debounce {
				decision.Action = ActionHibernate
			}
		}

		decisions = append(decisions, decision)
	}

	if d.MaxTeardowns > 0 && teardowns > d.MaxTeardowns {
		// Too many at once to be somebody closing finished work. Downgrade
		// every teardown to none; the count is reported by the caller.
		for i := range decisions {
			if decisions[i].Action == ActionTeardown {
				decisions[i].Action = ActionNone
			}
		}
	}

	return decisions
}

// Counts is how many threads a worktree currently carries.
type Counts struct {
	Live     int
	Archived int
}

// CountThreads groups a snapshot's threads by worktree path, so a display can
// show what is holding each worktree open without repeating the state machine.
func CountThreads(snapshot *t3.ShellSnapshot) map[string]Counts {
	live, archived := indexThreads(snapshot)
	out := make(map[string]Counts, len(live)+len(archived))
	for path, ids := range live {
		counts := out[path]
		counts.Live = len(ids)
		out[path] = counts
	}
	for path, ids := range archived {
		counts := out[path]
		counts.Archived = len(ids)
		out[path] = counts
	}
	return out
}

// NormalizePath exposes the path form CountThreads keys by, so callers can look
// up a worktree without guessing at trailing separators.
func NormalizePath(path string) string { return normalize(path) }

// TeardownCount reports how many decisions would remove a tree, so the caller
// can tell a suppressed batch from a quiet tick.
func TeardownCount(decisions []Decision) int {
	n := 0
	for _, decision := range decisions {
		if decision.Action == ActionTeardown {
			n++
		}
	}
	return n
}

// indexThreads groups thread ids by worktree path, split by whether the thread
// is still live. Threads bound to no worktree — the ones working directly in a
// repository checkout — are ignored.
func indexThreads(snapshot *t3.ShellSnapshot) (live, archived map[string][]string) {
	live = make(map[string][]string)
	archived = make(map[string][]string)
	if snapshot == nil {
		return live, archived
	}
	for _, thread := range snapshot.Threads {
		path := thread.Worktree()
		if path == "" {
			continue
		}
		if thread.DeletedAt != nil {
			continue
		}
		key := normalize(path)
		if thread.ArchivedAt != nil {
			archived[key] = append(archived[key], thread.ID)
			continue
		}
		live[key] = append(live[key], thread.ID)
	}
	return live, archived
}

func normalize(path string) string {
	return strings.TrimRight(filepath.Clean(path), string(filepath.Separator))
}
