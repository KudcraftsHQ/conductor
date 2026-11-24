package lifecycle

import (
	"context"
	"path/filepath"
	"time"
)

// PassOptions select what one planning pass looks at.
type PassOptions struct {
	Gather GatherOptions
	Force  bool
	// Paths restricts the pass to these worktrees (realpath'd); empty = all.
	Paths []string
}

// Pass is one read-only planning pass: every worktree, its facts and its plan.
// Sweep (phase 2) executes a Pass; `wt ls` and `wt gc --dry-run` print one.
type Pass struct {
	At        time.Time
	Paused    bool
	PauseInfo PauseInfo
	Facts     []*Facts
	Plans     []Plan // Plans[i] is for Facts[i]
	Inventory *Inventory
}

// PlanPass discovers, gathers and classifies. It writes nothing.
func PlanPass(ctx context.Context, env *Env, o PassOptions) (*Pass, error) {
	inv, err := env.LoadInventory()
	if err != nil {
		return nil, err
	}
	cands, err := Discover(ctx, env)
	if err != nil {
		return nil, err
	}
	if len(o.Paths) > 0 {
		want := map[string]bool{}
		for _, p := range o.Paths {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			want[realpath(p)] = true
		}
		kept := cands[:0]
		for _, c := range cands {
			if want[c.Path] {
				kept = append(kept, c)
			}
		}
		cands = kept
	}
	paused, info := env.Paused()
	now := env.Now()
	facts := Gather(ctx, env, cands, inv, o.Gather)
	plans := make([]Plan, len(facts))
	for i, f := range facts {
		plans[i] = Classify(f, env.SettingsFor(f.RepoRoot), now, ClassifyOptions{Paused: paused, Force: o.Force})
	}
	return &Pass{At: now, Paused: paused, PauseInfo: info, Facts: facts, Plans: plans, Inventory: inv}, nil
}

// Counts returns how many worktrees are in each state.
func (p *Pass) Counts() map[State]int {
	out := map[State]int{}
	for _, pl := range p.Plans {
		out[pl.State]++
	}
	return out
}

// ActionCounts returns how many worktrees each action would touch.
func (p *Pass) ActionCounts() map[Action]int {
	out := map[Action]int{}
	for _, pl := range p.Plans {
		if pl.Action != ActionNone {
			out[pl.Action]++
		}
	}
	return out
}
