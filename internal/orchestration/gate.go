package orchestration

import (
	"fmt"
	"regexp"
	"strings"
)

// ClassifyGate decides, from the request text alone, whether the finished work
// will owe a Readback document.
//
// The three-way answer is the point. A binary guess would either block code-only
// tasks behind a document nobody wanted, or finish research tasks with the note
// still sitting in /tmp. When the text does not say, the honest answer is "ask",
// and the asking happens later — at completion — so a wrong guess never delays
// the agent.
//
// Precedence is deliberate: an explicit mention of Readback or a report/research
// deliverable wins over code words, because "research the caching bug and write
// it up, then fix it" owes a document even though it also changes code.
func ClassifyGate(text string) ReadbackGate {
	t := strings.ToLower(text)
	if t == "" {
		return GateUndecided
	}
	switch {
	case reReadbackExplicit.MatchString(t):
		return GateRequired
	case reDeliverable.MatchString(t):
		return GateRequired
	case reCodeOnly.MatchString(t) && !reMaybeDoc.MatchString(t):
		return GateNotNeeded
	}
	return GateUndecided
}

var (
	// Naming Readback, or asking to publish/push a document, is as explicit as
	// it gets.
	reReadbackExplicit = regexp.MustCompile(`\breadback\b|\bpush (?:it|this|the (?:doc|document|report|note))\b`)

	// Words that name a *document* as the deliverable. "write up", "report",
	// "research", "analysis", "post-mortem", "plan", "review", "comparison".
	reDeliverable = regexp.MustCompile(
		`\bwrite[ -]?up\b|\bwriteup\b|\breports?\b|\bresearch\b|\banalys[ei]s\b|` +
			`\bpost[ -]?mortem\b|\binvestigat(?:e|ion)\b|\baudit\b|\bcompare\b|\bcomparison\b|` +
			`\bsummar(?:y|ise|ize)\b|\brecommendations?\b|\bproposal\b|\bdocument\b|` +
			`\bplan\b(?:\s|$)|\bwrite (?:a|an|the) \w+ (?:doc|note|memo)\b`)

	// Words that name *code* as the deliverable.
	reCodeOnly = regexp.MustCompile(
		`\bfix\b|\bbug\b|\bimplement\b|\brefactor\b|\bmigrat(?:e|ion)\b|\bupgrade\b|` +
			`\brename\b|\bdelete\b|\badd (?:a |an |the )?(?:test|endpoint|field|column|flag|button|route)\b|` +
			`\bunit tests?\b|\bfailing tests?\b|\bpatch\b|\brevert\b|\bbump\b|\bhotfix\b|\bcrash(?:es|ing)?\b`)

	// A code request that also asks to be written up is not code-only.
	reMaybeDoc = regexp.MustCompile(`\bwrite[ -]?up\b|\breports?\b|\bresearch\b|\breadback\b|\bdocument\b`)
)

// GateQuestion is the concise question sent to the requester when the gate is
// undecided and the agent has finished. Kept short because it lands in a
// Discord thread next to the work, not in a document.
func GateQuestion(t *Task) string {
	name := t.ID
	return fmt.Sprintf(
		"**%s** finished on branch `%s`.\n"+
			"Does this one need a Readback write-up before I call it done?\n"+
			"Reply **yes** to wait for the document, or **no** to close it out now. "+
			"The agent and its worktree stay exactly as they are either way.",
		name, t.Worktree.Branch)
}

// ParseGateDecision maps a free-text reply to a gate value. It is intentionally
// strict: an unrecognised reply leaves the gate undecided rather than guessing,
// because guessing "no" discards a document somebody asked for.
func ParseGateDecision(reply string) (ReadbackGate, bool) {
	r := strings.ToLower(strings.TrimSpace(reply))
	r = strings.Trim(r, ".!, ")
	switch r {
	case "yes", "y", "yeah", "yep", "readback", "required", "wait",
		"readback_required", "write it up", "write-up":
		return GateRequired, true
	case "no", "n", "nope", "skip", "not needed", "no readback",
		"no_readback_needed", "close it", "done":
		return GateNotNeeded, true
	}
	return GateUndecided, false
}
