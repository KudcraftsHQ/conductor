package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procTable is one read of /proc and tmux, shared by every worktree in a pass.
type procTable struct {
	procs   []Proc
	ppid    map[int]int
	panes   []tmuxPane
	unknown bool
}

type tmuxPane struct {
	windowID, windowName, cwd string
	pid                       int
}

// readProcTable scans /proc once (Linux) and lists tmux panes once. tmux being
// absent or not running means no windows, not an error.
func readProcTable(ctx context.Context) *procTable {
	t := &procTable{ppid: map[int]int{}}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.unknown = true
	} else {
		self := os.Getpid()
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			base := filepath.Join("/proc", e.Name())
			if stat, err := os.ReadFile(filepath.Join(base, "stat")); err == nil {
				// comm is parenthesised and may hold spaces or parens: split after the last ")".
				s := string(stat)
				if i := strings.LastIndexByte(s, ')'); i >= 0 {
					f := strings.Fields(s[i+1:])
					if len(f) >= 2 {
						if pp, err := strconv.Atoi(f[1]); err == nil {
							t.ppid[pid] = pp
						}
					}
				}
			}
			if pid == self {
				continue
			}
			cwd, err := os.Readlink(filepath.Join(base, "cwd"))
			if err != nil {
				continue
			}
			comm, _ := os.ReadFile(filepath.Join(base, "comm"))
			t.procs = append(t.procs, Proc{
				PID:  pid,
				Comm: strings.TrimSpace(string(comm)),
				Cwd:  strings.TrimSuffix(cwd, " (deleted)"),
			})
		}
		if len(t.ppid) == 0 {
			t.unknown = true
		}
	}

	out, err := fxRun(ctx, "", "tmux", "list-panes", "-a", "-F",
		"#{window_id}\t#{window_name}\t#{pane_current_path}\t#{pane_pid}")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			f := strings.Split(line, "\t")
			if len(f) < 4 {
				continue
			}
			pid, _ := strconv.Atoi(f[3])
			t.panes = append(t.panes, tmuxPane{f[0], f[1], strings.TrimSuffix(f[2], " (deleted)"), pid})
		}
	}
	return t
}

// inspect fills DevWindows and ProcsInside for one worktree path.
func (t *procTable) inspect(f *Facts) {
	f.ProcsUnknown = t.unknown
	byWin := map[string]*TmuxWindow{}
	var order []string
	panePIDs := map[int]bool{}
	for _, p := range t.panes {
		if !inside(p.cwd, f.Path) {
			continue
		}
		w, ok := byWin[p.windowID]
		if !ok {
			w = &TmuxWindow{ID: p.windowID, Name: p.windowName}
			byWin[p.windowID] = w
			order = append(order, p.windowID)
		}
		if p.pid > 0 {
			w.PanePIDs = append(w.PanePIDs, p.pid)
			panePIDs[p.pid] = true
		}
	}
	for _, id := range order {
		f.DevWindows = append(f.DevWindows, *byWin[id])
	}
	for _, p := range t.procs {
		if !inside(p.Cwd, f.Path) || t.descendsFrom(p.PID, panePIDs) {
			continue
		}
		f.ProcsInside = append(f.ProcsInside, p)
	}
}

// descendsFrom reports whether pid is one of roots or below one of them.
func (t *procTable) descendsFrom(pid int, roots map[int]bool) bool {
	if len(roots) == 0 {
		return false
	}
	for i := 0; i < 64 && pid > 1; i++ {
		if roots[pid] {
			return true
		}
		pid = t.ppid[pid]
	}
	return false
}
