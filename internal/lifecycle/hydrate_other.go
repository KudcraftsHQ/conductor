//go:build !unix

package lifecycle

import "os/exec"

func detach(*exec.Cmd) {}
