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
	"os"
	"os/exec"
	"path/filepath"
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

// ListenersByPort returns every listening TCP port and the pids holding it, in
// one probe. A watcher checking a hundred worktrees every few seconds cannot
// afford a process spawn per port.
func ListenersByPort() map[int][]int {
	if out, err := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn").Output(); err == nil {
		if ports := ParseLsofFields(string(out)); len(ports) > 0 {
			return ports
		}
	}
	out, err := exec.Command("ss", "-ltnpH").Output()
	if err != nil {
		return nil
	}
	return ParseSSByPort(string(out))
}

// ParseLsofFields reads `lsof -Fpn` output: a "p<pid>" line followed by one
// "n<addr>:<port>" line per socket that process holds.
func ParseLsofFields(out string) map[int][]int {
	ports := map[int][]int{}
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if port := portOf(line[1:]); port > 0 && pid > 1 {
				ports[port] = appendUnique(ports[port], pid)
			}
		}
	}
	return ports
}

// ParseSSByPort reads `ss -ltnpH` output, keyed by the local port.
func ParseSSByPort(out string) map[int][]int {
	ports := map[int][]int{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		port := portOf(fields[3])
		if port <= 0 {
			continue
		}
		for _, pid := range ParseSS(line) {
			ports[port] = appendUnique(ports[port], pid)
		}
	}
	return ports
}

func portOf(addr string) int {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0
	}
	port, err := strconv.Atoi(addr[i+1:])
	if err != nil {
		return 0
	}
	return port
}

func appendUnique(pids []int, pid int) []int {
	for _, p := range pids {
		if p == pid {
			return pids
		}
	}
	return append(pids, pid)
}

// Rooted returns the listeners running from inside dir that look like a dev
// server — bun or node — and are not under a tmux server, which is where
// conductor's own supervised servers live.
//
// This is the fallback for a stray on a port conductor never allocated: an
// agent that runs `bun run dev` gets the project's default port, not the
// worktree's. It is deliberately narrower than "anything with that cwd", which
// would catch the e2e-queue browser; callers use it only for worktrees whose
// work is finished. It reads /proc, so elsewhere it finds nothing.
func Rooted(dir string, listeners map[int][]int) []int {
	dir = filepath.Clean(dir)
	var out []int
	seen := map[int]bool{}
	for _, pids := range listeners {
		for _, pid := range pids {
			if seen[pid] {
				continue
			}
			seen[pid] = true
			cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
			if err != nil || !within(cwd, dir) {
				continue
			}
			if comm := procComm(pid); comm != "bun" && comm != "node" {
				continue
			}
			if underTmux(pid) {
				continue
			}
			out = append(out, pid)
		}
	}
	return out
}

func within(path, dir string) bool {
	path = filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func procComm(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// underTmux walks a process's parents looking for a tmux server.
func underTmux(pid int) bool {
	for depth := 0; pid > 1 && depth < 64; depth++ {
		if strings.HasPrefix(procComm(pid), "tmux") {
			return true
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return false
		}
		// The comm field is parenthesised and may hold spaces; ppid follows it.
		fields := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+1:]))
		if len(fields) < 2 {
			return false
		}
		pid, _ = strconv.Atoi(fields[1])
	}
	return false
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
