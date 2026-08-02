package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// AgentInfo is the subset of Herdr's agent record this package relies on.
// Nothing here is inferred: every field is read back from `herdr agent get`.
type AgentInfo struct {
	PaneID           string `json:"pane_id"`
	WorkspaceID      string `json:"workspace_id"`
	TerminalID       string `json:"terminal_id"`
	Agent            string `json:"agent"`
	Name             string `json:"name"`
	Status           string `json:"agent_status"`
	StateChangeSeq   int64  `json:"state_change_seq"`
	InteractiveReady bool   `json:"interactive_ready"`
	CWD              string `json:"cwd"`
	Title            string `json:"terminal_title_stripped"`
}

// Herdr is the monitoring and control boundary. Everything this package knows
// about a running agent comes through this interface, which is also what makes
// the launch and monitor paths testable without a terminal.
type Herdr interface {
	// CreateWorkspace makes a workspace labelled label rooted at cwd and
	// returns its root pane.
	CreateWorkspace(label, cwd string) (paneID, workspaceID string, err error)
	// FindWorkspace resolves a label to a workspace id.
	FindWorkspace(label string) (string, bool)
	// RenamePane sets a pane's title.
	RenamePane(paneID, title string) error
	// RunInPane runs a shell command line in a pane.
	RunInPane(paneID, command string) error
	// GetAgent reads the live agent record for a pane.
	GetAgent(target string) (*AgentInfo, error)
	// RenameAgent gives the agent a stable name, so a human scanning Herdr can
	// tell which pane belongs to which chat request.
	RenameAgent(target, name string) error
	// Prompt delivers text to an agent's composer. Acknowledgement is not
	// evidence of submission; callers verify separately.
	Prompt(target, text string) error
	// SendKeys presses keys in a pane. Used for exactly one Enter, on a pane
	// this package created and nobody else has typed into.
	SendKeys(target string, keys ...string) error
}

// ErrAgentGone is returned when Herdr has no agent for a target — the pane was
// closed, or the server restarted. It is a recovery signal, never a failure to
// retry the work into a new agent.
var ErrAgentGone = errors.New("herdr has no agent for that target")

// herdrCLI drives the real `herdr` binary. It speaks the same socket API as the
// TUI and answers in JSON on stdout.
type herdrCLI struct{}

// NewHerdrCLI returns the production Herdr client.
func NewHerdrCLI() Herdr { return herdrCLI{} }

func (herdrCLI) run(args ...string) error {
	out, err := exec.Command("herdr", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (herdrCLI) runJSON(v any, args ...string) error {
	out, err := exec.Command("herdr", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("herdr %s: %w: %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("herdr %s: %w", strings.Join(args, " "), err)
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("herdr %s returned unreadable JSON: %w",
			strings.Join(args, " "), err)
	}
	return nil
}

func (h herdrCLI) CreateWorkspace(label, cwd string) (string, string, error) {
	var created struct {
		Result struct {
			WorkspaceID string `json:"workspace_id"`
			RootPane    struct {
				PaneID      string `json:"pane_id"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := h.runJSON(&created, "workspace", "create",
		"--cwd", cwd, "--label", label, "--no-focus"); err != nil {
		return "", "", err
	}
	pane := created.Result.RootPane.PaneID
	if pane == "" {
		return "", "", fmt.Errorf("herdr created workspace %q without a root pane", label)
	}
	ws := created.Result.WorkspaceID
	if ws == "" {
		ws = created.Result.RootPane.WorkspaceID
	}
	if ws == "" {
		ws = workspaceOfPane(pane)
	}
	return pane, ws, nil
}

func (h herdrCLI) FindWorkspace(label string) (string, bool) {
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

func (h herdrCLI) RenamePane(paneID, title string) error {
	return h.run("pane", "rename", paneID, title)
}

func (h herdrCLI) RunInPane(paneID, command string) error {
	return h.run("pane", "run", paneID, command)
}

func (h herdrCLI) GetAgent(target string) (*AgentInfo, error) {
	var out struct {
		Result struct {
			Agent *AgentInfo `json:"agent"`
		} `json:"result"`
	}
	if err := h.runJSON(&out, "agent", "get", target); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrAgentGone, target, err)
	}
	if out.Result.Agent == nil || out.Result.Agent.PaneID == "" {
		return nil, fmt.Errorf("%w: %s", ErrAgentGone, target)
	}
	return out.Result.Agent, nil
}

func (h herdrCLI) RenameAgent(target, name string) error {
	return h.run("agent", "rename", target, name)
}

// Prompt submits without --wait on purpose: --wait makes Herdr block for up to
// five seconds looking for a state change and then answer `agent_prompt_stalled`
// on an ambiguous outcome. This package would rather do its own bounded,
// interruptible verification than inherit a blocking call whose failure mode is
// "probably delivered".
func (h herdrCLI) Prompt(target, text string) error {
	return h.run("agent", "prompt", target, text)
}

func (h herdrCLI) SendKeys(target string, keys ...string) error {
	args := append([]string{"agent", "send-keys", target}, keys...)
	return h.run(args...)
}

// workspaceOfPane derives "w1D" from "w1D:p1". Only a fallback: Herdr normally
// reports the workspace id outright.
func workspaceOfPane(paneID string) string {
	if i := strings.Index(paneID, ":"); i > 0 {
		return paneID[:i]
	}
	return ""
}
