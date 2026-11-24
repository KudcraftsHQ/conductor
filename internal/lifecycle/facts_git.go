package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// lockfiles in preference order; the first tracked one wins.
var lockfiles = []string{"bun.lock", "bun.lockb", "package-lock.json", "yarn.lock", "pnpm-lock.yaml"}

// repoInfo is what is true of a whole repo, read once per pass.
type repoInfo struct {
	remote        string // owner/name on GitHub
	defaultBranch string
}

type repoCache struct {
	mu    sync.Mutex
	repos map[string]*repoOnce
}

type repoOnce struct {
	once sync.Once
	info repoInfo
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`)

func (c *repoCache) get(ctx context.Context, root string) repoInfo {
	c.mu.Lock()
	if c.repos == nil {
		c.repos = map[string]*repoOnce{}
	}
	r, ok := c.repos[root]
	if !ok {
		r = &repoOnce{}
		c.repos[root] = r
	}
	c.mu.Unlock()
	r.once.Do(func() {
		if url, err := fxGit(ctx, root, "remote", "get-url", "origin"); err == nil {
			if m := githubRemote.FindStringSubmatch(url); m != nil {
				r.info.remote = m[1] + "/" + m[2]
			}
		}
		if ref, err := fxGit(ctx, root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
			r.info.defaultBranch = strings.TrimPrefix(ref, "origin/")
		}
	})
	return r.info
}

// gatherGit fills identity, unsaved-work, activity and lockfile facts.
func gatherGit(ctx context.Context, f *Facts, repos *repoCache) {
	status, err := fxGit(ctx, f.Path, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		f.GitErr = err.Error()
		return
	}
	f.IsGitWorktree = true
	f.IsRepoRoot = f.RepoRoot != "" && f.Path == f.RepoRoot
	for _, line := range strings.Split(status, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			if oid := strings.TrimPrefix(line, "# branch.oid "); oid != "(initial)" {
				f.Head = oid
			}
		case strings.HasPrefix(line, "# branch.head "):
			if b := strings.TrimPrefix(line, "# branch.head "); b != "(detached)" {
				f.Branch = b
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "), strings.HasPrefix(line, "u "):
			f.Dirty++
		case strings.HasPrefix(line, "? "):
			f.Untracked++
		}
	}

	if f.Head != "" {
		if n, err := fxGit(ctx, f.Path, "rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil {
			f.Unpushed, _ = strconv.Atoi(n)
		} else {
			f.GitErr = err.Error()
		}
		if ct, err := fxGit(ctx, f.Path, "log", "-1", "--format=%ct"); err == nil {
			if sec, err := strconv.ParseInt(ct, 10, 64); err == nil {
				f.LastCommit = time.Unix(sec, 0)
			}
		}
	}

	if tracked, err := fxGit(ctx, f.Path, append([]string{"ls-files", "--"}, lockfiles...)...); err == nil && tracked != "" {
		have := map[string]bool{}
		for _, l := range strings.Split(tracked, "\n") {
			have[strings.TrimSpace(l)] = true
		}
		for _, lf := range lockfiles {
			if have[lf] {
				f.Lockfile, f.LockfileTracked = lf, true
				break
			}
		}
	}
	if f.Lockfile == "" {
		for _, lf := range lockfiles {
			if exists(filepath.Join(f.Path, lf)) {
				f.Lockfile = lf
				break
			}
		}
	}

	if f.RepoRoot != "" {
		info := repos.get(ctx, f.RepoRoot)
		f.Remote, f.DefaultBranch = info.remote, info.defaultBranch
	}
	if f.Head != "" && f.DefaultBranch != "" {
		_, err := fxGit(ctx, f.Path, "merge-base", "--is-ancestor", "HEAD", "origin/"+f.DefaultBranch)
		f.HeadOnDefault = err == nil
	}
}

// lastTouch is the newest mtime two levels deep, skipping node_modules and
// .git — the interim script's last_touch.
func lastTouch(path string) time.Time {
	var newest time.Time
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.Name() == "node_modules" || e.Name() == ".git" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			if depth < 2 && e.IsDir() {
				walk(filepath.Join(dir, e.Name()), depth+1)
			}
		}
	}
	walk(path, 1)
	return newest
}

// slimTargets lists allowlisted dirs present under path (relative), to depth
// 4, not descending into a match, .git or node_modules. Nothing in NeverSlim
// or named .env* is ever listed.
func slimTargets(path string, allow []string) []string {
	allowed := map[string]bool{}
	for _, a := range allow {
		allowed[a] = true
	}
	for _, n := range NeverSlim {
		delete(allowed, n)
	}
	var out []string
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		entries, err := os.ReadDir(filepath.Join(path, rel))
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || name == ".git" || strings.HasPrefix(name, ".env") {
				continue
			}
			r := filepath.Join(rel, name)
			if allowed[name] {
				out = append(out, r)
				continue
			}
			if name == "node_modules" || depth >= 4 {
				continue
			}
			walk(r, depth+1)
		}
	}
	walk("", 1)
	return out
}
