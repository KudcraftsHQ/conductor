//go:build windows

package stray

import "os"

// Windows has no SIGTERM; a dev server there gets no graceful shutdown.
func terminate(pid int) { forceKill(pid) }

func forceKill(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// FindProcess opens a handle on Windows, so it fails for a pid that is gone.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
