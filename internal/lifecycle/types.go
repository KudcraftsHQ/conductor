// Package lifecycle keeps one inventory of every git worktree on the machine
// and decides, from facts alone, what may be done with each: leave it, slim it
// (delete regenerable dependency and build caches), retire it (record it, keep
// its commits under refs/graveyard/<id>, remove the directory), or flag it as
// needing Hammas because it holds unsaved work.
//
// The layers, each in its own file:
//
//	discover.go   every worktree on the machine (git + T3 root + .claude/worktrees)
//	facts*.go     what is true about one worktree right now (read-only)
//	classify.go   PURE: Facts + Settings + now → Plan. All thresholds and safety rules.
//	settings.go   conductor.json lifecycle config resolved per project; pause file
//	inventory.go  ~/.conductor/lifecycle.json, what the sweeper remembers between passes
//	record.go     ~/.conductor/graveyard/<id>/record.json, one per retired worktree
//	graveyard.go  refs/graveyard/<id> in the main repo
//	log.go        JSONL action log
//
// Executors (slim, retire, revive, hydrate, notify, sweep) are stubs filled in
// by phase 2; see docs/WORKTREE-LIFECYCLE.md "For implementers".
package lifecycle

import (
	"time"
)

// State is where a worktree sits in the lifecycle.
type State string

const (
	// StateActive: something is using it. Nothing happens.
	StateActive State = "active"
	// StateSlim: idle long enough that regenerable dirs can go (or already went).
	StateSlim State = "slim"
	// StateRetireCandidate: may be removed entirely; its record and graveyard
	// ref bring it back.
	StateRetireCandidate State = "retired-candidate"
	// StateNeedsYou: idle with unsaved work (uncommitted, untracked, unpushed).
	StateNeedsYou State = "needs-you"
	// StateUntouchable: repo roots, conductor root worktrees, missing
	// directories, anything git cannot read. Never acted on.
	StateUntouchable State = "untouchable"
	// StateRetired: only a record remains (listed by `wt ls --all`).
	StateRetired State = "retired"
)

// Action is what a sweep would do to a worktree now.
type Action string

const (
	ActionNone Action = "none"
	// ActionSlim deletes allowlisted, gitignored, regenerable dirs.
	ActionSlim Action = "slim"
	// ActionSnapshot commits the dirty state to refs/graveyard/<id> (nothing
	// touched in the worktree) and notifies Hammas once, with the retire date.
	ActionSnapshot Action = "needs-you-snapshot"
	// ActionRetire writes the record + graveyard ref and removes the worktree.
	// When Facts.HasUnsavedWork() the executor takes a fresh snapshot first.
	ActionRetire Action = "retire"
)

// Plan is classify's verdict for one worktree.
type Plan struct {
	State  State  `json:"state"`
	Action Action `json:"action"`
	// Reason is one human line: why this state, and why this action (or why not).
	Reason string `json:"reason"`
	// Blockers lists what stopped a stronger action, e.g. "PR #12 open",
	// "no tracked lockfile". Informational.
	Blockers []string `json:"blockers,omitempty"`
	// RetireAt is when a needs-you worktree will be retired from its snapshot
	// (zero otherwise). Notifications quote it.
	RetireAt time.Time `json:"retireAt,omitempty"`
	// Forced reports that --force bypassed a timing threshold to reach Action.
	Forced bool `json:"forced,omitempty"`
}

// Source says how discovery found a worktree; a path can have several.
type Source string

const (
	SourceGitList   Source = "git"       // `git worktree list` of a known repo
	SourceT3Root    Source = "t3"        // ~/.t3/worktrees/*/*
	SourceClaude    Source = "claude"    // <repo>/.claude/worktrees/*
	SourceConductor Source = "conductor" // registered in conductor.json
	SourceThread    Source = "thread"    // a T3 thread's worktreePath
)

