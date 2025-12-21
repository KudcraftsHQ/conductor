package main

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/opener"
	"github.com/hammashamzah/conductor/internal/workspace"
	"github.com/spf13/cobra"
)

var worktreeCmd = &cobra.Command{
	Use:     "worktree",
	Aliases: []string{"wt"},
	Short:   "Manage worktrees",
	Long:    "Create, list, open, and archive git worktrees",
}

var worktreeCreatePorts int

var worktreeCreateCmd = &cobra.Command{
	Use:   "create [branch]",
	Short: "Create a new worktree",
	Long:  "Creates a new git worktree with allocated ports",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, _, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project. Run 'conductor project add .' first")
		}

		branch := ""
		if len(args) > 0 {
			branch = args[0]
		}

		mgr := workspace.NewManager(cfg)
		name, wt, err := mgr.CreateWorktree(projectName, branch, worktreeCreatePorts)
		if err != nil {
			return err
		}

		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Created worktree '%s'\n", name)
		fmt.Printf("  Path: %s\n", wt.Path)
		fmt.Printf("  Branch: %s\n", wt.Branch)
		fmt.Printf("  Ports: %v\n", wt.Ports)

		return nil
	},
}

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List worktrees for current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		if len(project.Worktrees) == 0 {
			fmt.Println("No worktrees found.")
			return nil
		}

		// Sort worktrees by name
		names := make([]string, 0, len(project.Worktrees))
		for name := range project.Worktrees {
			names = append(names, name)
		}
		sort.Strings(names)

		fmt.Printf("Worktrees for %s:\n\n", projectName)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tBRANCH\tPORTS\tCREATED")
		fmt.Fprintln(w, "----\t------\t-----\t-------")

		for _, name := range names {
			wt := project.Worktrees[name]
			portRange := formatPortRange(wt.Ports)
			created := wt.CreatedAt.Format("Jan 2, 15:04")
			prefix := "  "
			if wt.IsRoot {
				prefix = "* "
			}
			fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\n", prefix, name, wt.Branch, portRange, created)
		}
		w.Flush()

		fmt.Println("\n* = root worktree")

		return nil
	},
}

var (
	worktreeOpenCursor   bool
	worktreeOpenVSCode   bool
	worktreeOpenTerminal bool
	worktreeOpenZed      bool
)

var worktreeOpenCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Open a worktree",
	Long:  "Open a worktree in terminal or IDE",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		_, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		name := args[0]
		wt, ok := project.Worktrees[name]
		if !ok {
			return fmt.Errorf("worktree '%s' not found", name)
		}

		// Determine what to open with
		switch {
		case worktreeOpenCursor:
			fmt.Printf("Opening %s in Cursor...\n", name)
			return opener.OpenInIDE(wt.Path, opener.IDECursor)
		case worktreeOpenVSCode:
			fmt.Printf("Opening %s in VSCode...\n", name)
			return opener.OpenInIDE(wt.Path, opener.IDEVSCode)
		case worktreeOpenZed:
			fmt.Printf("Opening %s in Zed...\n", name)
			return opener.OpenInIDE(wt.Path, opener.IDEZed)
		case worktreeOpenTerminal:
			fallthrough
		default:
			// Open in terminal with split panes
			fmt.Printf("Opening %s in terminal...\n", name)

			// Check if project has a run script
			leftCmd := ""
			rightCmd := "conductor run"

			// Get project config for potential custom commands
			projCfg, _ := config.LoadProjectConfig(project.Path)
			if projCfg != nil && projCfg.Scripts["run"] != "" {
				// Use conductor run for right pane
				rightCmd = "conductor run"
			}

			return opener.OpenInITermSplit(wt.Path, leftCmd, rightCmd)
		}
	},
}

var worktreeArchiveCmd = &cobra.Command{
	Use:   "archive <name>",
	Short: "Archive a worktree",
	Long:  "Remove a worktree and free its allocated ports",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, _, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		name := args[0]
		mgr := workspace.NewManager(cfg)
		if err := mgr.ArchiveWorktree(projectName, name); err != nil {
			return err
		}

		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Archived worktree '%s'\n", name)
		return nil
	},
}

var worktreeStatusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show worktree status",
	Long:  "Show port and environment information for a worktree",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, currentWt, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		// Use specified worktree or current
		var wt *config.Worktree
		var wtName string

		if len(args) > 0 {
			wtName = args[0]
			wt = project.Worktrees[wtName]
			if wt == nil {
				return fmt.Errorf("worktree '%s' not found", wtName)
			}
		} else {
			wt = currentWt
			for name, w := range project.Worktrees {
				if w == wt {
					wtName = name
					break
				}
			}
		}

		fmt.Printf("Project: %s\n", projectName)
		fmt.Printf("Worktree: %s\n", wtName)
		fmt.Printf("Path: %s\n", wt.Path)
		fmt.Printf("Branch: %s\n", wt.Branch)
		fmt.Printf("Is Root: %t\n", wt.IsRoot)
		fmt.Printf("Created: %s\n", wt.CreatedAt.Format(time.RFC3339))
		fmt.Printf("Ports: %v\n", wt.Ports)

		// Show port labels if available
		projCfg, _ := config.LoadProjectConfig(project.Path)
		if projCfg != nil && len(projCfg.Ports.Labels) > 0 {
			fmt.Println("\nPort Labels:")
			for i, label := range projCfg.Ports.Labels {
				if i < len(wt.Ports) {
					fmt.Printf("  %s: %d\n", label, wt.Ports[i])
				}
			}
		}

		return nil
	},
}

func formatPortRange(ports []int) string {
	if len(ports) == 0 {
		return "-"
	}
	if len(ports) == 1 {
		return fmt.Sprintf("%d", ports[0])
	}
	return fmt.Sprintf("%d-%d", ports[0], ports[len(ports)-1])
}

func init() {
	worktreeCreateCmd.Flags().IntVarP(&worktreeCreatePorts, "ports", "p", 0, "Number of ports to allocate")

	worktreeOpenCmd.Flags().BoolVar(&worktreeOpenCursor, "cursor", false, "Open in Cursor")
	worktreeOpenCmd.Flags().BoolVar(&worktreeOpenVSCode, "vscode", false, "Open in VSCode")
	worktreeOpenCmd.Flags().BoolVar(&worktreeOpenZed, "zed", false, "Open in Zed")
	worktreeOpenCmd.Flags().BoolVarP(&worktreeOpenTerminal, "terminal", "t", false, "Open in terminal")

	worktreeCmd.AddCommand(worktreeCreateCmd)
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeOpenCmd)
	worktreeCmd.AddCommand(worktreeArchiveCmd)
	worktreeCmd.AddCommand(worktreeStatusCmd)
}
