package t3

import (
	"context"
	"path/filepath"
	"strings"
)

// Candidate is one conductor worktree that T3 may be hosting.
//
// It carries the branch as well as the name because the dev server's tmux
// window is keyed by project and branch, not by the city the worktree is
// registered under.
type Candidate struct {
	Project      string
	Worktree     string
	Branch       string
	WorktreePath string
	// ChangeRequest is the state of this branch's pull request — "open",
	// "merged", "closed", "draft" — or "" when there is none. Supplied by the
	// caller because fetching it means shelling out to gh, which this package
	// stays clear of so it remains testable without a network.
	ChangeRequest string
}

// State is what T3's threads say should be true of a worktree.
type State int

const (
	// StateActive: at least one thread is still working here. The dev server
	// should be running.
	StateActive State = iota
	// StateSettled: every thread bound to this worktree is settled, whether by an
	// explicit ruling or because its PR merged. The work is finished but the
	// worktree is deliberately still around. Stop the dev server; leave the
	// worktree, its branch and its database alone.
	StateSettled
	// StateDrifted: no thread references this worktree at all any more, because
	// they were archived or deleted in T3's UI. Nothing is coming back. The
	// worktree, its database, its tunnel and its ports are all still allocated
	// and can be archived.
	StateDrifted
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateSettled:
		return "settled"
	case StateDrifted:
		return "drifted"
	}
	return "unknown"
}

// Assessment is a worktree and what should be done about it.
type Assessment struct {
	Candidate
	State State
	// Threads is how many non-archived threads are bound to the worktree, and
	// Settled how many of those are finished. Both are reported so a user can
	// see why a worktree was classified the way it was before acting on it.
	Threads int
	Settled int
}

// Classify sorts T3-hosted worktrees into active, settled and drifted.
//
// Only worktrees carrying a marker file are considered. A worktree created
// under tmux or herdr never had a thread, so treating its absence as drift
// would flag every legacy worktree at once — and archiving on drift would then
// destroy all of them.
//
// A worktree is only settled when *every* thread on it is. T3 reuses a worktree
// when a new thread picks a branch that already has one, so finishing one piece
// of work in a tree says nothing about the others still running there; stopping
// the shared dev server underneath them would break every one.
//
// It deliberately only *reports*. Acting on a classification means stopping a
// server or archiving a worktree, and the caller — not this package — decides
// which of those it is willing to do.
//
// Worktrees are supplied by the caller rather than read here so this stays
// independent of conductor's config and store packages, and therefore testable
// without either.
func (c *Client) Classify(ctx context.Context, worktrees []Candidate) ([]Assessment, error) {
	hosted := make([]Candidate, 0, len(worktrees))
	for _, worktree := range worktrees {
		if HasMarker(worktree.WorktreePath) {
			hosted = append(hosted, worktree)
		}
	}
	if len(hosted) == 0 {
		return nil, nil
	}

	snapshot, err := c.Shell(ctx)
	if err != nil {
		return nil, err
	}
	return ClassifySnapshot(snapshot, hosted), nil
}

// ClassifySnapshot is the decision itself, separated from fetching so it can be
// tested against a snapshot without a server. It assumes its callers have
// already filtered to T3-hosted worktrees.
func ClassifySnapshot(snapshot *ShellSnapshot, hosted []Candidate) []Assessment {
	// Index the caller's PR states by worktree path, so the thread loop below
	// can resolve settled-ness the way T3's sidebar does.
	crByPath := make(map[string]string, len(hosted))
	for _, w := range hosted {
		crByPath[normalizePath(w.WorktreePath)] = w.ChangeRequest
	}

	// Count live threads per worktree, and how many of them are settled. An
	// archived thread is not counted at all: it has released the worktree, which
	// is exactly what makes a worktree drifted once they all have.
	type tally struct{ total, settled int }
	byPath := make(map[string]*tally, len(snapshot.Threads))
	for _, thread := range snapshot.Threads {
		if thread.Archived() || thread.Worktree() == "" {
			continue
		}
		path := normalizePath(thread.Worktree())
		t, ok := byPath[path]
		if !ok {
			t = &tally{}
			byPath[path] = t
		}
		t.total++
		if thread.EffectiveSettled(SettleOptions{
			ChangeRequest:     crByPath[path],
			AutoSettleOnMerge: true,
		}) {
			t.settled++
		}
	}

	out := make([]Assessment, 0, len(hosted))
	for _, worktree := range hosted {
		assessment := Assessment{Candidate: worktree}
		switch t := byPath[normalizePath(worktree.WorktreePath)]; {
		case t == nil || t.total == 0:
			assessment.State = StateDrifted
		case t.settled == t.total:
			assessment.State = StateSettled
			assessment.Threads, assessment.Settled = t.total, t.settled
		default:
			assessment.State = StateActive
			assessment.Threads, assessment.Settled = t.total, t.settled
		}
		out = append(out, assessment)
	}
	return out
}

// Reconcile reports the T3-hosted worktrees that no longer have a thread.
//
// Retained as the narrow question `conductor t3 reconcile --archive` asks:
// which worktrees can be destroyed. Callers that also want to manage dev
// servers want Classify.
func (c *Client) Reconcile(ctx context.Context, worktrees []Candidate) ([]Candidate, error) {
	assessments, err := c.Classify(ctx, worktrees)
	if err != nil {
		return nil, err
	}
	var drifted []Candidate
	for _, a := range assessments {
		if a.State == StateDrifted {
			drifted = append(drifted, a.Candidate)
		}
	}
	return drifted, nil
}

// InState returns the assessments in the given state.
func InState(assessments []Assessment, state State) []Assessment {
	var out []Assessment
	for _, a := range assessments {
		if a.State == state {
			out = append(out, a)
		}
	}
	return out
}

// CountHosted returns how many of the given worktrees T3 is hosting.
func CountHosted(worktrees []Candidate) int {
	n := 0
	for _, worktree := range worktrees {
		if HasMarker(worktree.WorktreePath) {
			n++
		}
	}
	return n
}

// normalizePath makes paths comparable across the two systems.
func normalizePath(path string) string {
	return strings.TrimRight(filepath.Clean(path), string(filepath.Separator))
}
