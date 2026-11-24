package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Discover lists every git worktree on the machine, from git itself rather
// than from conductor.json (most were never registered there).
//
// Repo roots come from conductor's projects, plus the repos behind every T3
// worktree dir, conductor-registered worktree and T3 thread worktreePath. Each
// root's `git worktree list` is the core list; the T3 root glob, each root's
// .claude/worktrees and conductor's registered paths are added on top. The
// union is deduped by realpath. Repo roots (main worktrees, conductor project
// roots) are never candidates.
func Discover(ctx context.Context, env *Env) ([]Candidate, error) {
	type seed struct {
		path   string
		source Source
	}
	var seeds []seed

	// Directories that may be worktrees, each needing its repo root resolved.
	if env.T3Root != "" {
		dirs, _ := filepath.Glob(filepath.Join(env.T3Root, "*", "*"))
		for _, d := range dirs {
			seeds = append(seeds, seed{d, SourceT3Root})
		}
	}
	projectRoots := map[string]bool{}
	if env.Config != nil {
		for _, p := range env.Config.Projects {
			if p == nil {
				continue
			}
			if p.Path != "" {
				projectRoots[realpath(p.Path)] = true
			}
			for _, w := range p.Worktrees {
				if w != nil && w.Path != "" {
					seeds = append(seeds, seed{w.Path, SourceConductor})
				}
			}
		}
	}
	if threads, err := loadThreads(ctx, env.T3StateDB); err == nil {
		for p := range threads {
			seeds = append(seeds, seed{p, SourceThread})
		}
	}

	// Resolve each seed (only real git dirs) to its repo root, in parallel.
	type resolved struct {
		path, root string
		source     Source
	}
	results := make([]resolved, len(seeds))
	parallel(len(seeds), 8, func(i int) {
		s := seeds[i]
		p := realpath(s.path)
		if !isDir(p) || !exists(filepath.Join(p, ".git")) {
			return
		}
		root, err := repoRootOf(ctx, p)
		if err != nil {
			return
		}
		results[i] = resolved{p, root, s.source}
	})

	roots := map[string]bool{}
	for r := range projectRoots {
		if isDir(r) {
			roots[r] = true
		}
	}
	for _, r := range results {
		if r.root != "" {
			roots[r.root] = true
		}
	}

	byPath := map[string]*Candidate{}
	add := func(path, root string, src Source, missing bool) {
		c, ok := byPath[path]
		if !ok {
			c = &Candidate{Path: path, RepoRoot: root, Missing: missing}
			byPath[path] = c
		}
		if c.RepoRoot == "" {
			c.RepoRoot = root
		}
		if !missing {
			c.Missing = false
		}
		for _, s := range c.Sources {
			if s == src {
				return
			}
		}
		c.Sources = append(c.Sources, src)
	}

	// git worktree list per root, in parallel.
	rootList := make([]string, 0, len(roots))
	for r := range roots {
		rootList = append(rootList, r)
	}
	sort.Strings(rootList)
	type listed struct {
		path    string
		missing bool
	}
	lists := make([][]listed, len(rootList))
	mains := make([]string, len(rootList))
	parallel(len(rootList), 8, func(i int) {
		out, err := fxGit(ctx, rootList[i], "worktree", "list", "--porcelain")
		if err != nil {
			return
		}
		for j, block := range strings.Split(out, "\n\n") {
			var wt listed
			for _, line := range strings.Split(block, "\n") {
				switch {
				case strings.HasPrefix(line, "worktree "):
					wt.path = strings.TrimPrefix(line, "worktree ")
				case line == "prunable" || strings.HasPrefix(line, "prunable "):
					wt.missing = true
				}
			}
			if wt.path == "" {
				continue
			}
			if j == 0 {
				mains[i] = realpath(wt.path)
				continue
			}
			if !wt.missing {
				wt.path = realpath(wt.path)
				if !isDir(wt.path) {
					wt.missing = true
				}
			} else {
				wt.path = filepath.Clean(wt.path)
			}
			lists[i] = append(lists[i], wt)
		}
	})

	excluded := map[string]bool{}
	for r := range projectRoots {
		excluded[r] = true
	}
	for i, r := range rootList {
		excluded[r] = true
		if mains[i] != "" {
			excluded[mains[i]] = true
		}
		for _, wt := range lists[i] {
			add(wt.path, r, SourceGitList, wt.missing)
		}
		claude, _ := filepath.Glob(filepath.Join(r, ".claude", "worktrees", "*"))
		for _, d := range claude {
			p := realpath(d)
			if isDir(p) && exists(filepath.Join(p, ".git")) {
				add(p, r, SourceClaude, false)
			}
		}
	}
	for _, r := range results {
		if r.path != "" && r.source != SourceThread {
			add(r.path, r.root, r.source, false)
		}
	}
	// A thread's worktreePath adds a source tag to a worktree already found;
	// it never creates a candidate on its own.
	for _, r := range results {
		if r.path != "" && r.source == SourceThread {
			if _, ok := byPath[r.path]; ok {
				add(r.path, r.root, SourceThread, false)
			}
		}
	}

	out := make([]Candidate, 0, len(byPath))
	for p, c := range byPath {
		if excluded[p] {
			continue
		}
		if !c.Missing && !isDir(p) {
			continue
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// parallel runs fn(0..n-1) on at most workers goroutines.
func parallel(n, workers int, fn func(i int)) {
	if workers <= 0 {
		workers = 8
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
