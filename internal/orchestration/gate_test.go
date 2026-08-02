package orchestration

import "testing"

func TestClassifyGate(t *testing.T) {
	cases := []struct {
		text string
		want ReadbackGate
	}{
		// Clearly a document.
		{"research our Shopee competitors and write it up", GateRequired},
		{"write up what happened in the outage", GateRequired},
		{"push a readback with the findings", GateRequired},
		{"do a post-mortem on the Friday incident", GateRequired},
		{"audit the API for missing auth checks", GateRequired},
		{"compare Vite and Next for this app", GateRequired},
		{"plan the migration to Hono", GateRequired},

		// Clearly code.
		{"fix the crash in the uploader", GateNotNeeded},
		{"implement email sharing for folders", GateNotNeeded},
		{"refactor the port allocator", GateNotNeeded},
		{"add a test for the retry path", GateNotNeeded},
		{"bump the postgres driver", GateNotNeeded},
		{"revert the caching change", GateNotNeeded},

		// Not enough to go on.
		{"have a look at the uploader", GateUndecided},
		{"the dashboard feels slow", GateUndecided},
		{"can you take this one", GateUndecided},
		{"", GateUndecided},
	}
	for _, c := range cases {
		if got := ClassifyGate(c.text); got != c.want {
			t.Errorf("ClassifyGate(%q) = %s, want %s", c.text, got, c.want)
		}
	}
}

// A request that changes code *and* asks to be written up owes the document.
// Losing the write-up because the text also said "fix" is the failure this
// precedence rule exists for.
func TestCodeWorkThatAlsoAsksForAWriteUpRequiresReadback(t *testing.T) {
	for _, text := range []string{
		"fix the caching bug and write up what was wrong",
		"investigate the crash, then patch it and push a readback",
		"refactor the uploader and give me a short report",
	} {
		if got := ClassifyGate(text); got != GateRequired {
			t.Errorf("ClassifyGate(%q) = %s, want %s", text, got, GateRequired)
		}
	}
}

func TestParseGateDecision(t *testing.T) {
	yes := []string{"yes", "Yes", "y", "yeah", "readback", "required", " wait "}
	for _, r := range yes {
		got, ok := ParseGateDecision(r)
		if !ok || got != GateRequired {
			t.Errorf("ParseGateDecision(%q) = %s,%v; want %s,true", r, got, ok, GateRequired)
		}
	}
	no := []string{"no", "N", "nope", "skip", "not needed", "close it"}
	for _, r := range no {
		got, ok := ParseGateDecision(r)
		if !ok || got != GateNotNeeded {
			t.Errorf("ParseGateDecision(%q) = %s,%v; want %s,true", r, got, ok, GateNotNeeded)
		}
	}
	// Anything else leaves the gate alone. Guessing "no" here would throw away
	// a document somebody asked for.
	for _, r := range []string{"maybe", "later", "what?", "", "yes but only the summary"} {
		if got, ok := ParseGateDecision(r); ok {
			t.Errorf("ParseGateDecision(%q) = %s,true; want no decision", r, got)
		}
	}
}

func TestGateQuestionNamesTheWork(t *testing.T) {
	q := GateQuestion(&Task{ID: "demo-123", Worktree: WorktreeRef{Branch: "prague"}})
	for _, want := range []string{"demo-123", "prague", "yes", "no"} {
		if !contains(q, want) {
			t.Fatalf("the question is missing %q:\n%s", want, q)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
