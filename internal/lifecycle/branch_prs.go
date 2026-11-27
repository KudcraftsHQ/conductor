package lifecycle

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BranchFacts is what the T3 watcher needs to judge whether a worktree's work
// is finished: which branch it has checked out, the PRs whose head is that
// branch, and when the branch's own newest commit was made.
type BranchFacts struct {
	Branch string
	Remote string // owner/name on GitHub, "" if origin is not GitHub
	PRs    []PR
	PRErr  string
	// LastOwnCommit is the newest commit on HEAD that origin's default branch
	// does not have, zero when there is none. Pulling main into a merged
	// branch is therefore not new work; committing on it is.
	LastOwnCommit time.Time
}

// PRLookup reads BranchFacts the way `conductor wt ls` reads its PR column:
// the branch git has checked out, origin's GitHub repo, and that repo's PR list
// from gh, cached on disk and shared with wt ls. One lookup makes at most one
// gh call per repository, however many worktrees ask; make a new one for the
// next round.
type PRLookup struct {
	prs *prCache
}

// NewPRLookup returns a lookup over the shared PR cache.
func NewPRLookup() *PRLookup {
	return &PRLookup{prs: &prCache{dir: filepath.Join(StateDir(), "cache")}}
}

// Worktree gathers the facts for the worktree at path. Fields it could not
// read are left empty, which callers treat as "no PR known".
func (l *PRLookup) Worktree(ctx context.Context, path string) BranchFacts {
	var f BranchFacts
	if b, err := fxGit(ctx, path, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		f.Branch = strings.TrimSpace(b)
	}
	logArgs := []string{"log", "-1", "--format=%ct", "HEAD"}
	if def, err := fxGit(ctx, path, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil && strings.TrimSpace(def) != "" {
		logArgs = append(logArgs, "--not", strings.TrimSpace(def))
	}
	if ct, err := fxGit(ctx, path, logArgs...); err == nil && strings.TrimSpace(ct) != "" {
		if sec, err := strconv.ParseInt(strings.TrimSpace(ct), 10, 64); err == nil {
			f.LastOwnCommit = time.Unix(sec, 0)
		}
	}
	if url, err := fxGit(ctx, path, "remote", "get-url", "origin"); err == nil {
		if m := githubRemote.FindStringSubmatch(strings.TrimSpace(url)); m != nil {
			f.Remote = m[1] + "/" + m[2]
		}
	}
	if f.Remote != "" && f.Branch != "" {
		f.PRs, f.PRErr = l.prs.forBranch(ctx, f.Remote, f.Branch)
	}
	return f
}

// FinishedPR is the PR that finished a branch's work: when PRStatusOf says
// merged or closed, the one of that state closed most recently. ok is false
// while any PR on the branch is open, or there is none.
func FinishedPR(prs []PR) (pr PR, ok bool) {
	want := ""
	switch PRStatusOf(prs) {
	case "merged":
		want = "MERGED"
	case "closed":
		want = "CLOSED"
	default:
		return PR{}, false
	}
	for _, p := range prs {
		if p.State == want && (!ok || p.ClosedAt.After(pr.ClosedAt)) {
			pr, ok = p, true
		}
	}
	return pr, ok
}
