package agent

import "testing"

func TestIsShellCommand(t *testing.T) {
	shells := []string{"", "bash", "zsh", "fish", "sh"}
	for _, s := range shells {
		if !isShellCommand(s) {
			t.Errorf("isShellCommand(%q) = false, want true", s)
		}
	}
	agents := []string{"claude", "codex", "opencode", "node"}
	for _, a := range agents {
		if isShellCommand(a) {
			t.Errorf("isShellCommand(%q) = true, want false", a)
		}
	}
}
