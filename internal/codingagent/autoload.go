package codingagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The context file is only useful if agents read it, and none of them look for
// a file called .conductor-context.md. Each agent does auto-load some
// per-directory instruction file, so conductor drops a shim of that name beside
// the context file, importing or carrying it.
//
// Two constraints shape which shims exist:
//
//   - No tracked file is touched. The repository's CLAUDE.md and AGENTS.md
//     belong to somebody else's project; a shim that edited them would show up
//     in every diff an agent commits.
//   - No shim may hide the repository's own instructions. Codex's
//     AGENTS.override.md, for one, replaces AGENTS.md in the same directory, so
//     it is not used.
//
// What each agent T3 runs here loads, and so which shim it gets:
//
//   - Claude Code: CLAUDE.local.md, resolving `@path` imports — a one-line
//     import of the context file.
//   - dsh: AGENTS.local.md and CLAUDE.local.md as additive overlays after
//     AGENTS.md/CLAUDE.md, with no import support — so AGENTS.local.md carries
//     the context inline.
//   - pi: one of AGENTS.md/CLAUDE.md per directory (first found), no imports,
//     no local overlay; but .pi/APPEND_SYSTEM.md is appended to its system
//     prompt. That file replaces the user's global APPEND_SYSTEM.md, so it is
//     only written when there is no global one to hide.
//   - Codex: AGENTS.override.md, else AGENTS.md, else configured fallbacks —
//     one file per directory and no imports. Every option either hides the
//     repository's AGENTS.md or edits a tracked file, so Codex gets no shim.
//
// A shim path that the repository tracks is left alone. Every shim is listed
// in .git/info/exclude so it never reaches `git status`.

// ClaudeLocalFile is the per-user, per-directory memory file Claude Code loads
// alongside CLAUDE.md. It resolves `@path` imports, so the shim is one line.
const ClaudeLocalFile = "CLAUDE.local.md"

// AgentsLocalFile is dsh's additive local overlay to AGENTS.md.
const AgentsLocalFile = "AGENTS.local.md"

// PiAppendSystemFile is pi's project-level system prompt addition.
var PiAppendSystemFile = filepath.Join(".pi", "APPEND_SYSTEM.md")

const (
	shimBegin = "<!-- conductor:begin — managed by conductor, rewritten on provision and wake -->"
	shimEnd   = "<!-- conductor:end -->"
)

// claudeShimBlock is the marked block conductor owns inside CLAUDE.local.md.
// The sentence is for dsh, which also reads CLAUDE.local.md but does not
// resolve the import.
func claudeShimBlock() string {
	return shimBegin + "\n" +
		"This worktree's environment (app URL, ports, database, dev server rules) is in " + ContextFileName + ".\n" +
		"@" + ContextFileName + "\n" +
		shimEnd + "\n"
}

// inlineShimBlock carries the context itself, for agents that cannot import.
func inlineShimBlock(context string) string {
	return shimBegin + "\n" + strings.TrimRight(context, "\n") + "\n" + shimEnd + "\n"
}

// AutoloadFiles is every file conductor may write into a worktree for agents
// to find, for the git exclude.
func AutoloadFiles() []string {
	return []string{ContextFileName, ClaudeLocalFile, AgentsLocalFile, filepath.ToSlash(PiAppendSystemFile)}
}

// WriteAutoloadShims makes the context file reach the agents T3 runs. context
// is the context file's content, inlined where an agent cannot import it.
//
// It is idempotent: a file that already carries conductor's block has the
// block replaced in place, and one that somebody else wrote keeps its content
// with the block appended after it.
func WriteAutoloadShims(worktreePath, context string) error {
	shims := []struct {
		rel   string
		block string
	}{
		{ClaudeLocalFile, claudeShimBlock()},
		{AgentsLocalFile, inlineShimBlock(context)},
	}
	if !piGlobalAppendSystemExists() {
		shims = append(shims, struct {
			rel   string
			block string
		}{PiAppendSystemFile, inlineShimBlock(context)})
	}

	for _, shim := range shims {
		if gitTracks(worktreePath, shim.rel) {
			continue
		}
		path := filepath.Join(worktreePath, shim.rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := upsertBlock(path, shim.block); err != nil {
			return err
		}
	}
	return nil
}

// piGlobalAppendSystemExists reports whether the user has a global pi
// APPEND_SYSTEM.md, which a project one would silently replace.
func piGlobalAppendSystemExists() bool {
	dir := os.Getenv("PI_CODING_AGENT_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".pi", "agent")
	} else if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	_, err := os.Stat(filepath.Join(dir, "APPEND_SYSTEM.md"))
	return err == nil
}

// gitTracks reports whether rel is a tracked file of the worktree's repository.
// Outside a repository, or without git, nothing is tracked.
func gitTracks(worktreePath, rel string) bool {
	cmd := exec.Command("git", "-C", worktreePath, "ls-files", "--error-unmatch", "--", rel)
	return cmd.Run() == nil
}

// upsertBlock writes block into the file at path between conductor's markers,
// leaving anything outside them alone.
func upsertBlock(path, block string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(existing)

	var next string
	if begin := strings.Index(content, shimBegin); begin >= 0 {
		end := strings.Index(content[begin:], shimEnd)
		if end < 0 {
			// A begin with no end was mangled by hand; take everything after
			// it as ours rather than appending a second block.
			next = content[:begin] + block
		} else {
			rest := content[begin+end+len(shimEnd):]
			rest = strings.TrimPrefix(rest, "\n")
			next = content[:begin] + block + rest
		}
	} else {
		next = content
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		if next != "" {
			next += "\n"
		}
		next += block
	}

	if next == content {
		return nil
	}
	return os.WriteFile(path, []byte(next), 0644)
}

// ExcludeFromGit lists entries in the worktree's .git/info/exclude.
//
// The exclude file rather than .gitignore, because the worktree's .gitignore is
// a tracked file of somebody else's project. A linked worktree's git dir is
// per-worktree, but info/exclude is read from the common dir, so that is where
// the entries go. Failure is not reported: an unwritable exclude file is
// untidy, not broken.
func ExcludeFromGit(worktreePath string, entries ...string) {
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktreePath, dir)
	}
	path := filepath.Join(dir, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}

	existing, _ := os.ReadFile(path)
	present := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		present[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, entry := range entries {
		if !present[entry] && !present["/"+entry] {
			missing = append(missing, entry)
			present[entry] = true
		}
	}
	if len(missing) == 0 {
		return
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		_, _ = file.WriteString("\n")
	}
	_, _ = file.WriteString("\n# conductor\n" + strings.Join(missing, "\n") + "\n")
}
