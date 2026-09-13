package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/mux"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/stray"
	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/hammashamzah/conductor/internal/tmux"
	"github.com/hammashamzah/conductor/internal/workspace"
)

var t3Cmd = &cobra.Command{
	Use:   "t3",
	Short: "Manage the T3 Code integration",
	Long: `Manage the T3 Code integration.

Conductor can host worktree agent sessions in T3 Code instead of a terminal
multiplexer. Each worktree becomes a T3 thread, while its dev server stays in a
tmux window so that it survives T3 Code restarts.

Select it per-command with:
  CONDUCTOR_MUX=t3 conductor worktree create my-feature

Or make it the default by setting "multiplexer": "t3" under "defaults" in
~/.conductor/config.json. Conductor also picks T3 automatically when it is
running inside a T3 terminal.

Conductor needs a bearer token from the T3 server:
  t3 auth session issue --label conductor --ttl 365d --token-only > ~/.conductor/t3-token
  chmod 600 ~/.conductor/t3-token`,
}

var t3StatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show T3 Code connectivity and the worktrees it is hosting",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := t3.New()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
		defer cancel()

		if err := client.Ping(ctx); err != nil {
			return err
		}
		fmt.Printf("T3 Code:  %s (connected)\n\n", client.Origin)

		snapshot, err := client.Shell(ctx)
		if err != nil {
			return err
		}
		threads := snapshot.LiveThreadsWithWorktrees()
		if len(threads) == 0 {
			fmt.Println("No worktree-bound threads.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "THREAD\tBRANCH\tWORKTREE")
		for _, thread := range threads {
			branch := ""
			if thread.Branch != nil {
				branch = *thread.Branch
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", truncateTitle(thread.Title, 40), branch, thread.Worktree())
		}
		return w.Flush()
	},
}

var t3TokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Show where conductor reads its T3 Code token from",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := t3.TokenPath()
		if err != nil {
			return err
		}
		fmt.Printf("Token file: %s\n\n", path)

		if _, err := t3.DiscoverToken(); err != nil {
			fmt.Println(err)
			return nil
		}

		client, err := t3.New()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		if err := client.Ping(ctx); err != nil {
			fmt.Printf("Token present but rejected: %v\n", err)
			return nil
		}
		fmt.Printf("Token is valid for %s\n", client.Origin)
		return nil
	},
}

var t3SendMessage string

