package t3

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// LaunchIntentTTL bounds how long a launch intent is honoured. Long enough for
// T3 to prepare the thread and run the setup script; short enough that a
// stale intent cannot turn a later, deliberate `conductor adopt` into a no-op.
const LaunchIntentTTL = 10 * time.Minute

// WriteLaunchIntent records that conductor is about to launch a V2 thread on a
// worktree it is provisioning itself.
//
// Under orchestration V2, conductor binds its thread with launchThread under
// the repository's project, and launchThread runs that project's
// runOnWorktreeCreate script — `conductor adopt` — in the worktree. Adopt on an
// already-registered worktree re-provisions it, which drops and re-clones the
// dev database. The intent tells adopt that this run is the echo of
// conductor's own launch and should only bind.
//
// It lives under ~/.conductor rather than in the worktree so it can never show
// up in git status.
func WriteLaunchIntent(worktreePath string) error {
	path, err := launchIntentPath(worktreePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"+worktreePath+"\n"), 0644)
}

// ConsumeLaunchIntent reports whether a fresh launch intent exists for the
// worktree, and removes it either way.
func ConsumeLaunchIntent(worktreePath string) bool {
	path, err := launchIntentPath(worktreePath)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_ = os.Remove(path)
	first, _, _ := strings.Cut(string(data), "\n")
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(first))
	if err != nil {
		return false
	}
	return time.Since(at) < LaunchIntentTTL
}

func launchIntentPath(worktreePath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(normalizePath(worktreePath)))
	return filepath.Join(home, ".conductor", "t3-launch-intents", hex.EncodeToString(sum[:])), nil
}
