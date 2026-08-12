package mux

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The context file is conductor's only standing channel to an agent T3 is
// running, so what it does and does not say is behaviour, not documentation.
func TestT3AgentPromptNamesTheAddress(t *testing.T) {
	prompt := T3AgentPrompt("kudtrading", "t3code/429ade34", []int{3159, 3160})

	// The address is written in rather than described. An agent that has to run
	// a command to find the app will guess 3000 instead.
	assert.Contains(t, prompt, "The app is at http://localhost:3159")
	assert.Contains(t, prompt, "BASE_URL=http://localhost:3159")
	assert.NotContains(t, prompt, "3160", "only the first port is the dev server")

	// And a way to re-derive it, because a woken worktree gets new ports.
	assert.Contains(t, prompt, "conductor t3 dev status")
}

// A worktree with no ports yet must not be given an invented one.
func TestT3AgentPromptAdmitsWhenThereIsNoPort(t *testing.T) {
	prompt := T3AgentPrompt("demo", "feature", nil)

	assert.NotContains(t, prompt, "localhost:")
	assert.Contains(t, prompt, "conductor t3 dev status")
}

// Each of these is something an agent gets wrong when it is not told: it starts
// a second dev server, it never reads the server's log, it runs Playwright
// directly on a machine that allows one browser job at a time, and it reports a
// green exit code with nothing to look at.
func TestT3AgentPromptCoversTheOperationalRules(t *testing.T) {
	prompt := T3AgentPrompt("kudtrading", "t3code/429ade34", []int{3159})

	for _, required := range []string{
		"conductor wait",            // provisioning
		"conductor t3 dev restart",  // recovery
		"conductor t3 dev stop",     //
		"conductor t3 logs -f",      // logs
		"e2e-queue enqueue test",    // the queue, not playwright
		"e2e-queue worker status",   //
		"--workers=1",               //
		"page.screenshot",           // evidence
		"E2E_QUEUE_BROWSER_CDP",     // the pooled tab
		"Do not start a dev server", //
		"Never hardcode ports",      //
		"conductor t3 dev status",   //
	} {
		assert.Contains(t, prompt, required)
	}
}

// The window name is a tmux target an agent may paste; a label is a display
// string that must not read as one.
func TestT3AgentPromptLabelHasNoSlashes(t *testing.T) {
	prompt := T3AgentPrompt("kudtrading", "t3code/429ade34", []int{3159})
	assert.Contains(t, prompt, "--label kudtrading-t3code-429ade34")
}

// The file is read in a terminal, so lines that run past a terminal's width are
// a defect in it.
func TestT3AgentPromptWrapsToATerminal(t *testing.T) {
	prompt := T3AgentPrompt("kudtrading", "t3code/429ade34", []int{3159})
	for _, line := range strings.Split(prompt, "\n") {
		assert.LessOrEqual(t, len(line), 88, "overlong line: %q", line)
	}
}
