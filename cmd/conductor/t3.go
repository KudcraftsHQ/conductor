package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
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

		worktreePath, err := config.WorktreePath(args[0], args[1])
		if err != nil {
			return err
		}
		client, err := t3.New()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
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
		fmt.Printf("Sent to %s/thread/%s\n", client.Origin, thread.ID)
		return nil
	},
}

var (
	t3LogLines  int
	t3LogFollow bool
)

var t3LogsCmd = &cobra.Command{
	Use:   "logs <project> <worktree>",
	Short: "Read the dev server logs for a worktree",
	Long: `Read the dev server logs for a worktree.

The dev server runs in a tmux window rather than in the T3 thread, so that it
survives T3 Code restarts. This reads that window, which is how an agent
working in the thread — or hermes — gets at the output.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, worktree := args[0], args[1]
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

var t3ReconcileArchive bool

var t3ReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Find worktrees whose T3 thread has gone away",
	Long: `Find live worktrees that no longer have a thread in T3 Code.

A thread disappears when it is archived or deleted in T3's UI. The worktree, its
ports, its database and its tunnel all survive that, because they belong to
conductor rather than to T3.

By default this only reports. Pass --archive to actually archive the drifted
worktrees, which runs the archive script, drops the database, stops the tunnel
and frees the ports.`,
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

		drifted, err := client.Reconcile(ctx, candidates)
		if err != nil {
			return err
		}
		if len(drifted) == 0 {
			fmt.Printf("In sync: all %d T3-hosted worktree(s) have a live thread.\n", hosted)
			return nil
		}

		fmt.Printf("%d of %d T3-hosted worktree(s) have no thread:\n\n", len(drifted), hosted)
		for _, d := range drifted {
			fmt.Printf("  %s/%s\n    %s\n", d.Project, d.Worktree, d.WorktreePath)
		}

		if !t3ReconcileArchive {
			fmt.Println("\nNothing changed. Re-run with --archive to archive these worktrees.")
			fmt.Println("Archiving drops each worktree's database and removes it from disk.")
			return nil
		}

		fmt.Println()
		// Archiving mutates config, so it runs inside the store's mutation
		// wrapper the same way `conductor worktree archive` does.
		return s.BatchMutate(func(cfg *config.Config) error {
			manager := workspace.NewManager(cfg)
			for _, d := range drifted {
				if err := manager.ArchiveWorktree(d.Project, d.Worktree); err != nil {
					fmt.Printf("  failed to archive %s/%s: %v\n", d.Project, d.Worktree, err)
					continue
				}
				fmt.Printf("  archived %s/%s\n", d.Project, d.Worktree)
			}
			return nil
		})
	},
}

// liveWorktrees returns every non-archived, non-root worktree conductor knows
// about, as reconcile candidates.
func liveWorktrees(s *store.Store) ([]t3.Drift, error) {
	cfg := s.GetConfigSnapshot()
	if cfg == nil {
		return nil, fmt.Errorf("could not load conductor config")
	}

	var out []t3.Drift
	for projectName, project := range cfg.Projects {
		for worktreeName, worktree := range project.Worktrees {
			if worktree.Archived || worktree.IsRoot {
				continue
			}
			path, err := config.WorktreePath(projectName, worktreeName)
			if err != nil {
				continue
			}
			out = append(out, t3.Drift{
				Project:      projectName,
				Worktree:     worktreeName,
				WorktreePath: path,
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

func init() {
	t3SendCmd.Flags().StringVarP(&t3SendMessage, "message", "m", "", "Message to send to the thread")
	t3ReconcileCmd.Flags().BoolVar(&t3ReconcileArchive, "archive", false,
		"Archive the drifted worktrees (destructive: drops databases, removes worktrees)")

	t3LogsCmd.Flags().IntVarP(&t3LogLines, "lines", "n", 200, "Number of lines of history to read")
	t3LogsCmd.Flags().BoolVarP(&t3LogFollow, "follow", "f", false, "Follow the output")

	t3Cmd.AddCommand(t3StatusCmd)
	t3Cmd.AddCommand(t3LogsCmd)
	t3Cmd.AddCommand(t3TokenCmd)
	t3Cmd.AddCommand(t3SendCmd)
	t3Cmd.AddCommand(t3ReconcileCmd)
}
