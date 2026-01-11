// Package testing provides test utilities for the TUI
package testing

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ModelInterface defines the interface that TUI models must implement for testing
type ModelInterface interface {
	tea.Model
	// Additional methods from tui.Model that we need for testing
}

// Harness provides a test environment for the TUI without needing a real terminal
type Harness struct {
	t       *testing.T
	model   tea.Model
	history []ViewSnapshot
	ctx     context.Context
	cancel  context.CancelFunc
}

// ViewSnapshot captures the state at a point in time
type ViewSnapshot struct {
	View      string
	Timestamp time.Time
}

// NewHarness creates a new test harness with a model
func NewHarness(t *testing.T, model tea.Model) *Harness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	return &Harness{
		t:       t,
		model:   model,
		history: make([]ViewSnapshot, 0),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// SendKey simulates a key press and returns the command (if any)
func (h *Harness) SendKey(key tea.KeyMsg) tea.Cmd {
	h.t.Helper()
	newModel, cmd := h.model.Update(key)
	h.model = newModel
	h.recordSnapshot()
	return cmd
}

// SendRune sends a single rune key press
func (h *Harness) SendRune(r rune) tea.Cmd {
	return h.SendKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

// SendString sends a string of characters as individual key presses
func (h *Harness) SendString(s string) {
	h.t.Helper()
	for _, r := range s {
		h.SendRune(r)
	}
}

// SendEnter sends the Enter key
func (h *Harness) SendEnter() tea.Cmd {
	return h.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
}

// SendEsc sends the Escape key
func (h *Harness) SendEsc() tea.Cmd {
	return h.SendKey(tea.KeyMsg{Type: tea.KeyEsc})
}

// SendTab sends the Tab key
func (h *Harness) SendTab() tea.Cmd {
	return h.SendKey(tea.KeyMsg{Type: tea.KeyTab})
}

// SendBackspace sends the Backspace key
func (h *Harness) SendBackspace() tea.Cmd {
	return h.SendKey(tea.KeyMsg{Type: tea.KeyBackspace})
}

// SendUp sends the Up arrow key
func (h *Harness) SendUp() tea.Cmd {
	return h.SendKey(tea.KeyMsg{Type: tea.KeyUp})
}

// SendDown sends the Down arrow key
func (h *Harness) SendDown() tea.Cmd {
	return h.SendKey(tea.KeyMsg{Type: tea.KeyDown})
}

// SendCtrlC sends Ctrl+C
func (h *Harness) SendCtrlC() tea.Cmd {
	return h.SendKey(tea.KeyMsg{Type: tea.KeyCtrlC})
}

// SendMessage sends a tea.Msg to the model
func (h *Harness) SendMessage(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	newModel, cmd := h.model.Update(msg)
	h.model = newModel
	h.recordSnapshot()
	return cmd
}

// SendWindowSize sends a window size message
func (h *Harness) SendWindowSize(width, height int) tea.Cmd {
	return h.SendMessage(tea.WindowSizeMsg{Width: width, Height: height})
}

// ProcessCommand executes a command and sends the result back to the model
func (h *Harness) ProcessCommand(cmd tea.Cmd) tea.Msg {
	h.t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg != nil {
		h.SendMessage(msg)
	}
	return msg
}

// ProcessCommandChain processes commands until no more commands are returned
// Use with caution - can infinite loop if commands keep returning new commands
func (h *Harness) ProcessCommandChain(cmd tea.Cmd, maxDepth int) {
	h.t.Helper()
	for i := 0; i < maxDepth && cmd != nil; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		newModel, newCmd := h.model.Update(msg)
		h.model = newModel
		h.recordSnapshot()
		cmd = newCmd
	}
}

// recordSnapshot captures the current view
func (h *Harness) recordSnapshot() {
	h.history = append(h.history, ViewSnapshot{
		View:      h.model.View(),
		Timestamp: time.Now(),
	})
}

// GetModel returns the current model
func (h *Harness) GetModel() tea.Model {
	return h.model
}

// View returns the current view output
func (h *Harness) View() string {
	return h.model.View()
}

// GetHistory returns all recorded view snapshots
func (h *Harness) GetHistory() []ViewSnapshot {
	return h.history
}

// AssertViewContains checks if the current view contains the given text
func (h *Harness) AssertViewContains(text string) {
	h.t.Helper()
	view := h.model.View()
	if !strings.Contains(view, text) {
		h.t.Errorf("view does not contain %q\nActual view:\n%s", text, view)
	}
}

// AssertViewNotContains checks if the current view does NOT contain the given text
func (h *Harness) AssertViewNotContains(text string) {
	h.t.Helper()
	view := h.model.View()
	if strings.Contains(view, text) {
		h.t.Errorf("view should not contain %q\nActual view:\n%s", text, view)
	}
}

// AssertViewMatches checks if the current view matches exactly
func (h *Harness) AssertViewMatches(expected string) {
	h.t.Helper()
	view := h.model.View()
	if view != expected {
		h.t.Errorf("view does not match expected\nExpected:\n%s\n\nActual:\n%s", expected, view)
	}
}

// WaitFor polls until a condition is met or timeout
func (h *Harness) WaitFor(condition func(tea.Model) bool, timeout time.Duration) bool {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition(h.model) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Cleanup releases resources
func (h *Harness) Cleanup() {
	h.cancel()
}

// KeyMsg creates a tea.KeyMsg from a string
func KeyMsg(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	// Handle special keys
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}
