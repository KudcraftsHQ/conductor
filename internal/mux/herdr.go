package mux

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/session"
)

// herdrSessionName is the named herdr session conductor drives.
const herdrSessionName = "conductor"

// herdrMux drives herdr (https://herdr.dev) over its CLI, which speaks the same
// socket API as its TUI and returns JSON on stdout.
//
// Model mapping: a conductor worktree window is a herdr *workspace* labelled
// "project/branch", holding the coding agent pane (left) and the dev server
// pane (right).
type herdrMux struct{}

// Herdr returns the herdr-backed Multiplexer.
func Herdr() Multiplexer { return herdrMux{} }

func (herdrMux) Kind() Kind { return KindHerdr }

func (herdrMux) CheckInstalled() error {
	if _, err := exec.LookPath("herdr"); err != nil {
		return fmt.Errorf("herdr is required but not installed")
	}
	return nil
}

func (herdrMux) InstallGuide() string {
	return `herdr is not installed.

Install it with:
  curl -fsSL https://herdr.dev/install.sh | sh

Or see https://herdr.dev/docs for other options.`
}

// IsInsideSession reports whether we are running inside a herdr pane. herdr
// exports HERDR_PANE_ID into every pane it spawns.
func (herdrMux) IsInsideSession() bool {
	return os.Getenv("HERDR_PANE_ID") != ""
}

// IsInsideConductorSession reports whether we are inside conductor's own herdr
// session. HERDR_SESSION carries the session name.
func (h herdrMux) IsInsideConductorSession() bool {
	if !h.IsInsideSession() {
		return false
	}
	name := os.Getenv("HERDR_SESSION")
	// The default session reports an empty name; treat it as conductor's only
	// when we did not ask for a named one.
	return name == herdrSessionName || name == ""
}

func (herdrMux) SessionName() string { return herdrSessionName }

// StartSession replaces the current process with a herdr client attached to
// conductor's named session.
func (herdrMux) StartSession() error {
	bin, err := exec.LookPath("herdr")
	if err != nil {
		return fmt.Errorf("herdr not found: %w", err)
	}
	args := []string{"herdr", "--session", herdrSessionName}
	return syscall.Exec(bin, args, os.Environ())
}

// DetachSession is a no-op: herdr clients detach from the UI, and the server
// keeps panes running regardless. There is no CLI verb to detach a client.
func (herdrMux) DetachSession() error { return nil }

func (herdrMux) WindowName(project, branch string) string {
	return fmt.Sprintf("%s/%s", project, branch)
}

func (h herdrMux) WindowExists(project, branch string) bool {
	_, ok := h.workspaceID(h.WindowName(project, branch))
	return ok
}