var t3SendCmd = &cobra.Command{
	Use:   "send <project> <worktree>",
	Short: "Send chat input to a worktree's T3 thread",
	Long: `Send chat input to the T3 thread bound to a worktree.

This is the API equivalent of typing into the composer and pressing enter, so
external tools can drive a thread without a browser.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(t3SendMessage) == "" {
			return fmt.Errorf("a message is required (--message)")
		}

		worktreePath, err := config.ResolveWorktreePath(args[0], args[1])
		if err != nil {
			return err
		}
		client, err := t3.New()
		if err != nil {
			return err
		}
		// Long enough to cover starting a provider session, which the wait below
		// blocks on; the 30s this used to allow was shorter than a cold start.
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()

		snapshot, err := client.Shell(ctx)
		if err != nil {
			return err
		}
		thread, ok := snapshot.FindThreadByWorktree(worktreePath)
		if !ok {
			return fmt.Errorf("no live T3 thread is bound to %s", worktreePath)
		}
		if err := client.StartTurn(ctx, thread.ID, t3SendMessage, ""); err != nil {
			return err
		}
		// Dispatching is not running: the provider session starts afterwards and
		// reports refusals only on the thread. Reporting "Sent" without checking
		// is how a thread that never ran looked like a success.
		if err := client.WaitForTurn(ctx, thread.ID, 45*time.Second); err != nil {
			return err
		}
		fmt.Printf("Sent to %s/thread/%s\n", client.Origin, thread.ID)
		return nil
	},
}

var (
	t3LogLines  int
	t3LogFollow bool
)

var t3LogsCmd = &cobra.Command{
	Use:   "logs [project] [branch]",
	Short: "Read the dev server logs for a worktree",
	Long: `Read the dev server logs for a worktree.

The dev server runs in a tmux window rather than in the T3 thread, so that it
survives T3 Code restarts, and so that every thread bound to the worktree reads
the same server rather than each starting its own. This reads that window,
which is how an agent working in a thread — or hermes — gets at the output.

With no arguments it reads the dev server for the worktree you are standing in,
which is what lets a project's t3.json name the command without knowing which
worktree it will run in.`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, worktree, err := resolveLogTarget(args)
		if err != nil {
			return err
		}
		if !tmux.WindowExists(project, worktree) {
			return fmt.Errorf("no dev server window for %s/%s (is the worktree open?)", project, worktree)
		}
		target := fmt.Sprintf("%s:%s", tmux.SessionName, tmux.WindowName(project, worktree))

		if t3LogFollow {
			// tmux has no native follow, so poll the pane and print what is new.
			return followPane(cmd.Context(), target, t3LogLines)
		}

		out, err := exec.Command("tmux", "capture-pane", "-p",
			"-S", fmt.Sprintf("-%d", t3LogLines), "-t", target).Output()
		if err != nil {
			return fmt.Errorf("failed to read the dev server window: %w", err)
		}
		_, err = os.Stdout.Write(out)
		return err
	},
}

// followPane prints new dev server output as it appears. It compares whole
// captures rather than tailing a file, because a tmux pane is a screen buffer
// and has no file behind it.
func followPane(ctx context.Context, target string, lines int) error {
	var previous string
	for {
		out, err := exec.Command("tmux", "capture-pane", "-p",
			"-S", fmt.Sprintf("-%d", lines), "-t", target).Output()
		if err != nil {
			return fmt.Errorf("dev server window went away: %w", err)
		}
		current := string(out)
		if current != previous {
			if previous != "" && strings.HasPrefix(current, previous) {
				fmt.Print(current[len(previous):]) // Only the new tail.
			} else {
				fmt.Print(current)
			}
			previous = current
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

var (
	t3ReconcileArchive     bool
	t3ReconcileDev         bool
	t3ReconcileStopSettled bool
	t3ReconcileStartActive bool
	t3ReconcileKillStray   bool
)

var t3ReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Bring dev servers and worktrees back in line with T3's threads",
	Long: `Compare conductor's live worktrees against the threads in T3 Code.

T3's threads are the record of what is actually being worked on, and conductor's
dev servers and worktrees should follow them. Three cases fall out:

  active    at least one thread is still unsettled — the dev server should run
  settled   every thread on the worktree is settled — the dev server should stop
  drifted   no thread references the worktree at all — it can be archived

Settled is the case that matters in practice. Archiving a thread is rare; the
usual way to finish a piece of work is to mark it settled and move on, which
until now left its dev server — api, worker and file watcher — running forever.

By default this only reports.

  --dev            stop the dev servers of settled worktrees, and start any an
                   active worktree is missing. Reversible: nothing but a server
                   is touched, and 'conductor t3 dev restart' brings one back.
  --stop-settled   only the stopping half. This frees memory.
  --start-active   only the starting half. This spends it — worth separating on
                   a machine that is already tight, which is why --dev always
                   stops before it starts.
  --kill-stray     kill dev servers holding a worktree's port from outside its
                   tmux window. These are started by agents inside a thread and
                   are invisible to every other command here.
  --archive        archive the drifted worktrees. Destructive: runs the archive
                   script, drops the database, stops the tunnel, frees ports.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		candidates, err := liveWorktrees(s)
		if err != nil {
			return err
		}
		hosted := t3.CountHosted(candidates)
		if hosted == 0 {
			fmt.Printf("No T3-hosted worktrees. (%d live worktree(s) are hosted by another multiplexer.)\n", len(candidates))
			return nil
		}

		client, err := t3.New()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		assessments, err := client.Classify(ctx, candidates)
		if err != nil {
			return err
		}

		active := t3.InState(assessments, t3.StateActive)
		settled := t3.InState(assessments, t3.StateSettled)
		drifted := t3.InState(assessments, t3.StateDrifted)

		// Report only what there is something to do about. Most worktrees are
		// already in the state their threads imply, and listing every one buries
		// the few lines the user needs to read — and would keep reporting the
		// same settled worktrees after their servers had already been stopped,
		// so the command would never converge on "nothing to do".
		var stalled []t3.Assessment
		for _, a := range active {
			if !tmux.WindowExists(a.Project, a.Branch) || tmux.DevServerStopped(a.Project, a.Branch) {
				stalled = append(stalled, a)
			}
		}
		var serving []t3.Assessment
		for _, a := range settled {
			if tmux.WindowExists(a.Project, a.Branch) && !tmux.DevServerStopped(a.Project, a.Branch) {
				serving = append(serving, a)
			}
		}

		// A worktree whose port answers while its window is stopped has a dev
		// server conductor did not start and cannot stop. Those are invisible to
		// every count above, so they have to be found by asking the port.
		type strayServer struct {
			t3.Assessment
			Port int
			PIDs []int
		}
		var strays []strayServer
		cfg := s.GetConfigSnapshot()
		for _, a := range assessments {
			port := worktreePort(cfg, a.Project, a.Worktree)
			if port == 0 || !portOpen(port) {
				continue
			}
			if tmux.WindowExists(a.Project, a.Branch) && !tmux.DevServerStopped(a.Project, a.Branch) {
				continue // Its own supervised server is up; the port is meant to answer.
			}
			if pids := stray.Listeners(port); len(pids) > 0 {
				strays = append(strays, strayServer{Assessment: a, Port: port, PIDs: pids})
			}
		}

		fmt.Printf("%d T3-hosted worktree(s): %d active, %d settled, %d drifted.\n",
			hosted, len(active), len(settled), len(drifted))

		if len(serving) > 0 {
			fmt.Printf("\nSettled but still serving — dev server should stop:\n")
			for _, a := range serving {
				fmt.Printf("  %s/%s (%d thread(s))\n    %s\n", a.Project, a.Worktree, a.Threads, a.WorktreePath)
			}
		}
		if len(stalled) > 0 {
			fmt.Printf("\nActive but not serving — dev server should start:\n")
			for _, a := range stalled {
				fmt.Printf("  %s/%s (%d of %d thread(s) settled)\n    %s\n",
					a.Project, a.Worktree, a.Settled, a.Threads, a.WorktreePath)
			}
		}
		if len(strays) > 0 {
			fmt.Printf("\nUnsupervised — a server is on the port but not in the window:\n")
			for _, sv := range strays {
				fmt.Printf("  %s/%s (%s, port %d, pid %v)\n    %s\n",
					sv.Project, sv.Worktree, sv.State, sv.Port, sv.PIDs, sv.WorktreePath)
			}
		}
		if len(drifted) > 0 {
			fmt.Printf("\nDrifted — no thread left, worktree can be archived:\n")
			for _, a := range drifted {
				fmt.Printf("  %s/%s\n    %s\n", a.Project, a.Worktree, a.WorktreePath)
			}
		}

		if len(serving) == 0 && len(stalled) == 0 && len(drifted) == 0 && len(strays) == 0 {
			fmt.Println("\nIn sync: nothing to do.")
			return nil
		}

		if t3ReconcileKillStray {
			fmt.Println()
			for _, sv := range strays {
				n := stray.Kill(sv.PIDs)
				fmt.Printf("  killed %d process(es) on port %d for %s/%s\n",
					n, sv.Port, sv.Project, sv.Worktree)
			}
		}

		// The two halves are separable because they pull in opposite directions
		// on a constrained machine: stopping settled servers frees memory,
		// starting missing ones spends it. Stopping runs first for the same
		// reason, so a combined pass never peaks above where it started.
		stopSettled := t3ReconcileDev || t3ReconcileStopSettled
		startActive := t3ReconcileDev || t3ReconcileStartActive

		if stopSettled && len(serving) > 0 {
			fmt.Println()
			for _, a := range serving {
				if !tmux.WindowExists(a.Project, a.Branch) {
					continue
				}
				if tmux.DevServerStopped(a.Project, a.Branch) {
					continue
				}
				if err := tmux.StopDevServer(a.Project, a.Branch); err != nil {
					fmt.Printf("  failed to stop %s/%s: %v\n", a.Project, a.Worktree, err)
					continue
				}
				fmt.Printf("  stopped dev server for %s/%s\n", a.Project, a.Worktree)
			}
		}

		if startActive && len(stalled) > 0 {
			fmt.Println()
			for _, a := range stalled {
				what, err := tmux.EnsureDevServer(a.Project, a.Branch, a.WorktreePath)
				if err != nil {
					fmt.Printf("  failed to start %s/%s: %v\n", a.Project, a.Worktree, err)
					continue
				}
				fmt.Printf("  %s dev server for %s/%s\n", what, a.Project, a.Worktree)
			}
		}

		if len(drifted) > 0 && t3ReconcileArchive {
			fmt.Println()
			// Archiving mutates config, so it runs inside the store's mutation
			// wrapper the same way `conductor worktree archive` does.
			if err := s.BatchMutate(func(cfg *config.Config) error {
				manager := workspace.NewManager(cfg)
				for _, a := range drifted {
					if err := manager.ArchiveWorktree(a.Project, a.Worktree); err != nil {
						fmt.Printf("  failed to archive %s/%s: %v\n", a.Project, a.Worktree, err)
						continue
					}
					fmt.Printf("  archived %s/%s\n", a.Project, a.Worktree)
				}
				return nil
			}); err != nil {
				return err
			}
		}

		// Tell the user about exactly the work they have not authorised yet.
		var todo []string
		if !stopSettled && len(serving) > 0 {
			todo = append(todo, "--stop-settled to stop the dev servers of settled worktrees")
		}
		if !startActive && len(stalled) > 0 {
			todo = append(todo, "--start-active to start the dev servers active worktrees are missing")
		}
		if !t3ReconcileKillStray && len(strays) > 0 {
			todo = append(todo, "--kill-stray to stop the unsupervised servers listed above")
		}
		if !t3ReconcileArchive && len(drifted) > 0 {
			todo = append(todo, "--archive to archive the drifted worktrees (drops their databases)")
		}
		if len(todo) > 0 {
			// "Nothing changed" is only true when nothing did. Saying it after a
			// partial pass — --stop-settled having just stopped ten servers —
			// tells the user the opposite of what happened.
			if stopSettled || startActive || t3ReconcileArchive {
				fmt.Println("\nStill outstanding. Re-run with:")
			} else {
				fmt.Println("\nNothing changed. Re-run with:")
			}
			for _, line := range todo {
				fmt.Printf("  %s\n", line)
			}
		}
		return nil
	},
}

