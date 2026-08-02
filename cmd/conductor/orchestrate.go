package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/orchestration"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/spf13/cobra"
)

// The orchestrate command group is the machine-facing half of Conductor: it is
// driven by the Herdr/Hermes bridge on behalf of a chat message, so every
// subcommand can answer in JSON and none of them prompt.
var orchestrateCmd = &cobra.Command{
	Use:   "orchestrate",
	Short: "Run chat-originated tasks in fresh worktrees",
	Long: `Orchestrate turns a chat request into a coding agent running in a brand-new
worktree, and keeps enough state to report honest progress and a single
completion.

The guarantees it exists to provide:

  - the agent always runs in a fresh worktree, never the project's root checkout
  - Claude Code always starts with --dangerously-skip-permissions, since there
    is no human in the loop to answer a permission prompt
  - launch returns once the agent is confirmed working; it never waits for the
    task to finish
  - progress comes from Herdr observations only — nothing is inferred from a timer
  - the same originating message never starts a second agent

Completion is gated on an explicit Readback decision: a task whose request did
not make clear whether it owes a write-up is not reported complete until the
requester answers, and asking never stops the agent or frees the worktree.`,
}

var (
	orchProject      string
	orchPrompt       string
	orchPromptFile   string
	orchRequestID    string
	orchTaskID       string
	orchPlatform     string
	orchChannelID    string
	orchThreadID     string
	orchRequesterID  string
	orchGate         string
	orchJSON         bool
	orchReadyWait    time.Duration
	orchConfirmWait  time.Duration
	orchTestStatus   string
	orchTestDetail   string
	orchReadbackURL  string
	orchReadbackSlug string
)

var orchestrateLaunchCmd = &cobra.Command{
	Use:   "launch",
	Short: "Create a fresh worktree and start a coding agent on a request",
	Long: `Creates a new worktree for the project, starts Claude Code in it with
--dangerously-skip-permissions, delivers the prompt, and returns as soon as the
agent is confirmed working.

--request-id is the originating message id and is required: it is the
idempotency key, so redelivering the same chat message returns the task that is
already running instead of starting a second agent in a second worktree.`,
	RunE: runOrchestrateLaunch,
}

var orchestrateStatusCmd = &cobra.Command{
	Use:   "status <task-id>",
	Short: "Show a task's recorded state and recent progress",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrchestrateStatus,
}

var orchestrateObserveCmd = &cobra.Command{
	Use:   "observe [task-id]",
	Short: "Sample live agent state from Herdr and record any real progress",
	Long: `Reads the agent's live state from Herdr and records a progress event only when
something actually changed. With no task id it samples every task that still
expects progress — which is what a monitor does after a restart, since the
store, not memory, is the list of what is running.

When a finished task's Readback decision is still outstanding, the question to
put to the requester is reported here (once).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runOrchestrateObserve,
}

var orchestrateGateCmd = &cobra.Command{
	Use:   "gate <task-id> <yes|no>",
	Short: "Record the requester's Readback decision",
	Long: `Records whether a task owes a Readback write-up before it can be reported
complete. "yes" (or "required") waits for the document; "no" (or "not-needed")
lets the task complete without one. A decision made here outranks the classifier
and is never re-derived.`,
	Args: cobra.ExactArgs(2),
	RunE: runOrchestrateGate,
}

var orchestrateTestsCmd = &cobra.Command{
	Use:   "tests <task-id>",
	Short: "Record the task's test outcome for the completion message",
	Long: `Conductor does not run the agent's tests. This records what the agent (or a
human) reported, so the completion message can say it. Left unset it stays
"unknown", which is what the completion will say.`,
	Args: cobra.ExactArgs(1),
	RunE: runOrchestrateTests,
}

var orchestrateReadbackCmd = &cobra.Command{
	Use:   "readback <task-id>",
	Short: "Record the published Readback URL for a task",
	Long: `Records the URL the readback CLI printed. The URL is never constructed from a
slug: a fabricated link is indistinguishable, to the person reading the thread,
from the silence this is meant to end.`,
	Args: cobra.ExactArgs(1),
	RunE: runOrchestrateReadback,
}

var orchestrateSummaryCmd = &cobra.Command{
	Use:   "summary <task-id> <text>",
	Short: "Record the one-line outcome the completion message carries",
	Args:  cobra.ExactArgs(2),
	RunE:  runOrchestrateSummary,
}

var orchestrateCompleteCmd = &cobra.Command{
	Use:   "complete <task-id>",
	Short: "Produce the Discord-ready completion, if the task is really done",
	Long: `Evaluates the Readback gate and renders the completion message when everything
