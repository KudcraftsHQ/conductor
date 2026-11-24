//go:build unix

package lifecycle

import (
	"os/exec"
	"syscall"
)

// detach puts cmd in its own session, so an install started by the
// SessionStart hook outlives the hook (and the session) if it runs long.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