// resolveLogTarget works out which dev server window to read.
//
// The window is keyed by project and *branch*, not by worktree name — that is
// how CreateDevWindow names it — so the inferred form has to resolve the branch
// rather than the city the worktree is registered under.
func resolveLogTarget(args []string) (project, branch string, err error) {
	if len(args) == 2 {
		return args[0], args[1], nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("failed to get current directory: %w", err)
	}
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "", "", fmt.Errorf("could not load conductor config")
	}
	projectName, _, worktree, err := cfg.DetectProject(cwd)
	if err != nil || worktree == nil {
		return "", "", fmt.Errorf("not inside a registered worktree — pass the project and branch explicitly")
	}
	if len(args) == 1 {
		return projectName, args[0], nil
	}
	return projectName, worktree.Branch, nil
}

// liveWorktrees returns every non-archived, non-root worktree conductor knows
// about, as reconcile candidates.
func liveWorktrees(s *store.Store) ([]t3.Candidate, error) {
	cfg := s.GetConfigSnapshot()
	if cfg == nil {
		return nil, fmt.Errorf("could not load conductor config")
	}

	var out []t3.Candidate
	for projectName, project := range cfg.Projects {
		for worktreeName, worktree := range project.Worktrees {
			if worktree.Archived || worktree.IsRoot || worktree.Path == "" {
				continue
			}
			out = append(out, t3.Candidate{
				Project:      projectName,
				Worktree:     worktreeName,
				Branch:       worktree.Branch,
				WorktreePath: worktree.Path,
			})
		}
	}
	return out, nil
}

