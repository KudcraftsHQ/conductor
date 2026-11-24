package lifecycle

import (
	"context"
	"runtime"
)

// GatherOptions tunes a facts pass.
type GatherOptions struct {
	// Sizes measures OwnBytes per worktree (slow: a full walk of each tree).
	Sizes bool
	// Workers bounds concurrent worktrees; 0 means 8.
	Workers int
	// NoPRCache bypasses the 15-minute on-disk gh PR list cache.
	NoPRCache bool
}

// Gather reads the facts for every candidate, read-only. It returns one Facts
// per candidate in the same order, never nil; anything that could not be read
// is recorded on the fact (GitErr, PRErr, ThreadErr, ProcsUnknown) rather than
// failing the pass. Thread and process tables are read once and shared.
func Gather(ctx context.Context, env *Env, cands []Candidate, inv *Inventory, opts GatherOptions) []*Facts {
	threads, threadErr := loadThreads(ctx, env.T3StateDB)
	procs := readProcTable(ctx)
	if runtime.GOOS != "linux" {
		procs.unknown = true
	}
	repos := &repoCache{}
	prs := &prCache{dir: env.CacheDir(), noCache: opts.NoPRCache}

	// conductor.json entries by realpath.
	registered := map[string]*ConductorRef{}
	if env.Config != nil {
		for pname, p := range env.Config.Projects {
			if p == nil {
				continue
			}
			for wname, w := range p.Worktrees {
				if w == nil || w.Path == "" {
					continue
				}
				registered[realpath(w.Path)] = &ConductorRef{
					Project:     pname,
					Name:        wname,
					ProjectPath: p.Path,
					IsRoot:      w.IsRoot,
					Archived:    w.Archived,
					Hibernated:  w.Hibernated,
					HasPorts:    len(w.Ports) > 0,
					HasDatabase: w.DatabaseName != "",
				}
			}
		}
	}

	out := make([]*Facts, len(cands))
	parallel(len(cands), opts.Workers, func(i int) {
		f := &Facts{Candidate: cands[i], OwnBytes: -1}
		out[i] = f
		f.Entry = inv.Get(f.Path)
		f.Conductor = registered[f.Path]
		if threadErr != nil {
			f.ThreadErr = threadErr.Error()
		} else {
			f.Threads = threads[f.Path]
		}
		if f.Missing {
			return
		}
		procs.inspect(f)
		gatherGit(ctx, f, repos)
		f.LastFileTouch = lastTouch(f.Path)
		f.SlimTargets = slimTargets(f.Path, env.SettingsFor(f.RepoRoot).SlimAllowlist)
		if f.Remote != "" && f.Branch != "" {
			f.PRs, f.PRErr = prs.forBranch(ctx, f.Remote, f.Branch)
		}
		if opts.Sizes {
			if n, err := OwnBytes(f.Path); err == nil {
				f.OwnBytes = n
			}
		}
	})
	return out
}
