package t3

import (
	"os"
	"path/filepath"
	"strings"
)

// MarkerFileName records, inside a worktree, that T3 Code is hosting it.
//
// Reconciliation needs to tell "this worktree's thread was archived" apart from
// "this worktree predates the T3 backend entirely". Without that distinction
// every worktree ever made under tmux or herdr looks like drift, and archiving
// on drift would destroy all of them.
//
// The marker lives in the worktree so it disappears with it, and holds the
// thread id so conductor can resolve the thread without a snapshot scan.
const MarkerFileName = ".conductor-t3-thread"

// WriteMarker records the threads hosting a worktree, one id per line.
//
// A worktree can carry several threads at once — T3 reuses an existing worktree
// when a new thread picks a branch that already has one — so this is a set
// rather than a single id.
func WriteMarker(worktreePath string, threadIDs []string) error {
	var buf strings.Builder
	for _, id := range threadIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		buf.WriteString(id)
		buf.WriteString("\n")
	}
	return os.WriteFile(markerPath(worktreePath), []byte(buf.String()), 0644)
}

// ReadMarker returns the thread ids recorded for a worktree. The second result
// is false when the worktree is not hosted by T3.
func ReadMarker(worktreePath string) ([]string, bool) {
	data, err := os.ReadFile(markerPath(worktreePath))
	if err != nil {
		return nil, false
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

// RemoveMarker clears the marker, so a worktree whose thread conductor closed
// is no longer treated as T3-hosted.
func RemoveMarker(worktreePath string) {
	_ = os.Remove(markerPath(worktreePath))
}

// HasMarker reports whether T3 is hosting this worktree.
func HasMarker(worktreePath string) bool {
	_, ok := ReadMarker(worktreePath)
	return ok
}

func markerPath(worktreePath string) string {
	return filepath.Join(worktreePath, MarkerFileName)
}
