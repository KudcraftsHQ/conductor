package t3watch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithUserPathAddsMissingUserDirs(t *testing.T) {
	exists := func(dir string) bool {
		return dir == "/home/u/.local/bin" || dir == "/home/u/.bun/bin" || dir == "/usr/bin"
	}
	got := withUserPath("/usr/local/bin:/usr/bin", "/home/u", "/home/u/.local/bin", exists)
	assert.Equal(t, "/home/u/.local/bin:/home/u/.bun/bin:/usr/local/bin:/usr/bin", got)
}

func TestWithUserPathLeavesAFullPathAlone(t *testing.T) {
	exists := func(string) bool { return true }
	path := "/opt/x:/home/u/.local/bin:/home/u/.bun/bin:/home/u/go/bin:/home/u/.cargo/bin"
	assert.Equal(t, path, withUserPath(path, "/home/u", "/home/u/.local/bin/", exists))
}
