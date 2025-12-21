package main

import (
	"fmt"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize conductor globally",
	Long:  "Creates the conductor config directory and configuration file at ~/.config/conductor/",
	RunE: func(cmd *cobra.Command, args []string) error {
		if config.Exists() {
			return fmt.Errorf("conductor already initialized at ~/.config/conductor/")
		}

		if err := config.Init(); err != nil {
			return fmt.Errorf("failed to initialize: %w", err)
		}

		path, err := config.ConfigPath()
		if err != nil {
			return fmt.Errorf("failed to determine config path: %w", err)
		}
		fmt.Printf("Conductor initialized at %s\n", path)
		fmt.Println("\nNext steps:")
		fmt.Println("  conductor project add .    # Add current directory as a project")
		fmt.Println("  conductor                  # Launch TUI")

		return nil
	},
}
