package t3watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/lifecycle"
)

// PRRefreshInterval is how often each worktree's branch, last commit and PRs
// are read again. The PR lists themselves come from the cache `conductor wt
// ls` shares, so gh is asked at most once per repository per that cache's TTL.
const PRRefreshInterval = time.Minute

// prFacts is the watcher's latest read of every candidate worktree's PR facts.
// It is refreshed off the tick, so a slow gh never delays a pass.
type prFacts struct {
	mu      sync.Mutex
	byPath  map[string]lifecycle.BranchFacts
	at      time.Time
	running bool
	// held is the PR hold last logged per worktree, so engaging and lifting
	// are each said once.
	held map[string]string
}

// prCandidate is a worktree whose dev server the PR hold could apply to.
func prCandidate(w Worktree) bool {
	return !w.IsRoot && !w.Archived && !w.Hibernated && !w.Missing && w.Path != ""
}

// annotatePRs fills each candidate's PR, LastCommit and DevRestartedAt.
//
// The first pass reads synchronously, so `conductor t3 watch once` and a
// freshly started daemon decide on real facts; later passes use the last read
// and start the next one in the background when it is due.
func (w *Watcher) annotatePRs(ctx context.Context, worktrees []Worktree) {
	var paths []string
	for _, wt := range worktrees {
		if prCandidate(wt) {
			paths = append(paths, wt.Path)
		}
	}

	p := &w.prs
	p.mu.Lock()
	first := p.at.IsZero() && !p.running
	due := !p.running && time.Since(p.at) >= PRRefreshInterval
	if due {
		p.running = true
	}
	p.mu.Unlock()
	if due {
		if first {
			w.refreshPRs(ctx, paths)
		} else {
			go w.refreshPRs(ctx, paths)
		}
	}

	p.mu.Lock()
	facts := p.byPath
	p.mu.Unlock()
	for i := range worktrees {
		wt := &worktrees[i]
		if !prCandidate(*wt) {
			continue
		}
		wt.DevRestartedAt = devRestartedAt(wt.Path)
		f, ok := facts[normalize(wt.Path)]
		if !ok {
			continue
		}
		wt.LastCommit = f.LastOwnCommit
		if pr, ok := lifecycle.FinishedPR(f.PRs); ok {
			wt.PR = &FinishedPR{Number: pr.Number, State: strings.ToLower(pr.State), At: pr.ClosedAt}
		}
	}
	w.logPRHolds(worktrees)
}

func (w *Watcher) refreshPRs(ctx context.Context, paths []string) {
	lookup := lifecycle.NewPRLookup()
	out := make(map[string]lifecycle.BranchFacts, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, path := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer wg.Done()
			defer func() { <-sem }()
			f := lookup.Worktree(ctx, path)
			mu.Lock()
			out[normalize(path)] = f
			mu.Unlock()
		}(path)
	}
	wg.Wait()

	p := &w.prs
	p.mu.Lock()
	p.byPath, p.at, p.running = out, time.Now(), false
	p.mu.Unlock()
}

// logPRHolds says when a worktree's PR hold engages and when it lifts.
func (w *Watcher) logPRHolds(worktrees []Worktree) {
	p := &w.prs
	if p.held == nil {
		p.held = make(map[string]string)
	}
	for _, wt := range worktrees {
		if !prCandidate(wt) {
			continue // Hibernated or gone: nothing to say until it is back.
		}
		key, hold := wt.Key(), wt.PRHold()
		switch was := p.held[key]; {
		case hold != "" && hold != was:
			w.logf("%s: %s — its dev server stays stopped until new work (a commit, a new PR, or 'conductor t3 dev restart')",
				key, hold)
			p.held[key] = hold
		case hold == "" && was != "":
			w.logf("%s: new work since %s — its dev server follows its threads again", key, was)
			delete(p.held, key)
		}
	}
}

// MarkDevRestart records that somebody asked for the worktree's dev server by
// hand, which lifts its PR hold. It must be written before the server is
// started, or the watcher can stop it again in between.
func MarkDevRestart(path string) error {
	file := devRestartFile(path)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(normalize(path)+"\n"), 0o644)
}

func devRestartedAt(path string) time.Time {
	st, err := os.Stat(devRestartFile(path))
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func devRestartFile(path string) string {
	sum := sha256.Sum256([]byte(normalize(path)))
	return filepath.Join(lifecycle.StateDir(), "dev-restarts", hex.EncodeToString(sum[:8]))
}
