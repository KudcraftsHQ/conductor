package main

import (
	"fmt"
	"os"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/updater"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update conductor to the latest version",
	Long: `Check for updates and install the latest version of conductor.

Auto-updates only work if conductor is installed in a user-writable directory
(e.g., ~/.local/bin). If installed in a system directory (e.g., /usr/local/bin),
use 'conductor migrate' to move to a user directory first.`,
	Run: runUpdate,
}

var updateCheckOnly bool

func init() {
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false, "Only check for updates, don't install")
}

func runUpdate(cmd *cobra.Command, args []string) {
	// Create updater
	u := updater.New(version, "")

	// Check installation location
	path, writable, err := updater.GetInstallLocation()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get installation path: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Current installation: %s\n", path)
	fmt.Printf("Current version: %s\n\n", version)

	if !writable {
		fmt.Fprintf(os.Stderr, "Error: Cannot auto-update. Conductor is installed in a system directory.\n\n")
		fmt.Fprintf(os.Stderr, "To enable auto-updates:\n")
		fmt.Fprintf(os.Stderr, "  1. Run: conductor migrate\n")
		fmt.Fprintf(os.Stderr, "  2. Or manually reinstall to ~/.local/bin with: make install\n")
		os.Exit(1)
	}

	// Check for updates
	fmt.Println("Checking for updates...")
	updateInfo, err := u.CheckForUpdate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to check for updates: %v\n", err)
		os.Exit(1)
	}

	if !updateInfo.UpdateAvailable {
		fmt.Println("✓ You are running the latest version!")
		return
	}

	fmt.Printf("New version available: %s → %s\n", updateInfo.CurrentVersion, updateInfo.LatestVersion)
	fmt.Printf("Release URL: %s\n\n", updateInfo.ReleaseURL)

	if updateCheckOnly {
		return
	}

	// Download and install update
	fmt.Println("Downloading update...")
	if err := u.DownloadAndInstall(updateInfo); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to install update: %v\n", err)
		os.Exit(1)
	}

	// Update config with last check time
	if config.Exists() {
		cfg, err := config.Load()
		if err == nil {
			cfg.Updates.LastCheck = time.Now()
			cfg.Updates.LastVersion = updateInfo.LatestVersion
			config.Save(cfg)
		}
	}

	fmt.Printf("\n✓ Successfully updated to version %s!\n", updateInfo.LatestVersion)
	fmt.Println("\nThe update will take effect the next time you run conductor.")
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate conductor to user directory for auto-updates",
	Long: `Migrate conductor from a system directory to ~/.local/bin to enable auto-updates.

This command will:
  1. Copy the current binary to ~/.local/bin
  2. Set up PATH configuration
  3. Provide instructions to remove the old installation`,
	Run: runMigrate,
}

func runMigrate(cmd *cobra.Command, args []string) {
	// Get current installation
	currentPath, _, err := updater.GetInstallLocation()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get installation path: %v\n", err)
		os.Exit(1)
	}

	// Check if already in user directory
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get home directory: %v\n", err)
		os.Exit(1)
	}

	newPath := home + "/.local/bin/conductor"

	if currentPath == newPath {
		fmt.Println("✓ Conductor is already installed in ~/.local/bin")
		fmt.Println("Auto-updates are enabled!")
		return
	}

	fmt.Println("Migration Plan:")
	fmt.Printf("  Current: %s\n", currentPath)
	fmt.Printf("  New:     %s\n\n", newPath)

	// Create ~/.local/bin if it doesn't exist
	localBinDir := home + "/.local/bin"
	if err := os.MkdirAll(localBinDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create ~/.local/bin: %v\n", err)
		os.Exit(1)
	}

	// Copy binary
	fmt.Println("Copying binary to ~/.local/bin...")
	sourceFile, err := os.Open(currentPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to read current binary: %v\n", err)
		os.Exit(1)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(newPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create new binary: %v\n", err)
		os.Exit(1)
	}
	defer destFile.Close()

	if _, err := sourceFile.WriteTo(destFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to copy binary: %v\n", err)
		os.Exit(1)
	}

	// Set executable permissions
	if err := os.Chmod(newPath, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to set permissions: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Binary copied successfully!")
	fmt.Println()

	// Check if ~/.local/bin is in PATH
	fmt.Println("Next steps:")
	fmt.Println()
	fmt.Println("1. Add ~/.local/bin to your PATH (if not already done):")
	fmt.Println("   Add this line to your ~/.zshrc or ~/.bashrc:")
	fmt.Println()
	fmt.Println("     export PATH=\"$HOME/.local/bin:$PATH\"")
	fmt.Println()
	fmt.Println("2. Reload your shell configuration:")
	fmt.Println("   source ~/.zshrc   # or ~/.bashrc")
	fmt.Println()
	fmt.Println("3. Remove the old installation:")
	if updater.IsSystemInstallation() {
		fmt.Printf("   sudo rm %s\n", currentPath)
	} else {
		fmt.Printf("   rm %s\n", currentPath)
	}
	fmt.Println()
	fmt.Println("4. Verify the migration:")
	fmt.Println("   which conductor")
	fmt.Println("   # Should show: ~/.local/bin/conductor")
	fmt.Println()
	fmt.Println("✓ Migration complete! Auto-updates are now enabled.")
}
