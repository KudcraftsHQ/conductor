package lifecycle

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ClassifyOptions are the switches a caller may flip. Neither can override a
// safety rule (docs/WORKTREE-LIFECYCLE.md "Safety rules").
type ClassifyOptions struct {
	// Paused: the pause file exists. State is still computed, Action is none.
	Paused bool
	// Force bypasses the timing thresholds (slimAfter, retireAfter,
	// needsYouAfter, abandonAfter) — never the safety rules.
	Force bool
}

// Classify is PURE: the same facts, settings and clock always give the same
// plan. Every threshold and safety rule of the lifecycle lives here; the
// executors trust the plan and re-check only what can change between the
// plan and the act (processes, HEAD, dirtiness).
//
// Order matters, and is the order of the safety rules:
//  1. untouchable — repo roots, conductor roots, missing dirs, unreadable facts
//  2. active — a working/pinned thread, a process inside, recent activity
//  3. unsaved work — slim, then needs-you (snapshot + notify), then retire at abandonAfter
//  4. clean — slim, then retire once the PR is merged/closed (never while one is OPEN)
func Classify(f *Facts, s Settings, now time.Time, o ClassifyOptions) Plan {
	p := classify(f, s, now, o.Force)
	if p.Action == ActionNone {
		return p
	}
	switch {
	case o.Paused:
		p.Blockers = append(p.Blockers, "paused (would "+string(p.Action)+")")
		p.Action = ActionNone
	case !s.Enabled:
		p.Blockers = append(p.Blockers, "lifecycle disabled (would "+string(p.Action)+")")
		p.Action = ActionNone
	}
	return p
}

func classify(f *Facts, s Settings, now time.Time, force bool) Plan {
	// --- 1. never touched ---
	switch {
	case f.Missing:
		return untouchable("directory missing (pruned); nothing on disk")
	case f.IsRepoRoot:
		return untouchable("repo root")
	case f.Conductor != nil && f.Conductor.IsRoot:
		return untouchable("conductor project root")
	case keptBy(s, f.Path) != "":
		return untouchable("kept by lifecycle.keep " + keptBy(s, f.Path))
	case !f.IsGitWorktree:
		return untouchable("not a readable git worktree: " + firstNonEmpty(f.GitErr, "no .git"))
	case f.GitErr != "":
		return untouchable("git state unknown: " + f.GitErr)
	case f.RepoRoot == "" || filepath.Clean(f.RepoRoot) == filepath.Clean(f.Path):
		return untouchable("repo root unknown")
	case f.ThreadErr != "":
		return untouchable("T3 threads unreadable: " + f.ThreadErr)
	case f.ProcsUnknown:
		return untouchable("process table unreadable")
	}

	// --- 2. active ---
	for _, t := range f.Threads {
		if t.Working() {
			what := "working"
			if t.Pinned {
				what = "pinned"
			}
			return Plan{State: StateActive, Action: ActionNone, Reason: fmt.Sprintf("thread %s %s", threadLabel(t), what)}
		}
	}
	if len(f.ProcsInside) > 0 {
		pr := f.ProcsInside[0]
		more := ""
		if n := len(f.ProcsInside) - 1; n > 0 {
			more = fmt.Sprintf(" +%d", n)
		}
		return Plan{State: StateActive, Action: ActionNone, Reason: fmt.Sprintf("in use by pid %d (%s)%s", pr.PID, pr.Comm, more)}
	}
	last := f.LastActivity()
	idle := now.Sub(last)
	if last.IsZero() {
		idle = 0 // nothing known: treat as just used
	}
	forced := false
	if idle < s.SlimAfter {
		if !force {
			return Plan{State: StateActive, Action: ActionNone, Reason: "last activity " + Human(idle) + " ago (slim after " + Human(s.SlimAfter) + ")"}
		}
		forced = true
	}
	idleStr := "idle " + Human(idle)

	slimAction, slimNote := slimVerdict(f)

	// --- 3. unsaved work: never retired until Hammas was told and abandonAfter passed ---
	if f.HasUnsavedWork() {
		work := GitSummary(f)
		if idle < s.NeedsYouAfter && !force {
			return withForced(Plan{
				State:  StateSlim,
				Action: slimAction,
				Reason: fmt.Sprintf("%s, %s kept; needs you after %s%s", idleStr, work, Human(s.NeedsYouAfter), slimNote),
			}, forced)
		}
		forced = forced || idle < s.NeedsYouAfter
		en := f.Entry
		retireAt := needsYouRetireAt(en, last, s, now)
		p := Plan{State: StateNeedsYou, RetireAt: retireAt}
		switch {
		case en == nil || en.NeedsYouNotifiedAt.IsZero():
			p.Action = ActionSnapshot
			p.Reason = fmt.Sprintf("%s, %s; snapshot + notify, retire on %s", idleStr, work, retireAt.Format("2006-01-02"))
		case now.Before(retireAt) && !force:
			p.Action = slimAction
			p.Reason = fmt.Sprintf("%s, %s; told %s, retire on %s%s", idleStr, work, en.NeedsYouNotifiedAt.Format("2006-01-02"), retireAt.Format("2006-01-02"), slimNote)
		case f.PRStatus() == "open":
			p.Action = ActionNone
			p.Reason = fmt.Sprintf("%s, %s; past retire date but a PR is open", idleStr, work)
			p.Blockers = append(p.Blockers, openPRs(f))
		case f.PRErr != "":
			p.Action = ActionNone
			p.Reason = fmt.Sprintf("%s, %s; past retire date, PR state unknown", idleStr, work)
			p.Blockers = append(p.Blockers, "PR state unknown: "+f.PRErr)
		default:
			forced = forced || now.Before(retireAt)
			p.State = StateRetireCandidate
			p.Action = ActionRetire
			p.RetireAt = time.Time{}
			p.Reason = fmt.Sprintf("abandoned: %s, %s; told %s, retire date %s passed — retire from a fresh snapshot",
				idleStr, work, en.NeedsYouNotifiedAt.Format("2006-01-02"), retireAt.Format("2006-01-02"))
		}
		return withForced(p, forced)
	}

	// --- 4. clean, nothing unpushed ---
	retireWhy, blocker := "", ""
	switch st := f.PRStatus(); {
	case st == "open":
		blocker = openPRs(f)
	case f.PRErr != "":
		blocker = "PR state unknown: " + f.PRErr
	case st == "merged" || st == "closed":
		retireWhy = fmt.Sprintf("PR #%d %s", prOf(f, strings.ToUpper(st)).Number, st)
	case f.HeadOnDefault && f.Branch != "":
		// No PR, but HEAD is already on origin's default branch: git loses
		// nothing either way (see docs, "Retire without a PR").
		retireWhy = "no PR, HEAD on origin/" + f.DefaultBranch
	case f.Branch == "":
		blocker = "detached HEAD, no PR"
	default:
		blocker = "no PR for " + f.Branch
	}
	if f.Head == "" && retireWhy != "" {
		retireWhy, blocker = "", "no commit at HEAD"
	}
	if retireWhy != "" {
		if idle >= s.RetireAfter || force {
			return withForced(Plan{
				State:  StateRetireCandidate,
				Action: ActionRetire,
				Reason: fmt.Sprintf("clean, %s, %s", retireWhy, idleStr),
			}, forced || idle < s.RetireAfter)
		}
		blocker = "retire after " + Human(s.RetireAfter) + " (" + retireWhy + ")"
	}
	p := Plan{
		State:  StateSlim,
		Action: slimAction,
		Reason: fmt.Sprintf("clean, %s%s", idleStr, slimNote),
	}
	if blocker != "" {
		p.Blockers = append(p.Blockers, blocker)
		p.Reason += "; " + blocker
	}
	return withForced(p, forced)
}