func truncateTitle(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

var t3CreatePrompt string

var t3CreateCmd = &cobra.Command{
	Use:   "create <project> <worktree>",
	Short: "Create the T3 thread for an existing worktree",
	Long: `Create the T3 Code thread bound to a worktree that already exists.

Provisioning a worktree and opening its thread are two separate steps.
"conductor worktree create" does the first — git worktree, ports, database —
and the thread is opened later, when a coding window is created. The TUI,
"conductor build" and the agent dispatcher all do that, but nothing exposed it
to a plain shell, so a worktree made with "conductor worktree create" had no
thread and "conductor t3 send" failed with "no live T3 thread is bound".

This opens that thread through the same code path the coding window uses, so
both routes produce an identical thread. With --prompt, the prompt is
submitted as the thread's first turn.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, worktreeName := args[0], args[1]

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("conductor is not initialised; run 'conductor init'")
		}
		project, ok := cfg.GetProject(projectName)
		if !ok {
			return fmt.Errorf("unknown project %q", projectName)
		}
		worktree, ok := project.Worktrees[worktreeName]
		if !ok || worktree == nil {
			return fmt.Errorf("unknown worktree %q in project %q", worktreeName, projectName)
		}
		if worktree.Archived {
			return fmt.Errorf("worktree %q is archived; wake it before opening a thread", worktreeName)
		}
		worktreePath, err := config.ResolveWorktreePath(projectName, worktreeName)
		if err != nil {
			return err
		}

		// Always the T3 backend: the command is "conductor t3 create", not a
		// request for whichever multiplexer the config happens to name.
		m := mux.T3()
		if err := m.CreateCodingWindowWithTask(
			projectName, worktree.Branch, worktreePath, t3CreatePrompt, codingagent.ClaudeCode,
		); err != nil {
			return err
		}

		// Report the thread from the marker the backend just wrote, not from a
		// fresh snapshot: T3 applies the create asynchronously, so reading the
		// snapshot here races it and reports "no thread" for one that exists.
		client, err := t3.New()
		if err != nil {
			return err
		}
		ids, ok := t3.ReadMarker(worktreePath)
		if !ok || len(ids) == 0 {
			return fmt.Errorf("thread created for %s/%s but no marker was written to %s",
				projectName, worktree.Branch, worktreePath)
		}
		fmt.Printf("%s/thread/%s\n", client.Origin, ids[len(ids)-1])
		return nil
	},
}

// t3DevCmd is the dev server's control surface for an agent working in a
// thread.
//
// The dev server lives in tmux rather than in the thread, so an agent has no
// process to Ctrl-C and no obvious way back if it stops. Left without one it
// does the wrong thing — starts a second server, which collides on the port
// that the first one is still holding. This is the right thing, named.
var t3DevCmd = &cobra.Command{
	Use:   "dev",
	Short: "Control the shared dev server for a worktree",
	Long: `Control the dev server that conductor runs for a worktree.

The dev server lives in a tmux window rather than in a T3 terminal, so that it
survives T3 Code restarts and is shared by every thread bound to the worktree.
That means you never start it yourself — you ask for it here.

  conductor t3 dev status     # is it up, and on what address
  conductor t3 dev restart    # interrupt and bring it back, or recreate its window
  conductor t3 dev stop       # leave it at the restart prompt`,
}

var t3DevStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report the dev server's window, address and whether it is listening",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, worktree, wt, err := currentWorktree()
		if err != nil {
			return err
		}

		fmt.Printf("Worktree: %s/%s\n", project, worktree)
		fmt.Printf("Window:   %s\n", tmux.WindowTarget(project, wt.Branch))
		if url := worktreeURL(wt); url != "" {
			fmt.Printf("Address:  %s\n", url)
		}

		if !tmux.WindowExists(project, wt.Branch) {
			fmt.Println("State:    no window — run 'conductor t3 dev restart' to create one")
			return nil
		}
		state := "running"
		if tmux.DevServerStopped(project, wt.Branch) {
			state = "stopped, waiting at the restart prompt"
		}
		fmt.Printf("State:    %s\n", state)

		if len(wt.Ports) > 0 {
			listening := "not listening"
			if portOpen(wt.Ports[0]) {
				listening = "listening"
			}
			fmt.Printf("Port %d:  %s\n", wt.Ports[0], listening)
		}
		return nil
	},
}