func (h herdrMux) ListWindowNames() []string {
	var out struct {
		Result struct {
			Workspaces []struct {
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := h.runJSON(&out, "workspace", "list"); err != nil {
		return nil
	}
	names := make([]string, 0, len(out.Result.Workspaces))
	for _, ws := range out.Result.Workspaces {
		names = append(names, ws.Label)
	}
	return names
}

func (h herdrMux) CreateCodingWindow(project, branch, worktreePath string, agent codingagent.Agent) error {
	return h.createWindow(project, branch, worktreePath, agent, agent.InteractiveArgs(herdrAgentPrompt()), "")
}

func (h herdrMux) CreateCodingWindowWithTask(project, branch, worktreePath, taskPrompt string, agent codingagent.Agent) error {
	return h.createWindow(project, branch, worktreePath, agent,
		agent.TaskArgs(herdrAgentPrompt(), taskPrompt), " (agent)")
}

// createWindow builds the worktree workspace: agent pane on the left, dev
// server pane on the right.
func (h herdrMux) createWindow(project, branch, worktreePath string, agent codingagent.Agent, agentArgs []string, labelSuffix string) error {
	label := h.WindowName(project, branch)
	if _, exists := h.workspaceID(label); exists {
		return fmt.Errorf("workspace %q already exists", label)
	}

	systemPrompt := herdrAgentPrompt()
	if agent.UsesContextFile() {
		if err := codingagent.WriteContextFile(worktreePath, systemPrompt); err != nil {
			return fmt.Errorf("failed to write agent context file: %w", err)
		}
	}

	var created struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := h.runJSON(&created, "workspace", "create",
		"--cwd", worktreePath, "--label", label, "--no-focus"); err != nil {
		return fmt.Errorf("failed to create herdr workspace: %w", err)
	}
	agentPane := created.Result.RootPane.PaneID
	if agentPane == "" {
		return fmt.Errorf("herdr did not return a root pane for workspace %q", label)
	}

	// Dev server pane to the right of the agent pane.
	var split struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := h.runJSON(&split, "pane", "split", agentPane,
		"--direction", "right", "--cwd", worktreePath, "--no-focus"); err != nil {
		return fmt.Errorf("failed to split herdr pane: %w", err)
	}
	devPane := split.Result.Pane.PaneID

	_ = h.run("pane", "rename", agentPane, branch+" - "+agent.PaneLabel()+labelSuffix)
	if devPane != "" {
		_ = h.run("pane", "rename", devPane, "dev")
		_ = h.run("pane", "run", devPane, herdrDevCommand)
	}

	// Start the agent last so it is the pane the user lands on.
	if err := h.run("pane", "run", agentPane, shellJoin(agentArgs)); err != nil {
		return fmt.Errorf("failed to start agent in herdr pane: %w", err)
	}
	return nil
}

func (h herdrMux) KillWindow(project, branch string) error {
	id, ok := h.workspaceID(h.WindowName(project, branch))
	if !ok {
		return nil
	}
	return h.run("workspace", "close", id)
}

func (h herdrMux) FocusWindow(project, branch string) error {
	id, ok := h.workspaceID(h.WindowName(project, branch))
	if !ok {
		return fmt.Errorf("no herdr workspace for %s/%s", project, branch)
	}
	return h.run("workspace", "focus", id)
}

// KillOtherWindows closes every workspace except the focused one.
func (h herdrMux) KillOtherWindows() {
	var out struct {
		Result struct {
			Workspaces []struct {
				WorkspaceID string `json:"workspace_id"`
				Focused     bool   `json:"focused"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := h.runJSON(&out, "workspace", "list"); err != nil {
		return
	}
	for _, ws := range out.Result.Workspaces {
		if !ws.Focused {
			_ = h.run("workspace", "close", ws.WorkspaceID)
		}
	}
}

// StartAgentPane creates a dedicated workspace labelled windowName so that
// WindowExists and KillWindow can address it later. `herdr agent start` alone
// would place the pane in the focused workspace, leaving it unaddressable.
func (h herdrMux) StartAgentPane(windowName, workDir string, argv []string, paneTitle string) (string, error) {
	if _, exists := h.workspaceID(windowName); exists {
		return "", fmt.Errorf("workspace %q already exists", windowName)
	}

	var created struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := h.runJSON(&created, "workspace", "create",
		"--cwd", workDir, "--label", windowName, "--no-focus"); err != nil {
		return "", fmt.Errorf("failed to create herdr workspace: %w", err)
	}
	paneID := created.Result.RootPane.PaneID
	if paneID == "" {
		return "", fmt.Errorf("herdr did not return a pane id for %q", windowName)
	}

	if paneTitle != "" {
		_ = h.run("pane", "rename", paneID, paneTitle)
	}
	if err := h.run("pane", "run", paneID, shellJoin(argv)); err != nil {
		return "", fmt.Errorf("failed to start agent in herdr pane: %w", err)
	}
	return paneID, nil
}

func (h herdrMux) PaneExists(paneID string) bool {
	return h.run("pane", "get", paneID) == nil
}

// GetPaneCommand returns the detected agent running in a pane. herdr reports a
// detected agent label rather than a raw process name, and detection takes a
// moment after a pane starts — so an empty result means "no agent detected
// (yet)", not necessarily "the agent exited". Callers must not treat the first
// empty reading as completion; see monitorCompletion in internal/agent.
func (h herdrMux) GetPaneCommand(paneID string) string {
	var out struct {
		Result struct {
			Pane struct {
				Agent string `json:"agent"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := h.runJSON(&out, "pane", "get", paneID); err != nil {
		return ""
	}
	return out.Result.Pane.Agent
}

// UpdateTabTitles is a no-op: herdr detects and renders agent status itself.
func (herdrMux) UpdateTabTitles([]*session.Session) {}

func (herdrMux) TracksAgentStatus() bool { return true }

// --- helpers ---

// workspaceID resolves a workspace label to its id.
func (h herdrMux) workspaceID(label string) (string, bool) {
	var out struct {
		Result struct {
			Workspaces []struct {
				WorkspaceID string `json:"workspace_id"`
				Label       string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := h.runJSON(&out, "workspace", "list"); err != nil {
		return "", false
	}
	for _, ws := range out.Result.Workspaces {
		if ws.Label == label {
			return ws.WorkspaceID, true
		}
	}
	return "", false
}

func (herdrMux) run(args ...string) error {
	return exec.Command("herdr", args...).Run()
}

// runJSON runs a herdr CLI command and decodes its JSON response into v.
func (herdrMux) runJSON(v any, args ...string) error {
	out, err := exec.Command("herdr", args...).Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

// ShellJoin renders argv as a single shell command line for `herdr pane run`,
// for callers outside this package that drive herdr panes directly.
func ShellJoin(argv []string) string { return shellJoin(argv) }

// HerdrAgentPrompt is the system prompt handed to agents started in a herdr
// worktree workspace, exported for the orchestration launch path.
func HerdrAgentPrompt() string { return herdrAgentPrompt() }

// shellJoin renders argv as a single shell command line for `herdr pane run`,
// which takes command text rather than an argv vector.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote single-quotes a value for POSIX shells.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`&|;<>()*?[]#~=%{}!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// herdrDevCommand keeps the dev server pane alive across restarts, mirroring
// the tmux implementation.
const herdrDevCommand = `trap '' INT; while true; do conductor run; ec=$?; echo ''; if [ $ec -eq 130 ]; then echo 'Dev server stopped. Press Enter to restart or type command...'; else echo 'Dev server exited. Press Enter to restart or type command...'; fi; read -r cmd; [ -n "$cmd" ] && eval "$cmd" || continue; done`

// herdrAgentPrompt is the system prompt handed to coding agents so they can
// drive the dev server pane through herdr instead of tmux.
func herdrAgentPrompt() string {
	return `## Conductor Herdr Integration

This workspace uses conductor with herdr panes:
- Left pane: Coding agent (you are here)
- Right pane: Dev server

Your own pane id is in $HERDR_PANE_ID. To find the dev server pane, run:
  herdr pane list --workspace "$HERDR_ACTIVE_WORKSPACE_ID"
and pick the pane whose label is "dev".

### Dev Server Management
- To view dev server logs: herdr pane read <dev-pane> --source recent --lines 50
- To kill the dev server: herdr pane send-keys <dev-pane> C-c
- To restart the dev server: herdr pane run <dev-pane> 'conductor run'
- IMPORTANT: Only run dev server commands in the dev pane, never in this pane`
}
