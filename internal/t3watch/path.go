package t3watch

import (
	"os"
	"path/filepath"
	"strings"
)

// userBinDirs are where a user's own tools live, in the order a login shell on
// this setup puts them. bun, node and conductor itself are all in one of them.
var userBinDirs = []string{".local/bin", ".bun/bin", "go/bin", ".cargo/bin"}

// ensureUserPath makes the project scripts the watcher runs see the same tools
// a login shell would.
//
// The watcher runs as a systemd user service, whose PATH is the bare
// /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin. Everything it spawns
// inherits that: a wake runs the project's setup.sh, which died on line one
// with "bun: command not found", and archive.sh calls `conductor` by name.
// Under T3's hook or an interactive shell the same scripts work, so the gap
// only showed once a wake had to provision from the service.
func ensureUserPath() {
	home, _ := os.UserHomeDir()
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	_ = os.Setenv("PATH", withUserPath(os.Getenv("PATH"), home, exeDir, isDir))
}

// withUserPath prepends the directory of the running binary and the user's
// bin directories that exist, skipping any already on path.
func withUserPath(path, home, exeDir string, exists func(string) bool) string {
	have := make(map[string]bool)
	for _, dir := range filepath.SplitList(path) {
		have[filepath.Clean(dir)] = true
	}
	var add []string
	consider := func(dir string) {
		if dir == "" {
			return
		}
		dir = filepath.Clean(dir)
		if have[dir] || !exists(dir) {
			return
		}
		have[dir] = true
		add = append(add, dir)
	}
	consider(exeDir)
	if home != "" {
		for _, rel := range userBinDirs {
			consider(filepath.Join(home, rel))
		}
	}
	if len(add) == 0 {
		return path
	}
	if path == "" {
		return strings.Join(add, string(filepath.ListSeparator))
	}
	return strings.Join(add, string(filepath.ListSeparator)) + string(filepath.ListSeparator) + path
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