is satisfied. Calling it early is harmless: it reports what is outstanding
("the agent is still working", "waiting on the Readback decision") and does not
claim the task is done.`,
	Args: cobra.ExactArgs(1),
	RunE: runOrchestrateComplete,
}

var orchestrateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List orchestrated tasks, newest first",
	RunE:  runOrchestrateList,
}

func init() {
	orchestrateCmd.AddCommand(orchestrateLaunchCmd)
	orchestrateCmd.AddCommand(orchestrateStatusCmd)
	orchestrateCmd.AddCommand(orchestrateObserveCmd)
	orchestrateCmd.AddCommand(orchestrateGateCmd)
	orchestrateCmd.AddCommand(orchestrateTestsCmd)
	orchestrateCmd.AddCommand(orchestrateReadbackCmd)
	orchestrateCmd.AddCommand(orchestrateSummaryCmd)
	orchestrateCmd.AddCommand(orchestrateCompleteCmd)
	orchestrateCmd.AddCommand(orchestrateListCmd)

	f := orchestrateLaunchCmd.Flags()
	f.StringVar(&orchProject, "project", "", "Registered conductor project (required)")
	f.StringVarP(&orchPrompt, "prompt", "p", "", "Task prompt to give the agent")
	f.StringVar(&orchPromptFile, "prompt-file", "", "Read the task prompt from a file ('-' for stdin)")
	f.StringVar(&orchRequestID, "request-id", "", "Originating message id; the idempotency key (required)")
	f.StringVar(&orchTaskID, "task-id", "", "Task id to use (default: derived from project and request id)")
	f.StringVar(&orchPlatform, "platform", "discord", "Originating platform")
	f.StringVar(&orchChannelID, "channel-id", "", "Originating channel id")
	f.StringVar(&orchThreadID, "thread-id", "", "Originating thread id")
	f.StringVar(&orchRequesterID, "requester-id", "", "Originating user id")
	f.StringVar(&orchGate, "readback", "", "Override the Readback gate: required|not-needed|ask")
	f.DurationVar(&orchReadyWait, "ready-timeout", 0, "How long to wait for the agent to become interactive (default 45s)")
	f.DurationVar(&orchConfirmWait, "confirm-timeout", 0, "How long to wait for the agent to start working (default 20s)")

	orchestrateTestsCmd.Flags().StringVar(&orchTestStatus, "status", "", "passed|failed|skipped|unknown (required)")
	orchestrateTestsCmd.Flags().StringVar(&orchTestDetail, "detail", "", "Command or note to quote in the completion")

	orchestrateReadbackCmd.Flags().StringVar(&orchReadbackURL, "url", "", "URL the readback CLI printed (required)")
	orchestrateReadbackCmd.Flags().StringVar(&orchReadbackSlug, "slug", "", "Slug it was pushed under")

	for _, c := range orchestrateCmd.Commands() {
		c.Flags().BoolVar(&orchJSON, "json", false, "Emit JSON for machine callers")
	}
}

// orchestrationDir is where task state lives. Deliberately outside the project
// so it survives archiving the worktree a task ran in.
func orchestrationDir() (string, error) {
	dir, err := config.ConductorDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "orchestration"), nil
}

func openTaskStore() (*orchestration.Store, error) {
	dir, err := orchestrationDir()
	if err != nil {
		return nil, err
	}
	return orchestration.NewStore(dir), nil
}

func newMonitor() (*orchestration.Monitor, error) {
	s, err := openTaskStore()
	if err != nil {
		return nil, err
	}
	return &orchestration.Monitor{Store: s, Herdr: orchestration.NewHerdrCLI()}, nil
}

func runOrchestrateLaunch(cmd *cobra.Command, args []string) error {
	prompt, err := resolvePrompt()
	if err != nil {
		return err
	}
	gate, err := parseGateFlag(orchGate)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	s := store.New(cfg)
	defer func() { _, _ = s.Close() }()

	taskStore, err := openTaskStore()
	if err != nil {
		return err
	}
	launcher := &orchestration.Launcher{
		Store:         taskStore,
		Herdr:         orchestration.NewHerdrCLI(),
		Worktrees:     orchestration.NewConductorWorktrees(cfg, s),
		ReadyBudget:   orchReadyWait,
		ConfirmBudget: orchConfirmWait,
	}

	res, launchErr := launcher.Launch(orchestration.LaunchRequest{
		Project: orchProject,
		Prompt:  prompt,
		TaskID:  orchTaskID,
		Gate:    gate,
		Origin: orchestration.Origin{
			RequestID:   orchRequestID,
			Platform:    orchPlatform,
			ChannelID:   orchChannelID,
			ThreadID:    orchThreadID,
			RequesterID: orchRequesterID,
		},
	})
	if res == nil {
		return launchErr
	}
	if orchJSON {
		if err := emitJSON(map[string]any{
			"task":      res.Task,
			"duplicate": res.Duplicate,
			"confirmed": res.Confirmed,
			"detail":    res.Detail,
			"error":     errString(launchErr),
		}); err != nil {
			return err
		}
		return launchErr
	}

	t := res.Task
	switch {
	case res.Duplicate:
		fmt.Printf("Already launched: %s (%s)\n%s\n", t.ID, t.State, res.Detail)
	case launchErr != nil:
		fmt.Printf("Launch failed: %s\n%s\n", t.ID, res.Detail)
	default:
		fmt.Printf("Launched %s\n", t.ID)
		fmt.Printf("  worktree : %s (branch %s)\n", t.Worktree.Path, t.Worktree.Branch)
		fmt.Printf("  pane     : %s (workspace %s)\n", t.Agent.PaneID, t.Agent.WorkspaceID)
		fmt.Printf("  state    : %s — %s\n", t.State, t.Detail)
		fmt.Printf("  readback : %s\n", t.Gate)
	}
	return launchErr
}

func resolvePrompt() (string, error) {
	if orchPrompt != "" && orchPromptFile != "" {
		return "", fmt.Errorf("--prompt and --prompt-file cannot be used together")
	}
	if orchPromptFile == "" {
		return orchPrompt, nil
	}
	if orchPromptFile == "-" {
		data, err := os.ReadFile("/dev/stdin")
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
	data, err := os.ReadFile(orchPromptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func parseGateFlag(v string) (orchestration.ReadbackGate, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "", nil
	case "required", "yes", "readback_required":
		return orchestration.GateRequired, nil
	case "not-needed", "none", "no", "no_readback_needed":
		return orchestration.GateNotNeeded, nil
	case "ask", "undecided", "awaiting_readback_decision":
		return orchestration.GateUndecided, nil
	}
	return "", fmt.Errorf("unknown --readback value %q (use required, not-needed or ask)", v)
}

func runOrchestrateStatus(cmd *cobra.Command, args []string) error {
	s, err := openTaskStore()
	if err != nil {
		return err
	}
	t, err := s.Get(args[0])
	if err != nil {
		return err
	}
	if orchJSON {
		return emitJSON(t)
	}
	printTask(t)
	if len(t.Progress) > 0 {
		fmt.Println("\nrecent progress:")
		start := len(t.Progress) - 10
		if start < 0 {
			start = 0
		}
		for _, ev := range t.Progress[start:] {
			fmt.Printf("  %s  %-9s %s\n",
				ev.At.Format("15:04:05"), ev.Kind, ev.Detail)
		}
	}
	return nil
}

func runOrchestrateObserve(cmd *cobra.Command, args []string) error {
	m, err := newMonitor()
	if err != nil {
		return err
	}
	var observations []*orchestration.Observation
	if len(args) == 1 {
		obs, err := m.Observe(args[0])
		if err != nil {
			return err
		}
		observations = []*orchestration.Observation{obs}
	} else {
		observations, err = m.ObserveAll()
		if err != nil {
			return err
		}
	}
	if orchJSON {
		return emitJSON(observations)
	}
	if len(observations) == 0 {
		fmt.Println("No tasks are waiting on agent progress.")
		return nil
	}
	for _, obs := range observations {
		marker := " "
		if obs.Changed {
			marker = "*"
		}
		fmt.Printf("%s %-28s %-28s %s\n", marker, obs.Task.ID, obs.Task.State, obs.Detail)
		if obs.AskReadback != "" {
			fmt.Printf("\n  --- ask the requester ---\n%s\n\n", indent(obs.AskReadback, "  "))
		}
		if obs.ReadyToComplete {
			fmt.Printf("  ready to complete: conductor orchestrate complete %s\n", obs.Task.ID)
		}
	}
	return nil
}

func runOrchestrateGate(cmd *cobra.Command, args []string) error {
	gate, ok := orchestration.ParseGateDecision(args[1])
	if !ok {
		return fmt.Errorf(
			"could not read %q as a Readback decision; use yes/required or no/not-needed", args[1])
	}
	m, err := newMonitor()
	if err != nil {
		return err
	}
	t, err := m.ResolveGate(args[0], gate)
	if err != nil {
		return err
	}
	if orchJSON {
		return emitJSON(t)
	}
	fmt.Printf("%s: readback gate is now %s (state %s)\n", t.ID, t.Gate, t.State)
	return nil
}

func runOrchestrateTests(cmd *cobra.Command, args []string) error {
	if orchTestStatus == "" {
		return fmt.Errorf("--status is required (passed, failed, skipped or unknown)")
	}
	m, err := newMonitor()
	if err != nil {
		return err
	}
	t, err := m.RecordTests(args[0],
		orchestration.TestStatus(strings.ToLower(orchTestStatus)), orchTestDetail)
	if err != nil {
		return err
	}
	if orchJSON {
		return emitJSON(t)
	}
	fmt.Printf("%s: tests recorded as %s\n", t.ID, t.Tests)
	return nil
}

func runOrchestrateReadback(cmd *cobra.Command, args []string) error {
	if orchReadbackURL == "" {
		return fmt.Errorf("--url is required; pass the URL the readback CLI printed")
	}
	m, err := newMonitor()
	if err != nil {
		return err
	}
	t, err := m.RecordReadback(args[0], orchReadbackSlug, orchReadbackURL)
	if err != nil {
		return err
	}
	if orchJSON {
		return emitJSON(t)
	}
	fmt.Printf("%s: readback recorded (%s), state %s\n", t.ID, t.Readback.URL, t.State)
	return nil
}

func runOrchestrateSummary(cmd *cobra.Command, args []string) error {
	m, err := newMonitor()
	if err != nil {
		return err
	}
	t, err := m.RecordSummary(args[0], args[1])
	if err != nil {
		return err
	}
	if orchJSON {
		return emitJSON(t)
	}
	fmt.Printf("%s: summary recorded\n", t.ID)
	return nil
}

func runOrchestrateComplete(cmd *cobra.Command, args []string) error {
	m, err := newMonitor()
	if err != nil {
		return err
	}
	c, err := m.Complete(args[0])
	if err != nil {
		return err
	}
	if orchJSON {
		return emitJSON(map[string]any{
			"ready":       c.Ready,
			"reason":      c.Reason,
			"message":     c.Message,
			"askReadback": c.AskReadback,
			"task":        c.Task,
		})
	}
	if !c.Ready {
		fmt.Printf("Not complete: %s\n", c.Reason)
		if c.Message != "" {
			fmt.Printf("\n%s\n", c.Message)
		}
		return nil
	}
	fmt.Println(c.Message)
	return nil
}

func runOrchestrateList(cmd *cobra.Command, args []string) error {
	s, err := openTaskStore()
	if err != nil {
		return err
	}
	tasks, err := s.List()
	if err != nil {
		return err
	}
	if orchJSON {
		return emitJSON(tasks)
	}
	if len(tasks) == 0 {
		fmt.Println("No orchestrated tasks yet.")
		return nil
	}
	fmt.Printf("%-28s %-12s %-28s %-26s %s\n", "TASK", "PROJECT", "STATE", "READBACK", "BRANCH")
	for _, t := range tasks {
		fmt.Printf("%-28s %-12s %-28s %-26s %s\n",
			t.ID, t.Project, t.State, t.Gate, t.Worktree.Branch)
	}
	return nil
}

func printTask(t *orchestration.Task) {
	fmt.Printf("task     : %s\n", t.ID)
	fmt.Printf("project  : %s\n", t.Project)
	fmt.Printf("state    : %s — %s\n", t.State, t.Detail)
	fmt.Printf("readback : %s (decided by %s)\n", t.Gate, t.GateDecidedBy)
	if t.Readback.URL != "" {
		fmt.Printf("report   : %s\n", t.Readback.URL)
	}
	fmt.Printf("tests    : %s %s\n", t.Tests, t.TestDetail)
	fmt.Printf("worktree : %s (branch %s)\n", t.Worktree.Path, t.Worktree.Branch)
	fmt.Printf("agent    : pane %s terminal %s\n", t.Agent.PaneID, t.Agent.TerminalID)
	fmt.Printf("origin   : %s message %s\n", t.Origin.Platform, t.Origin.RequestID)
	if !t.LastObservedAt.IsZero() {
		fmt.Printf("last seen: %s (status %s, seq %d)\n",
			t.LastObservedAt.Format(time.RFC3339), t.LastStatus, t.LastSeq)
	}
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
