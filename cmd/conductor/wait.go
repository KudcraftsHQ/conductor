package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/ready"
)

var (
	waitFor     string
	waitTimeout time.Duration
	waitJSON    bool
	waitQuiet   bool
)

// exitCoded carries an exit status out of RunE without printing cobra's usage
// text, which is noise for a wait that simply timed out.
type exitCoded struct {
	code int
	msg  string
}

func (e exitCoded) Error() string { return e.msg }

var waitCmd = &cobra.Command{
	Use:   "wait [path]",
	Short: "Block until a worktree is provisioned and safe to work in",
	Long: `Block until a worktree has finished provisioning.

T3 Code starts a thread's first turn immediately after launching conductor's
provisioning hook, without waiting for it. A dev database is a full clone and
takes minutes, so anything that queries data, runs migrations, seeds or tests
has to wait first — otherwise it works against a database that does not exist
yet.

By default this waits for the setup script to finish and for the worktree's
database to answer a query. The dev server is not included: a task whose job is
to fix a server that will not boot must not deadlock waiting for it to boot.
Ask for it with --for all, or --for server.

  conductor wait                       # setup + database, inferred from the cwd
  conductor wait --for all             # and the dev server port
  conductor wait --for server          # only the dev server
  conductor wait --timeout 5m --json

Exit codes:
  0  ready
  1  setup failed, or was abandoned by a process that died
  2  timed out
  3  not a conductor worktree`,
	Args: cobra.MaximumNArgs(1),
	// The exit code is the interface here, so cobra's usage dump on a non-zero
	// return would bury it.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		phases, err := ready.ParsePhases(waitFor)
		if err != nil {
			return err
		}

		path := "."
		if len(args) == 1 {
			path = args[0]
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), waitTimeout)
		defer cancel()

		// A first resolve before the wait, so an already-ready worktree costs
		// nothing and a bad path fails immediately rather than after a tick.
		cfg, cfgErr := config.Load()
		if cfgErr != nil {
			return exitCoded{3, cfgErr.Error()}
		}
		if first := ready.Resolve(cfg, path, phases); first.State == ready.StateUnknown {
			return report(first, 3)
		}

		progress := func(status ready.Status) {
			if waitQuiet || waitJSON {
				return
			}
			fmt.Fprintf(os.Stderr, "%s\n", status)
		}
		status, waitErr := ready.Wait(ctx, path, phases, 2*time.Second, progress)

		switch {
		case waitErr != nil && status.State.Terminal():
			// The context died at the same moment as a real answer; the answer wins.
		case waitErr != nil:
			return reportStatus(status, 2, fmt.Sprintf("timed out after %s waiting for %s",
				waitTimeout, statusPhase(status)))
		}

		switch status.State {
		case ready.StateReady, ready.StateUnmanaged:
			return reportStatus(status, 0, "")
		case ready.StateFailed, ready.StateStalled:
			return reportStatus(status, 1, "")
		case ready.StateUnknown:
			return reportStatus(status, 3, "")
		default:
			return reportStatus(status, 2, "")
		}
	},
}

func statusPhase(status ready.Status) string {
	if status.Phase == "" {
		return "the worktree"
	}
	return string(status.Phase)
}

// report is reportStatus for the pre-wait resolve, kept separate only so the
// call site reads as an early return.
func report(status ready.Status, code int) error { return reportStatus(status, code, "") }

// reportStatus prints the outcome and returns the exit code.
//
// A failed setup prints the tail of its log: a caller told only "failed" has to
// go and find the log itself, and an agent will not.
func reportStatus(status ready.Status, code int, override string) error {
	detail := status.Detail
	if override != "" {
		detail = override
	}

	if waitJSON {
		payload := struct {
			ready.Status
			Detail   string `json:"detail,omitempty"`
			ExitCode int    `json:"exitCode"`
		}{Status: status, Detail: detail, ExitCode: code}
		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err == nil {
			fmt.Println(string(encoded))
		}
	} else if !waitQuiet || code != 0 {
		if code == 0 {
			fmt.Printf("ready: %s\n", status.Path)
		} else {
			fmt.Fprintf(os.Stderr, "not ready (%s): %s\n", status.State, detail)
		}
	}

	if code == 1 && !waitJSON {
		if tail := ready.TailSetupLog(status.SetupLog, 20); tail != "" {
			fmt.Fprintf(os.Stderr, "\nlast 20 lines of %s:\n%s\n", status.SetupLog, tail)
		}
	}
	if code == 0 {
		return nil
	}
	return exitCoded{code, detail}
}

func init() {
	waitCmd.Flags().StringVar(&waitFor, "for", "",
		"Phases to wait for: setup, db, server, all (default \"setup,db\")")
	waitCmd.Flags().DurationVar(&waitTimeout, "timeout", 20*time.Minute,
		"Give up after this long")
	waitCmd.Flags().BoolVar(&waitJSON, "json", false, "Report the status as JSON")
	waitCmd.Flags().BoolVarP(&waitQuiet, "quiet", "q", false, "Only report failures")
	rootCmd.AddCommand(waitCmd)
}
