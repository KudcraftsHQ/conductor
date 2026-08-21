// Package stray finds dev servers that conductor is not supervising.
//
// A worktree's dev server is supposed to live in a tmux window, where
// `conductor t3 dev stop` can reach it. Agents working in a thread routinely
// start one themselves instead — the T3 thread has no server to Ctrl-C, so
// running `bun run dev` looks like the obvious move — and the result is a
// process tree parented to systemd rather than to a pane. Conductor cannot see
// it, cannot stop it, and does not count it. On this machine that was 829MB
// across seven worktrees, three of which no longer existed on disk.
//
// Detection is by *port*, not by working directory. The tempting test — "a bun
// process whose cwd is inside the worktree" — misfires on shared services that
// merely happen to have been launched from there: the e2e-queue browser and its
// chromium renderers sat in one worktree holding 357MB and would have been
// killed as strays. A worktree's allocated port is unambiguous. Nothing but that
// worktree's own server should be listening on it.
package stray

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Listeners returns the pids listening on a TCP port.
//
// lsof is asked first because it is the same answer on macOS and Linux; ss is
// the fallback for the Linux boxes that ship without it.
func Listeners(port int) []int {
	if out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN").Output(); err == nil {
		if pids := ParseLsof(string(out)); len(pids) > 0 {
			return pids
		}
	}
	out, err := exec.Command("ss", "-ltnpH", fmt.Sprintf("sport = :%d", port)).Output()
	if err != nil {
		return nil
	}
	return ParseSS(string(out))
}

// ParseLsof reads `lsof -ti` output: one pid per line.
func ParseLsof(out string) []int {
	var pids []int
	for _, line := range strings.Fields(out) {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 1 {
			pids = append(pids, pid)
		}
	}
	return pids
}

var ssPID = regexp.MustCompile(`pid=(\d+)`)

// ParseSS reads the users:(("bun",pid=123,fd=4)) field of `ss -ltnp` output.
func ParseSS(out string) []int {
	seen := map[int]bool{}
	var pids []int
	for _, match := range ssPID.FindAllStringSubmatch(out, -1) {
		pid, err := strconv.Atoi(match[1])
		if err != nil || pid <= 1 || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids
}

// Tree returns pid and every process descended from it, deepest last.
//
// Descendants are walked explicitly rather than signalling the process group.
// A stray started by an agent can share its group with the agent's own shell,
// and killing the group would take the agent's session down with the server it
// was only meant to stop.
func Tree(pid int, children map[int][]int) []int {
	out := []int{pid}
	for _, child := range children[pid] {
		out = append(out, Tree(child, children)...)
	}
	return out
}

// Children maps each pid to its direct children, read from ps so this works
// the same on Linux and macOS.
func Children() map[int][]int {
	out, err := exec.Command("ps", "-eo", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	return ParseChildren(string(out))
}

// ParseChildren builds the pid -> children map from `ps -eo pid=,ppid=`.
func ParseChildren(out string) map[int][]int {
	children := map[int][]int{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	return children
}

// Kill stops a stray and everything under it, and reports how many processes it
// signalled. Children are signalled before their parents so a supervisor cannot
// restart one on the way down.
func Kill(pids []int) int {
	children := Children()
	var all []int
	seen := map[int]bool{}
	for _, pid := range pids {
		for _, p := range Tree(pid, children) {
			if !seen[p] && p > 1 {
				seen[p] = true
				all = append(all, p)
			}
		}
	}
	for i := len(all) - 1; i >= 0; i-- {
		_ = syscall.Kill(all[i], syscall.SIGTERM)
	}
	// Give a dev server its shutdown handler before insisting.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !anyAlive(all) {
			return len(all)
		}
		time.Sleep(250 * time.Millisecond)
	}
	for i := len(all) - 1; i >= 0; i-- {
		if syscall.Kill(all[i], 0) == nil {
			_ = syscall.Kill(all[i], syscall.SIGKILL)
		}
	}
	return len(all)
}

func anyAlive(pids []int) bool {
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			return true
		}
	}
	return false
}
