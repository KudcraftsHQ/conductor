package codingagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testContext = "# Conductor Context\n\nApp: http://localhost:3159\n"

// No test reads or hides the real user's pi config.
func isolatePi(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// A fresh worktree gets a CLAUDE.local.md that imports the context file and
// nothing else.
func TestWriteAutoloadShimsCreatesClaudeLocal(t *testing.T) {
	isolatePi(t)
	dir := t.TempDir()
	require.NoError(t, WriteAutoloadShims(dir, testContext))

	content := readFile(t, filepath.Join(dir, ClaudeLocalFile))
	assert.Contains(t, content, "@"+ContextFileName)
	assert.Equal(t, claudeShimBlock(), content)
}

// Provision and wake both write, and a backfill may run over the same tree any
// number of times; the file must not grow.
func TestWriteAutoloadShimsIsIdempotent(t *testing.T) {
	isolatePi(t)
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		require.NoError(t, WriteAutoloadShims(dir, testContext))
	}
	content := readFile(t, filepath.Join(dir, ClaudeLocalFile))
	assert.Equal(t, 1, strings.Count(content, shimBegin))
	assert.Equal(t, 1, strings.Count(content, "@"+ContextFileName))
}

// Somebody's own CLAUDE.local.md is theirs: conductor appends its block after
// it, once, and never rewrites what is outside the markers.
func TestWriteAutoloadShimsAppendsToAnExistingClaudeLocal(t *testing.T) {
	isolatePi(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ClaudeLocalFile)
	mine := "# My notes\nUse the staging API key from 1Password."
	require.NoError(t, os.WriteFile(path, []byte(mine), 0644))

	require.NoError(t, WriteAutoloadShims(dir, testContext))
	require.NoError(t, WriteAutoloadShims(dir, testContext))

	content := readFile(t, path)
	assert.True(t, strings.HasPrefix(content, mine+"\n"), "the owner's content must stay first and intact")
	assert.Equal(t, 1, strings.Count(content, shimBegin))
	assert.True(t, strings.HasSuffix(content, claudeShimBlock()))

	// Edits the owner makes after the block survive a rewrite too.
	require.NoError(t, os.WriteFile(path, []byte(content+"\nMore notes.\n"), 0644))
	require.NoError(t, WriteAutoloadShims(dir, testContext))
	content = readFile(t, path)
	assert.Contains(t, content, "More notes.")
	assert.Equal(t, 1, strings.Count(content, shimBegin))
}

// An end marker deleted by hand must not lead to a second block on every run.
func TestWriteAutoloadShimsRepairsAMangledBlock(t *testing.T) {
	isolatePi(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ClaudeLocalFile)
	require.NoError(t, os.WriteFile(path, []byte("keep\n\n"+shimBegin+"\n@old.md\n"), 0644))

	require.NoError(t, WriteAutoloadShims(dir, testContext))
	content := readFile(t, path)
	assert.Equal(t, "keep\n\n"+claudeShimBlock(), content)
}

// In a linked worktree git reads info/exclude from the common dir, not the
// worktree's own git dir — writing to the latter silently does nothing.
func TestExcludeFromGitHidesTheShimsInALinkedWorktree(t *testing.T) {
	isolatePi(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	wt := filepath.Join(root, "wt")
	require.NoError(t, os.MkdirAll(repo, 0755))
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(out)
	}
	git(repo, "init", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0644))
	// A repository that tracks one of the shim names owns it.
	require.NoError(t, os.WriteFile(filepath.Join(repo, AgentsLocalFile), []byte("tracked\n"), 0644))
	git(repo, "add", ".")
	git(repo, "commit", "-m", "init")
	git(repo, "worktree", "add", "-b", "feat", wt)

	require.NoError(t, WriteContextFile(wt, "hello"))
	require.NoError(t, WriteAutoloadShims(wt, testContext))
	ExcludeFromGit(wt, AutoloadFiles()...)
	ExcludeFromGit(wt, AutoloadFiles()...)

	assert.Empty(t, strings.TrimSpace(git(wt, "status", "--porcelain")), "shims must not show in git status")
	assert.Equal(t, "tracked\n", readFile(t, filepath.Join(wt, AgentsLocalFile)), "a tracked file is never edited")
	assert.FileExists(t, filepath.Join(wt, ClaudeLocalFile))

	exclude := readFile(t, filepath.Join(repo, ".git", "info", "exclude"))
	for _, entry := range AutoloadFiles() {
		assert.Equal(t, 1, strings.Count(exclude, "\n"+entry+"\n"), "%s listed exactly once", entry)
	}
}

// dsh and pi cannot resolve an import, so their shims carry the context
// itself; it is rewritten with the context file, so it is never stale.
func TestWriteAutoloadShimsInlinesForAgentsWithoutImports(t *testing.T) {
	isolatePi(t)
	dir := t.TempDir()
	require.NoError(t, WriteAutoloadShims(dir, testContext))

	for _, rel := range []string{AgentsLocalFile, PiAppendSystemFile} {
		content := readFile(t, filepath.Join(dir, rel))
		assert.Equal(t, inlineShimBlock(testContext), content, rel)
	}

	// A wake rewrites the block with the new port, not beside the old one.
	require.NoError(t, WriteAutoloadShims(dir, "App: http://localhost:3200\n"))
	content := readFile(t, filepath.Join(dir, AgentsLocalFile))
	assert.Contains(t, content, "3200")
	assert.NotContains(t, content, "3159")
}

// A project APPEND_SYSTEM.md replaces the user's global one in pi, so it is
// not written when there is a global one to hide.
func TestWriteAutoloadShimsDoesNotShadowAGlobalPiAppendSystem(t *testing.T) {
	piDir := isolatePi(t)
	require.NoError(t, os.WriteFile(filepath.Join(piDir, "APPEND_SYSTEM.md"), []byte("mine\n"), 0644))

	dir := t.TempDir()
	require.NoError(t, WriteAutoloadShims(dir, testContext))
	assert.NoFileExists(t, filepath.Join(dir, PiAppendSystemFile))
	assert.FileExists(t, filepath.Join(dir, AgentsLocalFile))
}
