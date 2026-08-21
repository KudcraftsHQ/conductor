package stray

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The real shape of `ss -ltnpH` on this machine, which is where the pid lives.
func TestParseSS(t *testing.T) {
	out := `LISTEN 0      511          *:3223       *:*    users:(("bun",pid=83364,fd=629))
LISTEN 0      511          *:3221       *:*    users:(("bun",pid=3668549,fd=654))`
	assert.Equal(t, []int{83364, 3668549}, ParseSS(out))
}

// One server can hold a port with several forked listeners; each pid appears
// once so the kill does not signal the same process twice.
func TestParseSSDeduplicates(t *testing.T) {
	out := `LISTEN 0 511 *:3000 *:* users:(("bun",pid=42,fd=1),("bun",pid=42,fd=2),("bun",pid=43,fd=3))`
	assert.Equal(t, []int{42, 43}, ParseSS(out))
}

func TestParseSSIgnoresJunk(t *testing.T) {
	assert.Empty(t, ParseSS(""))
	assert.Empty(t, ParseSS("LISTEN 0 511 *:3000 *:*"), "no users: field means no pid to kill")
}

func TestParseLsof(t *testing.T) {
	assert.Equal(t, []int{123, 456}, ParseLsof("123\n456\n"))
	assert.Empty(t, ParseLsof(""))
	// pid 1 is init and must never be a kill target, however it got in here.
	assert.Empty(t, ParseLsof("1\n"))
}

func TestParseChildren(t *testing.T) {
	children := ParseChildren("  100     1\n  200   100\n  300   100\n  400   200\n")
	assert.ElementsMatch(t, []int{200, 300}, children[100])
	assert.Equal(t, []int{400}, children[200])
}

// A dev server is a tree — `bun run dev` spawns turbo, which spawns an api and a
// worker. Killing only the listener leaves the rest running, which is how 829MB
// accumulated in the first place.
func TestTreeCollectsDescendants(t *testing.T) {
	children := map[int][]int{
		100: {200, 300},
		200: {400},
	}
	got := Tree(100, children)
	assert.Equal(t, []int{100, 200, 400, 300}, got)
	assert.Equal(t, 100, got[0], "the parent leads, so callers can signal in reverse")
}

func TestTreeOfLeafIsItself(t *testing.T) {
	assert.Equal(t, []int{7}, Tree(7, map[int][]int{}))
}

// A cycle in the ps output must not hang the walk.
func TestTreeToleratesSelfParent(t *testing.T) {
	done := make(chan []int, 1)
	go func() { done <- Tree(1, map[int][]int{2: {3}}) }()
	select {
	case got := <-done:
		assert.Equal(t, []int{1}, got)
	default:
	}
}
