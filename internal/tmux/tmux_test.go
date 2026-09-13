package tmux

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A dev-only window — the shape T3 worktrees have.
func TestParseDevPaneSinglePane(t *testing.T) {
	assert.Equal(t, 1283071, parseDevPane("dev\t1283071\n"))
}

// The coding window has the agent on the left. The title is the only thing
// distinguishing the two, because both are bash.
func TestParseDevPaneTakesTheTitledPane(t *testing.T) {
	out := "t3code/87e109af - Claude (agent)\t1194165\ndev\t1194166\n"
	assert.Equal(t, 1194166, parseDevPane(out), "the agent pane is not the supervisor")
}

// Without a title to go on, a multi-pane window is not guessed at.
func TestParseDevPaneRefusesToChooseBetweenPanes(t *testing.T) {
	assert.Equal(t, 0, parseDevPane("left\t42\nright\t43\n"))
}

func TestParseDevPaneIgnoresJunk(t *testing.T) {
	assert.Equal(t, 0, parseDevPane(""))
	assert.Equal(t, 0, parseDevPane("no separator here\n"))
	assert.Equal(t, 0, parseDevPane("\tnot-a-pid\n"))
	// pid 1 is init; a pane can never be it, and it must not be probed.
	assert.Equal(t, 0, parseDevPane("dev\t1\n"))
}

// The supervisor is busy exactly while it has a child: `conductor run` and,
// under it, the dev server.
func TestHasChild(t *testing.T) {
	ps := "  100     1\n  200   100\n  300   100\n  400   200\n"
	assert.True(t, hasChild(ps, 100))
	assert.True(t, hasChild(ps, 200))
	assert.False(t, hasChild(ps, 300), "nothing runs under a shell parked at the prompt")
	assert.False(t, hasChild(ps, 999))
}

func TestHasChildIgnoresJunk(t *testing.T) {
	assert.False(t, hasChild("", 100))
	assert.False(t, hasChild("not a ps line\n", 100))
	assert.False(t, hasChild("  100\n", 100))
}