var t3DevRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the dev server, recreating its window if it is gone",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, _, wt, err := currentWorktree()
		if err != nil {
			return err
		}
		what, err := tmux.RestartDevServer(project, wt.Branch, wt.Path)
		if err != nil {
			return err
		}
		fmt.Printf("%s — follow it with 'conductor t3 logs -f'\n", what)
		return nil
	},
}

var t3DevStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the dev server, leaving its window at the restart prompt",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, _, wt, err := currentWorktree()
		if err != nil {
			return err
		}
		if err := tmux.StopDevServer(project, wt.Branch); err != nil {
			return err
		}
		fmt.Println("stopped — 'conductor t3 dev restart' brings it back")
		return nil
	},
}

// currentWorktree resolves the worktree the caller is standing in. Every dev
// command infers rather than taking arguments: an agent runs them from inside
// the tree it is working in, and asking it to name the worktree is asking it to
// get the name wrong.
func currentWorktree() (project, worktree string, wt *config.Worktree, err error) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "", "", nil, fmt.Errorf("conductor is not initialised")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", nil, err
	}
	projectName, proj, found, err := cfg.DetectProject(cwd)
	if err != nil || found == nil {
		return "", "", nil, fmt.Errorf("not inside a registered worktree")
	}
	for name, candidate := range proj.Worktrees {
		if candidate == found {
			return projectName, name, found, nil
		}
	}
	return projectName, "", found, nil
}