// Candidate is one discovered worktree before facts are gathered.
type Candidate struct {
	Path     string   `json:"path"`     // realpath
	RepoRoot string   `json:"repoRoot"` // realpath of the main worktree ("" if unknown yet)
	Sources  []Source `json:"sources"`
	// Missing: git lists it (prunable) or a thread names it, but the directory is gone.
	Missing bool `json:"missing,omitempty"`
}

// Facts is everything classify needs to know about one worktree, gathered
// read-only. Zero values mean "none" unless the matching *Err/*Unknown field
// says the fact could not be read; classify treats unknown as unsafe.
type Facts struct {
	Candidate

	// --- identity ---
	IsGitWorktree bool `json:"isGitWorktree"` // has a .git file/dir and git answers
	// IsRepoRoot: this is the repo's main worktree (never touched).
	IsRepoRoot bool   `json:"isRepoRoot,omitempty"`
	Remote     string `json:"remote,omitempty"` // "owner/name" of origin on GitHub, "" if none
	Branch     string `json:"branch,omitempty"` // "" when detached
	Head       string `json:"head,omitempty"`   // full SHA
	// DefaultBranch is origin's default ("main"/"master"), "" if unknown.
	DefaultBranch string `json:"defaultBranch,omitempty"`
	// HeadOnDefault: HEAD is reachable from origin/<DefaultBranch>.
	HeadOnDefault bool   `json:"headOnDefault,omitempty"`
	GitErr        string `json:"gitErr,omitempty"`

	// --- unsaved work ---
	Dirty     int `json:"dirty"`     // tracked files changed (staged or not), conflicts
	Untracked int `json:"untracked"` // untracked, non-ignored entries (git status -unormal)
	Unpushed  int `json:"unpushed"`  // commits on HEAD not on any remote ref

	// --- PRs (gh, one cached list per repo) ---
	PRs   []PR   `json:"prs,omitempty"` // every PR whose head is Branch
	PRErr string `json:"prErr,omitempty"`

	// --- T3 threads bound to Path (any state, deleted ones included) ---
	Threads   []ThreadFact `json:"threads,omitempty"`
	ThreadErr string       `json:"threadErr,omitempty"`

	// --- processes ---
	// ProcsInside: processes whose cwd is inside Path, excluding conductor's
	// own dev-server tmux windows (those are in DevWindows).
	ProcsInside []Proc `json:"procsInside,omitempty"`
	// DevWindows: tmux windows with a pane inside Path. The retire executor
	// closes these before re-checking ProcsInside.
	DevWindows []TmuxWindow `json:"devWindows,omitempty"`
	// ProcsUnknown: the process table could not be read (non-Linux, error).
	ProcsUnknown bool `json:"procsUnknown,omitempty"`

	// --- activity ---
	LastCommit    time.Time `json:"lastCommit,omitempty"`
	LastFileTouch time.Time `json:"lastFileTouch,omitempty"` // newest mtime 2 levels deep, minus node_modules/.git

	// --- dependencies ---
	Lockfile        string `json:"lockfile,omitempty"` // e.g. "bun.lock"; "" if none
	LockfileTracked bool   `json:"lockfileTracked,omitempty"`
	// SlimTargets: allowlisted dirs present (relative paths), not yet confirmed
	// ignored; the slim executor runs git check-ignore on each.
	SlimTargets []string `json:"slimTargets,omitempty"`

	// --- conductor ---
	Conductor *ConductorRef `json:"conductor,omitempty"`
	// Entry is what the inventory remembers from earlier passes (nil if new).
	Entry *Entry `json:"entry,omitempty"`

	// --- size (only when asked; -1 = not measured) ---
	OwnBytes int64 `json:"ownBytes"`
}

// PR is one pull request whose head branch matches the worktree's branch.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"` // OPEN | MERGED | CLOSED (gh's casing)
}

