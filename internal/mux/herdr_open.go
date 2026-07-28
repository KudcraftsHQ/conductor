package mux

import (
	"fmt"

	"github.com/hammashamzah/conductor/internal/codingagent"
)

// HerdrOpenOptions controls which processes Conductor starts in a Herdr
// worktree workspace. With no options, the workspace contains a normal shell.
type HerdrOpenOptions struct {
	// Claude starts an interactive Claude Code session in the root pane.
	Claude bool
	// Prompt runs Claude Code once with --print, without opening an interactive
	// Claude UI. It cannot be combined with Claude.
	Prompt string
	// Dev starts the project's dev server through `conductor run` in a second
	// pane. Conductor resolves the project-specific run script and worktree
	// environment itself.
	Dev bool
}

type herdrOpenPlan struct {
	Label        string
	CreateArgs   []string
	RootLabel    string
	RootCommand  []string
	NeedsDevPane bool
	DevCommand   string
}

func newHerdrOpenPlan(project, branch, worktreePath string, options HerdrOpenOptions) (herdrOpenPlan, error) {
	if options.Claude && options.Prompt != "" {
		return herdrOpenPlan{}, fmt.Errorf("--claude and --prompt cannot be used together")
	}

	plan := herdrOpenPlan{
		Label:      Herdr().WindowName(project, branch),
		CreateArgs: []string{"workspace", "create", "--cwd", worktreePath, "--label", Herdr().WindowName(project, branch), "--focus"},
		RootLabel:  "terminal",
	}

	switch {
	case options.Prompt != "":
		plan.RootLabel = "claude"
		plan.RootCommand = codingagent.ClaudeCode.OneShotArgs(options.Prompt)
	case options.Claude:
		plan.RootLabel = "claude"
		plan.RootCommand = codingagent.ClaudeCode.InteractiveArgs(herdrAgentPrompt())
	}

	if options.Dev {
		plan.NeedsDevPane = true
		plan.DevCommand = "conductor run"
	}

	return plan, nil
}

// OpenHerdrWorktree opens a focused Herdr workspace for a Conductor worktree.
// It is deliberately idempotent for the default open path: an existing
// workspace is focused rather than duplicated.
func OpenHerdrWorktree(project, branch, worktreePath string, options HerdrOpenOptions) error {
	return herdrMux{}.openWorktree(project, branch, worktreePath, options)
}

func (h herdrMux) openWorktree(project, branch, worktreePath string, options HerdrOpenOptions) error {
	plan, err := newHerdrOpenPlan(project, branch, worktreePath, options)
	if err != nil {
		return err
	}

	if id, exists := h.workspaceID(plan.Label); exists {
		return h.run("workspace", "focus", id)
	}

	var created struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := h.runJSON(&created, plan.CreateArgs...); err != nil {
		return fmt.Errorf("failed to create herdr workspace: %w", err)
	}
	rootPane := created.Result.RootPane.PaneID
	if rootPane == "" {
		return fmt.Errorf("herdr did not return a root pane for workspace %q", plan.Label)
	}
	_ = h.run("pane", "rename", rootPane, plan.RootLabel)

	if plan.NeedsDevPane {
		var split struct {
			Result struct {
				Pane struct {
					PaneID string `json:"pane_id"`
				} `json:"pane"`
			} `json:"result"`
		}
		if err := h.runJSON(&split, "pane", "split", rootPane, "--direction", "right", "--cwd", worktreePath, "--no-focus"); err != nil {
			return fmt.Errorf("failed to create herdr dev pane: %w", err)
		}
		if split.Result.Pane.PaneID == "" {
			return fmt.Errorf("herdr did not return a dev pane for workspace %q", plan.Label)
		}
		devPane := split.Result.Pane.PaneID
		_ = h.run("pane", "rename", devPane, "dev")
		if err := h.run("pane", "run", devPane, plan.DevCommand); err != nil {
			return fmt.Errorf("failed to start dev server in herdr pane: %w", err)
		}
	}

	if len(plan.RootCommand) > 0 {
		if err := h.run("pane", "run", rootPane, shellJoin(plan.RootCommand)); err != nil {
			return fmt.Errorf("failed to start Claude in herdr pane: %w", err)
		}
	}
	return nil
}