// worktreeURL is where the dev server answers. The port is the worktree's
// first, which is the one the setup script writes into the project's .env.
func worktreeURL(wt *config.Worktree) string {
	if len(wt.Ports) == 0 {
		return ""
	}
	return fmt.Sprintf("http://localhost:%d", wt.Ports[0])
}

// worktreePort returns the first port conductor allocated to a worktree, or 0.
func worktreePort(cfg *config.Config, project, worktree string) int {
	if cfg == nil {
		return 0
	}
	p, ok := cfg.Projects[project]
	if !ok {
		return 0
	}
	wt, ok := p.Worktrees[worktree]
	if !ok || len(wt.Ports) == 0 {
		return 0
	}
	return wt.Ports[0]
}

// portOpen reports whether the dev server is accepting connections. "localhost"
// covers both loopback families: Vite 8 binds [::1] only, so an IPv4-only probe
// printed "not listening" for a server answering requests fine.
func portOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

var t3ProvidersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List the provider instances this T3 build has, and what conductor would pick",
	Long: `List T3's configured provider instances and the model selection conductor
would use for a new thread.

A thread's model selection names a provider instance, and T3 accepts an instance
id that its build does not have: the thread is created, looks healthy, and only
fails when the first turn tries to start — in thread.session.lastError, where
nothing is looking. From outside it is indistinguishable from a thread that
ignores programmatic turns.

This is the check for that. An instance marked unusable cannot run a turn, and
conductor will not create a thread against one.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := t3.New()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		instances, err := client.ProviderInstances(ctx)
		if err != nil {
			return err
		}

		writer := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		fmt.Fprintln(writer, "INSTANCE\tDRIVER\tUSABLE\tSTATUS\tAUTH\tMODELS")
		for _, instance := range instances {
			usable := "no"
			if instance.Usable() {
				usable = "yes"
			}
			models := make([]string, 0, len(instance.Models))
			for _, model := range instance.Models {
				models = append(models, model.Slug)
			}
			listed := strings.Join(models, ", ")
			if listed == "" {
				listed = "-"
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
				instance.InstanceID, instance.Driver, usable,
				instance.Status, instance.Auth.Status, listed)
		}
		if err := writer.Flush(); err != nil {
			return err
		}

		selection, err := client.ResolveModelSelection(ctx)
		if err != nil {
			return fmt.Errorf("\nconductor cannot pick a model: %w", err)
		}
		fmt.Printf("\nconductor would create threads on %s\n", selection)
		if selection.OptionsKey() == "" {
			fmt.Println("(no provider options set, so T3 applies the model's own defaults — " +
				"'high' reasoning on every current Claude model)")
		}
		if override, ok := t3.ModelSelectionFromEnv(); ok {
			fmt.Printf("(CONDUCTOR_T3_MODEL requests %s)\n", override)
		}
		return nil
	},
}

func init() {
	t3SendCmd.Flags().StringVarP(&t3SendMessage, "message", "m", "", "Message to send to the thread")
	t3ReconcileCmd.Flags().BoolVar(&t3ReconcileDev, "dev", false,
		"Stop the dev servers of settled worktrees and start those an active worktree is missing")
	t3ReconcileCmd.Flags().BoolVar(&t3ReconcileStopSettled, "stop-settled", false,
		"Stop the dev servers of settled worktrees, and nothing else")
	t3ReconcileCmd.Flags().BoolVar(&t3ReconcileStartActive, "start-active", false,
		"Start the dev servers that active worktrees are missing, and nothing else")
	t3ReconcileCmd.Flags().BoolVar(&t3ReconcileKillStray, "kill-stray", false,
		"Kill dev servers holding a worktree's port from outside its tmux window")
	t3ReconcileCmd.Flags().BoolVar(&t3ReconcileArchive, "archive", false,
		"Archive the drifted worktrees (destructive: drops databases, removes worktrees)")

	t3LogsCmd.Flags().IntVarP(&t3LogLines, "lines", "n", 200, "Number of lines of history to read")
	t3LogsCmd.Flags().BoolVarP(&t3LogFollow, "follow", "f", false, "Follow the output")

	t3CreateCmd.Flags().StringVarP(&t3CreatePrompt, "prompt", "p", "",
		"Prompt to submit as the thread's first turn")

	t3Cmd.AddCommand(t3StatusCmd)
	t3Cmd.AddCommand(t3LogsCmd)
	t3Cmd.AddCommand(t3TokenCmd)
	t3Cmd.AddCommand(t3SendCmd)
	t3Cmd.AddCommand(t3CreateCmd)
	t3DevCmd.AddCommand(t3DevStatusCmd)
	t3DevCmd.AddCommand(t3DevRestartCmd)
	t3DevCmd.AddCommand(t3DevStopCmd)
	t3Cmd.AddCommand(t3DevCmd)
	t3Cmd.AddCommand(t3ProvidersCmd)
	t3Cmd.AddCommand(t3ReconcileCmd)
	t3Cmd.AddCommand(t3WatchCmd)
}