// ThreadFact is one T3 thread bound to the worktree, read from T3's state DB.
type ThreadFact struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Subagent bool   `json:"subagent,omitempty"` // provider-spawned, shares the parent's tree
	// Settled follows T3: settledOverride "settled" → true, "active" → false,
	// otherwise settledAt set and not older than unsettledAt.
	Settled       bool      `json:"settled"`
	Pinned        bool      `json:"pinned,omitempty"`
	ArchivedAt    time.Time `json:"archivedAt,omitempty"`
	DeletedAt     time.Time `json:"deletedAt,omitempty"`
	SettledAt     time.Time `json:"settledAt,omitempty"`
	UnsettledAt   time.Time `json:"unsettledAt,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt,omitempty"`
	LastVisitedAt time.Time `json:"lastVisitedAt,omitempty"`
}

// Live reports the thread is neither archived nor deleted.
func (t ThreadFact) Live() bool { return t.ArchivedAt.IsZero() && t.DeletedAt.IsZero() }

// Working reports the thread holds the worktree open: live, and unsettled or pinned.
func (t ThreadFact) Working() bool { return t.Live() && (t.Pinned || !t.Settled) }

// Proc is a process with its cwd inside a worktree.
type Proc struct {
	PID  int    `json:"pid"`
	Comm string `json:"comm"`
	Cwd  string `json:"cwd"`
}

// TmuxWindow is a tmux window with at least one pane inside a worktree.
type TmuxWindow struct {
	ID       string `json:"id"` // @12
	Name     string `json:"name"`
	PanePIDs []int  `json:"panePids"`
}

// ConductorRef ties a worktree to its conductor.json entry.
type ConductorRef struct {
	Project     string `json:"project"`
	Name        string `json:"name"` // the city name
	ProjectPath string `json:"projectPath"`
	IsRoot      bool   `json:"isRoot,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	Hibernated  bool   `json:"hibernated,omitempty"`
	HasPorts    bool   `json:"hasPorts,omitempty"`
	HasDatabase bool   `json:"hasDatabase,omitempty"`
}

// HasUnsavedWork reports anything git alone could not bring back.
func (f *Facts) HasUnsavedWork() bool { return f.Dirty > 0 || f.Untracked > 0 || f.Unpushed > 0 }

// LiveThreads returns threads that are not archived or deleted.
func (f *Facts) LiveThreads() []ThreadFact {
	var out []ThreadFact
	for _, t := range f.Threads {
		if t.Live() {
			out = append(out, t)
		}
	}
	return out
}

// ThreadStatus summarises threads for display: working | settled | archived | none.
func (f *Facts) ThreadStatus() string {
	if len(f.Threads) == 0 {
		return "none"
	}
	anyLive, anyArchived := false, false
	for _, t := range f.Threads {
		if t.Working() {
			return "working"
		}
		if t.Live() {
			anyLive = true
		} else if t.DeletedAt.IsZero() {
			anyArchived = true
		}
	}
	switch {
	case anyLive:
		return "settled"
	case anyArchived:
		return "archived"
	}
	return "deleted"
}

// PRStatus summarises PRs for display and for classify: open | merged | closed | none.
// OPEN wins over everything, then MERGED, then CLOSED.
func (f *Facts) PRStatus() string {
	has := map[string]bool{}
	for _, p := range f.PRs {
		has[p.State] = true
	}
	switch {
	case has["OPEN"]:
		return "open"
	case has["MERGED"]:
		return "merged"
	case has["CLOSED"]:
		return "closed"
	}
	return "none"
}

// LastActivity is the newest of: any thread's updatedAt/lastVisitedAt/
// settledAt/unsettledAt/archivedAt, the last commit, and the newest file
// mtime two levels deep. Zero if nothing is known.
func (f *Facts) LastActivity() time.Time {
	latest := f.LastCommit
	bump := func(t time.Time) {
		if t.After(latest) {
			latest = t
		}
	}
	bump(f.LastFileTouch)
	for _, t := range f.Threads {
		if !t.DeletedAt.IsZero() {
			continue
		}
		bump(t.UpdatedAt)
		bump(t.LastVisitedAt)
		bump(t.SettledAt)
		bump(t.UnsettledAt)
		bump(t.ArchivedAt)
	}
	return latest
}
