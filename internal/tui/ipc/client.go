package ipc

import (
	"net"
	"sync"
	"time"
)

var (
	// disabled is set to true when running inside the TUI process
	// to prevent the TUI from sending IPC notifications to itself
	disabled   bool
	disabledMu sync.RWMutex
)

// DisableNotifications disables IPC notifications (call this when running inside TUI)
func DisableNotifications() {
	disabledMu.Lock()
	disabled = true
	disabledMu.Unlock()
}

// EnableNotifications enables IPC notifications
func EnableNotifications() {
	disabledMu.Lock()
	disabled = false
	disabledMu.Unlock()
}

// NotifyTUI sends a refresh signal to a running TUI
// Returns nil if TUI is not running (socket doesn't exist)
func NotifyTUI() error {
	// Skip if notifications are disabled (we're inside the TUI)
	disabledMu.RLock()
	if disabled {
		disabledMu.RUnlock()
		return nil
	}
	disabledMu.RUnlock()

	sockPath, err := SocketPath()
	if err != nil {
		return nil // Silently ignore
	}

	conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
	if err != nil {
		return nil // TUI not running, that's OK
	}
	defer func() { _ = conn.Close() }()

	_, _ = conn.Write([]byte("refresh"))
	return nil
}
