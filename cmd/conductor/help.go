package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var helpMarkdown bool

var helpCmd = &cobra.Command{
	Use:   "help [command]",
	Short: "Help about any command",
	Long: `Help provides help for any command in the application.
Simply type conductor help [path to command] for full details.

Use --markdown to output documentation in markdown format (useful for AI tools).`,
	Run: func(cmd *cobra.Command, args []string) {
		if helpMarkdown {
			printMarkdownHelp(rootCmd)
			return
		}

		// Default help behavior
		if len(args) == 0 {
			_ = rootCmd.Help()
			fmt.Println()
			fmt.Println("💡 Tip: Use 'conductor help --markdown' for AI-friendly documentation")
			return
		}

		// Find the command and show its help
		targetCmd, _, err := rootCmd.Find(args)
		if err != nil {
			fmt.Printf("Unknown command: %s\n", strings.Join(args, " "))
			return
		}
		_ = targetCmd.Help()
	},
}

func init() {
	helpCmd.Flags().BoolVar(&helpMarkdown, "markdown", false, "Output documentation in markdown format (AI-friendly)")
	// Replace Cobra's default help command with our custom one
	rootCmd.SetHelpCommand(helpCmd)
}

func printMarkdownHelp(cmd *cobra.Command) {
	fmt.Println("# Conductor CLI Reference")
	fmt.Println()
	fmt.Println("Conductor is a CLI tool for managing git worktrees with isolated development environments.")
	fmt.Println()
	fmt.Printf("**Version:** %s\n", version)
	fmt.Println()
	fmt.Println("## Commands")
	fmt.Println()

	// Group commands by category
	categories := map[string][]*cobra.Command{
		"Core":       {},
		"Project":    {},
		"Worktree":   {},
		"Database":   {},
		"Tunnel":     {},
		"Utilities":  {},
	}

	for _, c := range cmd.Commands() {
		if c.Hidden {
			continue
		}
		switch c.Name() {
		case "project":
			categories["Project"] = append(categories["Project"], c)
		case "worktree":
			categories["Worktree"] = append(categories["Worktree"], c)
		case "database", "db":
			categories["Database"] = append(categories["Database"], c)
		case "tunnel":
			categories["Tunnel"] = append(categories["Tunnel"], c)
		case "init", "setup", "run", "archive", "status":
			categories["Core"] = append(categories["Core"], c)
		default:
			categories["Utilities"] = append(categories["Utilities"], c)
		}
	}

	// Print in order
	categoryOrder := []string{"Core", "Project", "Worktree", "Database", "Tunnel", "Utilities"}
	for _, cat := range categoryOrder {
		commands := categories[cat]
		if len(commands) == 0 {
			continue
		}

		fmt.Printf("### %s\n\n", cat)
		for _, c := range commands {
			printCommandMarkdown(c, 0)
		}
	}

	// Print environment variables
	fmt.Println("## Environment Variables")
	fmt.Println()
	fmt.Println("Conductor injects these variables when running scripts:")
	fmt.Println()
	fmt.Println("| Variable | Description |")
	fmt.Println("|----------|-------------|")
	fmt.Println("| `CONDUCTOR_PROJECT_NAME` | Project name |")
	fmt.Println("| `CONDUCTOR_WORKSPACE_NAME` | Worktree name |")
	fmt.Println("| `CONDUCTOR_WORKTREE_PATH` | Worktree directory path |")
	fmt.Println("| `CONDUCTOR_PORT` | Primary allocated port |")
	fmt.Println("| `PORT` | Alias for primary port |")
	fmt.Println("| `CONDUCTOR_PORTS` | Comma-separated list of all ports |")
	fmt.Println("| `CONDUCTOR_PORT_N` | Individual ports (0, 1, 2...) |")
	fmt.Println("| `CONDUCTOR_PORT_<LABEL>` | Labeled ports (e.g., `CONDUCTOR_PORT_WEB`) |")
	fmt.Println("| `CONDUCTOR_TUNNEL_URL` | Active tunnel URL (if any) |")
	fmt.Println()

	// Print configuration
	fmt.Println("## Configuration Files")
	fmt.Println()
	fmt.Println("- **Global config:** `~/.conductor/conductor.json`")
	fmt.Println("- **Project config:** `<project>/conductor.json`")
	fmt.Println("- **Scripts directory:** `<project>/.conductor-scripts/`")
	fmt.Println()
}

func printCommandMarkdown(cmd *cobra.Command, depth int) {
	indent := strings.Repeat("  ", depth)

	// Command header
	fmt.Printf("%s#### `conductor %s`\n\n", indent, cmd.CommandPath()[10:]) // Remove "conductor " prefix

	// Description
	if cmd.Long != "" {
		fmt.Printf("%s%s\n\n", indent, strings.Split(cmd.Long, "\n")[0])
	} else if cmd.Short != "" {
		fmt.Printf("%s%s\n\n", indent, cmd.Short)
	}

	// Usage
	if cmd.Use != "" && !cmd.HasSubCommands() {
		fmt.Printf("%s**Usage:** `conductor %s`\n\n", indent, cmd.UseLine()[10:])
	}

	// Aliases
	if len(cmd.Aliases) > 0 {
		fmt.Printf("%s**Aliases:** %s\n\n", indent, strings.Join(cmd.Aliases, ", "))
	}

	// Flags
	if cmd.HasLocalFlags() {
		fmt.Printf("%s**Flags:**\n\n", indent)
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden {
				return
			}
			shorthand := ""
			if f.Shorthand != "" {
				shorthand = fmt.Sprintf("-%s, ", f.Shorthand)
			}
			fmt.Printf("%s- `%s--%s`: %s", indent, shorthand, f.Name, f.Usage)
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" {
				fmt.Printf(" (default: %s)", f.DefValue)
			}
			fmt.Println()
		})
		fmt.Println()
	}

	// Subcommands
	if cmd.HasSubCommands() {
		subCmds := cmd.Commands()
		// Sort by name
		sort.Slice(subCmds, func(i, j int) bool {
			return subCmds[i].Name() < subCmds[j].Name()
		})

		fmt.Printf("%s**Subcommands:**\n\n", indent)
		fmt.Printf("%s| Command | Description |\n", indent)
		fmt.Printf("%s|---------|-------------|\n", indent)
		for _, sub := range subCmds {
			if sub.Hidden {
				continue
			}
			fmt.Printf("%s| `%s` | %s |\n", indent, sub.Name(), sub.Short)
		}
		fmt.Println()
	}
}
