package lifecycle

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// fxTimeout bounds every git/gh/tmux call made while gathering facts.
const fxTimeout = 30 * time.Second

// fxRun runs a command read-only and returns trimmed stdout. Git is told not
// to take optional locks: agents may be working in the tree while we look.
func fxRun(ctx context.Context, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fxTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), firstLine(msg))
	}
	return strings.TrimSpace(out.String()), nil
}

// fxGit runs `git -C path args...`.
func fxGit(ctx context.Context, path string, args ...string) (string, error) {
	return fxRun(ctx, "", "git", append([]string{"-C", path}, args...)...)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// repoRootOf returns the main worktree of the repo containing path, from
// git's common dir (its parent when it ends in /.git; a bare repo is itself).
func repoRootOf(ctx context.Context, path string) (string, error) {
	common, err := fxGit(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common = strings.TrimSuffix(common, "/")
	if strings.HasSuffix(common, "/.git") {
		return realpath(strings.TrimSuffix(common, "/.git")), nil
	}
	return realpath(common), nil
}

// inside reports whether p is root or below it.
func inside(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+"/")
}
