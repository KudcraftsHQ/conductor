package tmux

import (
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/session"
)

// spinnerFrames is a 10-frame braille dots animation (same as spinner "dots"
// in https://github.com/vyfor/rattles).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerInterval is how often the animation advances.
const spinnerInterval = 100 * time.Millisecond

// maxTabTitleLen is the max rendered length for a window/tab title
// (iTerm2 truncates beyond this anyway).
const maxTabTitleLen = 28

// iconForStatus returns a single visually distinct Unicode glyph for each
// agent status. These show up as the first character of the tmux window
// name, which iTerm2 renders as the native tab title.
func iconForStatus(s session.AgentStatus) string {
	switch s {
	case session.StatusRunning:
		return "⚡"
	case session.StatusToolRunning:
		return "⚙"
	case session.StatusDone:
		return "✓"
	case session.StatusError:
		return "✗"
	case session.StatusWaiting:
		return "?"
	case session.StatusInterrupted:
		return "⚠"
	case session.StatusStale:
		return "⏸"
	default:
		return "○"
	}
}

// buildTabTitle constructs "<icon> <project>/<branch>" truncated to fit.
// The input windowName is assumed to be in "project/branch" format.
func buildTabTitle(icon, windowName string) string {
	title := icon + " " + windowName
	if runeLen(title) <= maxTabTitleLen {
		return title
	}
	// Truncate, keeping the icon and leaving room for an ellipsis.
	prefix := icon + " "
	budget := maxTabTitleLen - runeLen(prefix) - 1 // -1 for ellipsis
	if budget < 1 {
		budget = 1
	}
	return prefix + truncateRunes(windowName, budget) + "…"
}

func runeLen(s string) int {
	return len([]rune(s))
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// tabTitle state: last rendered title per window + last known status so the
// animation loop can keep drawing spinner frames even between 3s tracker
// updates.
var (
	tabTitleMu       sync.Mutex
	tabTitleCache    = map[string]string{}              // windowName → last rendered title
	tabStatusCache   = map[string]session.AgentStatus{} // windowName → last known status
	spinnerLoopStart sync.Once
)

// UpdateTabTitles takes a session snapshot from the tracker and stores the
// latest statuses. The animation loop (started on first call) actually
// issues tmux rename commands every spinnerInterval — batching all renames
// into a single tmux script so the spinner stays smooth even with many tabs.
func UpdateTabTitles(sessions []*session.Session) {
	tabTitleMu.Lock()
	// Reset status cache from the latest snapshot. Windows absent from the
	// snapshot (no agent pane) are removed so we stop animating them.
	seen := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		if s.WindowName == "" {
			continue
		}
		seen[s.WindowName] = true
		// Prefer higher-priority status if the same window has multiple panes.
		if existing, ok := tabStatusCache[s.WindowName]; ok {
			if s.Status.Priority() <= existing.Priority() {
				continue
			}
		}
		tabStatusCache[s.WindowName] = s.Status
	}
	for win := range tabStatusCache {
		if !seen[win] {
			delete(tabStatusCache, win)
			delete(tabTitleCache, win)
		}
	}
	tabTitleMu.Unlock()

	spinnerLoopStart.Do(startSpinnerLoop)
	// Kick once immediately so non-animated states update without waiting
	// for the next animation tick.
	go renderOnce()
}

// startSpinnerLoop kicks off a goroutine that rewrites active window titles
// on every animation tick. Exits on its own once there are no tabs to animate
// (see renderOnce).
func startSpinnerLoop() {
	go func() {
		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		for range ticker.C {
			renderOnce()
		}
	}()
}

// renderOnce computes the current titles for all tracked windows (using the
// current spinner frame for animated statuses) and issues a batched rename
// via a single tmux script.
func renderOnce() {
	frame := int(time.Now().UnixMilli()/100) % len(spinnerFrames)
	spinner := spinnerFrames[frame]

	tabTitleMu.Lock()
	// Build the list of rename commands we actually need.
	type renameOp struct {
		windowName string
		title      string
	}
	var ops []renameOp
	for windowName, status := range tabStatusCache {
		icon := iconForStatus(status)
		if isAnimatedStatus(status) {
			icon = spinner
		}
		title := buildTabTitle(icon, windowName)
		if tabTitleCache[windowName] == title {
			continue
		}
		ops = append(ops, renameOp{windowName, title})
		tabTitleCache[windowName] = title
	}
	tabTitleMu.Unlock()

	if len(ops) == 0 {
		return
	}

	// Batch the renames into a single tmux invocation using `; ` separators.
	// Each op is a separate `rename-window -t <target> <title>` argv.
	args := make([]string, 0, len(ops)*6)
	for i, op := range ops {
		if i > 0 {
			args = append(args, ";")
		}
		target := fmt.Sprintf("%s:%s", SessionName, op.windowName)
		args = append(args, "rename-window", "-t", target, op.title)
	}
	_ = exec.Command("tmux", args...).Run()
}

// isAnimatedStatus returns true for statuses where the icon should be an
// animated spinner frame rather than a static glyph.
func isAnimatedStatus(s session.AgentStatus) bool {
	return s == session.StatusRunning || s == session.StatusToolRunning
}

// ResetTabTitle restores a window's title to just "project/branch" without
// an icon. Used when conductor shuts down to leave clean names behind.
func ResetTabTitle(project, branch string) {
	windowName := WindowName(project, branch)
	target := fmt.Sprintf("%s:%s", SessionName, windowName)
	_ = exec.Command("tmux", "rename-window", "-t", target, windowName).Run()

	tabTitleMu.Lock()
	delete(tabTitleCache, windowName)
	tabTitleMu.Unlock()
}
