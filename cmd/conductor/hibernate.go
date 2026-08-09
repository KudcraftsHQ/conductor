package main

import (
	"fmt"
	"os"

	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/workspace"
	"github.com/spf13/cobra"
)

// worktreeHibernateCmd releases a worktree's resources without touching its
// tree.
//
// The watcher does this on its own once every thread on a worktree is archived.
// Doing it by hand is for reclaiming a port or a database sooner than the
// debounce would, and for worktrees T3 does not host at all.
var worktreeHibernateCmd = &cobra.Command{
	Use:   "hibernate <name>",
	Short: "Release a worktree's ports, database and tunnel, keeping its tree",
	Long: `Releases everything a worktree consumes — its database is dropped, its
tunnel and dev server stopped, its ports returned — and leaves the working tree
and branch exactly as they are, uncommitted work included.

Wake it again with 'conductor worktree wake <name>', which allocates fresh
ports and rebuilds the database. Unarchiving any of its T3 threads does the
same thing automatically.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, s, err := currentProject()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		name := args[0]
		manager := workspace.NewManagerWithStore(s.GetConfigSnapshot(), s)
		if err := manager.ReleaseResources(projectName, name); err != nil {
			return err
		}
		fmt.Printf("Hibernated %s/%s — working tree and branch untouched\n", projectName, name)
		return nil
	},
}

// worktreeWakeCmd re-provisions a hibernated worktree.
var worktreeWakeCmd = &cobra.Command{
	Use:   "wake <name>",
	Short: "Re-provision a hibernated worktree",
	Long: `Allocates fresh ports, rebuilds the dev database, runs the project setup
script and starts the dev server for a worktree whose resources were released.

The ports will not be the ones it had before, and the database is a new clone —
read both from the environment rather than from memory. The working tree was
never touched, so anything uncommitted is still there.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, s, err := currentProject()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		name := args[0]
		manager := workspace.NewManagerWithStore(s.GetConfigSnapshot(), s)
		if err := manager.Provision(projectName, name); err != nil {
			return err
		}

		if wt, err := manager.GetWorktree(projectName, name); err == nil && wt != nil {
			fmt.Printf("Woke %s/%s on ports %v\n", projectName, name, wt.Ports)
			if wt.DatabaseName != "" {
				fmt.Printf("Database: %s\n", wt.DatabaseName)
			}
		}
		return nil
	},
}

// currentProject resolves the project from the working directory, returning the
// loaded store so the caller can close it.
func currentProject() (string, *store.Store, error) {
	s, err := store.Load()
	if err != nil {
		return "", nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = s.Close()
		return "", nil, fmt.Errorf("failed to get current directory: %w", err)
	}
	projectName, _, _, err := s.GetConfigSnapshot().DetectProject(cwd)
	if err != nil {
		_, _ = s.Close()
		return "", nil, fmt.Errorf("not in a registered project")
	}
	return projectName, s, nil
}

func init() {
	worktreeCmd.AddCommand(worktreeHibernateCmd)
	worktreeCmd.AddCommand(worktreeWakeCmd)
	worktreeCmd.AddCommand(worktreeDeleteCmd)
}

// worktreeDeleteCmd drops an archived worktree's entry for good.
//
// Archiving already removed the tree and freed the ports; what survives is a
// tombstone that keeps the setup and archive logs readable. This is how you
// discard that too. It refuses anything not yet archived, so there is no path
// from here to losing a working tree.
var worktreeDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Permanently remove an archived worktree's entry",
	Long: `Removes an archived worktree from conductor's state, discarding its setup and
archive logs along with it.

Only archived worktrees can be deleted — archive it first. The git branch is not
touched by this: archiving already dealt with the tree, and by this point there
is nothing left on disk to remove.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, s, err := currentProject()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		name := args[0]
		manager := workspace.NewManagerWithStore(s.GetConfigSnapshot(), s)
		if err := manager.DeleteWorktree(projectName, name); err != nil {
			return err
		}
		fmt.Printf("Deleted %s/%s\n", projectName, name)
		return nil
	},
}