// needsYouRetireAt is when a needs-you worktree may be retired: abandonAfter
// past its last activity, but never sooner than the grace period
// (abandonAfter − needsYouAfter, at least a day) after Hammas was told — so a
// worktree idle for months still gets its full warning — and never earlier
// than a date already announced.
func needsYouRetireAt(en *Entry, last time.Time, s Settings, now time.Time) time.Time {
	grace := s.AbandonAfter - s.NeedsYouAfter
	if grace < 24*time.Hour {
		grace = 24 * time.Hour
	}
	told := now
	if en != nil && !en.NeedsYouNotifiedAt.IsZero() {
		told = en.NeedsYouNotifiedAt
	}
	at := last.Add(s.AbandonAfter)
	if t := told.Add(grace); t.After(at) {
		at = t
	}
	if en != nil && en.RetireAt.After(at) {
		at = en.RetireAt
	}
	return at
}

// slimVerdict decides whether there is anything to slim, and whether it may be.
// node_modules needs a tracked lockfile (so the reinstall is exact); build
// caches (.vite, .next, …) are regenerated by the build and need nothing.
func slimVerdict(f *Facts) (Action, string) {
	if len(f.SlimTargets) == 0 {
		return ActionNone, "; already slim"
	}
	if f.LockfileTracked {
		return ActionSlim, fmt.Sprintf("; slim %d dir(s)", len(f.SlimTargets))
	}
	var caches int
	for _, t := range f.SlimTargets {
		if filepath.Base(t) != "node_modules" {
			caches++
		}
	}
	if caches > 0 {
		return ActionSlim, fmt.Sprintf("; slim %d cache dir(s), keep node_modules (no tracked lockfile)", caches)
	}
	return ActionNone, "; not slimmed: no tracked lockfile"
}

func keptBy(s Settings, path string) string {
	k, _ := s.Kept(path)
	return k
}

func untouchable(why string) Plan {
	return Plan{State: StateUntouchable, Action: ActionNone, Reason: why}
}

func withForced(p Plan, forced bool) Plan {
	if forced && p.Action != ActionNone {
		p.Forced = true
		p.Reason += " [forced]"
	}
	return p
}

func threadLabel(t ThreadFact) string {
	title := strings.TrimSpace(t.Title)
	if len(title) > 40 {
		title = title[:40] + "…"
	}
	if title == "" {
		return t.ID
	}
	return fmt.Sprintf("%q", title)
}

func openPRs(f *Facts) string {
	var nums []string
	for _, p := range f.PRs {
		if p.State == "OPEN" {
			nums = append(nums, fmt.Sprintf("#%d", p.Number))
		}
	}
	return "PR " + strings.Join(nums, ",") + " open"
}

func prOf(f *Facts, state string) PR {
	var best PR
	for _, p := range f.PRs {
		if p.State == state && p.Number > best.Number {
			best = p
		}
	}
	return best
}

// GitSummary is the GIT column: "clean", or "2 dirty, 1 untracked, 3 unpushed".
func GitSummary(f *Facts) string {
	if f.GitErr != "" {
		return "error"
	}
	var parts []string
	if f.Dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", f.Dirty))
	}
	if f.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", f.Untracked))
	}
	if f.Unpushed > 0 {
		parts = append(parts, fmt.Sprintf("%d unpushed", f.Unpushed))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, ", ")
}

// Human renders a duration coarsely: 45m, 5h, 3d.
func Human(d time.Duration) string {
	switch {
	case d < 0:
		return "0m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
