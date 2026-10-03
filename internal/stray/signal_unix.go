//go:build !windows

package stray

import "syscall"

func terminate(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }

func forceKill(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
