package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects",
	Long:  "Add, remove, and list registered projects",
}

var projectAddPorts int

var projectAddCmd = &cobra.Command{
	Use:   "add [path]",
	Short: "Register a project",
	Long: `Add a git repository to conductor. Defaults to current directory.

This command will:
- Register the project in conductor
- Allocate ports for the root worktree
- Create conductor.json if it doesn't exist (with --init flag)`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Auto-initialize conductor if not already done
		if !config.Exists() {
			fmt.Println("Conductor not initialized. Initializing now...")
			if err := config.Init(); err != nil {
				return fmt.Errorf("failed to initialize conductor: %w", err)
			}
			configPath, err := config.ConfigPath()
			if err != nil {
				return fmt.Errorf("failed to determine config path: %w", err)
			}
			fmt.Printf("Initialized conductor at %s\n\n", configPath)
		}

		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		path := "."
		if len(args) > 0 {
			path = args[0]
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("invalid path: %w", err)
		}

		// Use BatchMutate to call the config's AddProject method which handles
		// git validation, branch detection, port allocation, and project creation
		var name string
		var project *config.Project
		err = s.BatchMutate(func(cfg *config.Config) error {
			var addErr error
			name, addErr = cfg.AddProject(absPath, projectAddPorts)
			if addErr != nil {
				return addErr
			}
			project = cfg.Projects[name]
			return nil
		})
		if err != nil {
			return err
		}

		rootWt := project.Worktrees["root"]

		fmt.Printf("Added project '%s'\n", name)
		fmt.Printf("  Path: %s\n", absPath)
		if len(rootWt.Ports) == 1 {
			fmt.Printf("  Ports: %d\n", rootWt.Ports[0])
		} else {
			fmt.Printf("  Ports: %d-%d\n", rootWt.Ports[0], rootWt.Ports[len(rootWt.Ports)-1])
		}

		// Show worktree storage location
		wtBase, err := config.WorktreeBasePath(name)
		if err != nil {
			return fmt.Errorf("failed to determine worktree path: %w", err)
		}
		fmt.Printf("  Worktrees: %s\n", wtBase)

		// Check if conductor.json exists, suggest creating it
		projectConfigPath := filepath.Join(absPath, "conductor.json")
		if _, err := os.Stat(projectConfigPath); os.IsNotExist(err) {
			fmt.Println("\nTip: Run 'conductor project init' in the project to create conductor.json")
		}

		return nil
	},
}

var projectRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Unregister a project",
	Long:  "Remove a project from conductor (does not delete files)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		name := args[0]

		// Use BatchMutate to call the config's RemoveProject method which handles
		// freeing all ports for all worktrees
		err = s.BatchMutate(func(cfg *config.Config) error {
			return cfg.RemoveProject(name)
		})
		if err != nil {
			return err
		}

		fmt.Printf("Removed project '%s'\n", name)
		return nil
	},
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		projects := s.GetAllProjects()
		if len(projects) == 0 {
			fmt.Println("No projects registered.")
			fmt.Println("Use 'conductor project add .' to add a project.")
			return nil
		}

		// Sort projects by name
		names := make([]string, 0, len(projects))
		for name := range projects {
			names = append(names, name)
		}
		sort.Strings(names)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tPATH\tWORKTREES\tPORTS")
		_, _ = fmt.Fprintln(w, "----\t----\t---------\t-----")

		for _, name := range names {
			proj := projects[name]
			// Collect all ports from worktrees
			var ports []int
			for _, wt := range proj.Worktrees {
				ports = append(ports, wt.Ports...)
			}
			sort.Ints(ports)

			portRange := "-"
			if len(ports) > 0 {
				if len(ports) == 1 {
					portRange = fmt.Sprintf("%d", ports[0])
				} else {
					portRange = fmt.Sprintf("%d-%d", ports[0], ports[len(ports)-1])
				}
			}
			// Count worktrees (excluding root)
			wtCount := 0
			for _, wt := range proj.Worktrees {
				if !wt.IsRoot {
					wtCount++
				}
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", name, proj.Path, wtCount, portRange)
		}
		_ = w.Flush()

		return nil
	},
}

var projectInitPorts int

var projectInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize conductor.json in current project",
	Long:  "Creates a conductor.json template in the current directory",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		configPath := filepath.Join(cwd, "conductor.json")
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("conductor.json already exists")
		}

		// Create default project config
		ports := 1
		if projectInitPorts > 0 {
			ports = projectInitPorts
		}

		projectCfg := &config.ProjectConfig{
			Scripts: map[string]string{
				"setup":   "# Add setup commands here\necho 'Running setup...'",
				"run":     "# Add run commands here\necho 'Starting dev server...'",
				"archive": "# Add cleanup commands here\necho 'Cleaning up...'",
			},
			Ports: config.PortConfig{
				Default: ports,
				Labels:  []string{},
			},
		}

		if err := config.SaveProjectConfig(cwd, projectCfg); err != nil {
			return err
		}

		// Create .conductor-scripts directory
		scriptsDir := filepath.Join(cwd, ".conductor-scripts")
		if err := os.MkdirAll(scriptsDir, 0755); err != nil {
			return fmt.Errorf("failed to create scripts directory: %w", err)
		}

		// Create example script
		exampleScript := filepath.Join(scriptsDir, "setup.sh.example")
		exampleContent := `#!/bin/bash
# Example setup script
# Rename to setup.sh to use

echo "Setting up workspace..."
echo "CONDUCTOR_PORT: $CONDUCTOR_PORT"
echo "CONDUCTOR_WORKSPACE_NAME: $CONDUCTOR_WORKSPACE_NAME"

# Add your setup commands here
# npm install
# prisma migrate deploy
`
		if err := os.WriteFile(exampleScript, []byte(exampleContent), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create example script: %v\n", err)
		}

		fmt.Println("Created conductor.json")
		fmt.Println("Created .conductor-scripts/ directory")
		fmt.Println("\nEdit conductor.json to configure your scripts and port settings.")

		return nil
	},
}

var projectShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show project details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		name := args[0]
		project, ok := s.GetProject(name)
		if !ok {
			return fmt.Errorf("project '%s' not found", name)
		}

		data, err := json.MarshalIndent(project, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal project data: %w", err)
		}
		fmt.Println(string(data))

		return nil
	},
}

func init() {
	projectAddCmd.Flags().IntVarP(&projectAddPorts, "ports", "p", 0, "Default ports per worktree")
	projectInitCmd.Flags().IntVarP(&projectInitPorts, "ports", "p", 0, "Default ports per worktree")

	projectCmd.AddCommand(projectAddCmd)
	projectCmd.AddCommand(projectRemoveCmd)
	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectInitCmd)
	projectCmd.AddCommand(projectShowCmd)
}
