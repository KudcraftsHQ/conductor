package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/detect"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/spf13/cobra"
)

var (
	setupSkipTrustLayer bool
	setupSkipProofShot  bool
	setupForce          bool
)

var projectSetupCmd = &cobra.Command{
	Use:   "setup [project-name]",
	Short: "Detect project type and check tooling availability",
	Long: `Detect the project's framework, language, and UI type, then check
if TrustLayer and ProofShot are available for this project.

If no project name is given, detects from the current directory.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		// Resolve project
		var projectName string
		var projectPath string

		if len(args) > 0 {
			projectName = args[0]
			path, ok := s.GetProjectPath(projectName)
			if !ok {
				return fmt.Errorf("project '%s' not found", projectName)
			}
			projectPath = path
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			snap := s.GetConfigSnapshot()
			name, proj, _, err := snap.DetectProject(cwd)
			if err != nil {
				return fmt.Errorf("not in a registered project (use 'conductor project add' first)")
			}
			projectName = name
			projectPath = proj.Path
		}

		// Check if already detected
		project, ok := s.GetProject(projectName)
		if !ok {
			return fmt.Errorf("project '%s' not found", projectName)
		}
		if project.Tooling != nil && !setupForce {
			fmt.Printf("Project '%s' already detected:\n", projectName)
			// Load auth from conductor.json for display
			existingCfg, _ := config.LoadProjectConfig(projectPath)
			var existingAuth *detect.AuthInfo
			if existingCfg != nil && existingCfg.Auth != nil {
				existingAuth = &detect.AuthInfo{
					Type:        existingCfg.Auth.Type,
					LoginURL:    existingCfg.Auth.LoginURL,
					SeedCommand: existingCfg.Auth.SeedCommand,
				}
			}
			printToolingSummary(projectName, project.Tooling, existingAuth)
			fmt.Println("\nUse --force to re-detect.")
			return nil
		}

		// Run detection
		info, err := detect.DetectProject(projectPath)
		if err != nil {
			return fmt.Errorf("detection failed: %w", err)
		}

		tooling := &config.ProjectTooling{
			DetectedAt:     time.Now(),
			Framework:      info.Framework,
			Language:       info.Language,
			PackageManager: info.PackageManager,
			TestFramework:  info.TestFramework,
			WebEligible:    info.WebEligible,
			UIType:         info.UIType,
		}

		// Check ProofShot availability
		if !setupSkipProofShot && info.WebEligible {
			if _, err := exec.LookPath("proofshot"); err == nil {
				tooling.ProofShotReady = true
			}
		}

		// Check TrustLayer initialization
		if !setupSkipTrustLayer {
			tlConfigPath := filepath.Join(projectPath, ".claude", "trustlayer", "config.json")
			if _, err := os.Stat(tlConfigPath); err == nil {
				tooling.TrustLayerInit = true
			}
		}

		// Store in global config
		if err := s.SetProjectTooling(projectName, tooling); err != nil {
			return fmt.Errorf("failed to save tooling: %w", err)
		}

		// Detect auth
		authInfo := detect.DetectAuth(projectPath)

		// Update project-level conductor.json if it exists
		projectCfg, err := config.LoadProjectConfig(projectPath)
		if err == nil && projectCfg != nil {
			projectCfg.Tooling = &config.ProjectToolingConfig{
				Framework:      info.Framework,
				Language:       info.Language,
				PackageManager: info.PackageManager,
				WebEligible:    info.WebEligible,
				UIType:         info.UIType,
			}
			// Set auth config if detected and not already configured
			if authInfo.Type != "none" && projectCfg.Auth == nil {
				projectCfg.Auth = &config.AuthConfig{
					Type:        authInfo.Type,
					LoginURL:    authInfo.LoginURL,
					SeedCommand: authInfo.SeedCommand,
					CallbackURL: authInfo.CallbackURL,
				}
			}
			if err := config.SaveProjectConfig(projectPath, projectCfg); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to update conductor.json: %v\n", err)
			}
		}

		// Print summary
		printToolingSummary(projectName, tooling, authInfo)

		// Print action items
		fmt.Println()
		if info.WebEligible && !tooling.ProofShotReady {
			fmt.Println("  To enable ProofShot (visual verification):")
			fmt.Println("    npm install -g proofshot && proofshot install")
			fmt.Println()
		}
		if !setupSkipTrustLayer && !tooling.TrustLayerInit {
			// Check if TrustLayer is globally installed
			homeDir, _ := os.UserHomeDir()
			tlSkill := filepath.Join(homeDir, ".claude", "skills", "spec-init", "SKILL.md")
			if _, err := os.Stat(tlSkill); err == nil {
				fmt.Println("  To initialize TrustLayer (spec-driven TDD):")
				fmt.Println("    Run /spec-setup in Claude Code")
				fmt.Println()
			}
		}

		return nil
	},
}

func printToolingSummary(name string, tooling *config.ProjectTooling, authInfo *detect.AuthInfo) {
	fmt.Printf("\nProject: %s\n", name)
	fmt.Printf("  Framework:       %s\n", tooling.Framework)
	fmt.Printf("  Language:        %s\n", tooling.Language)
	fmt.Printf("  Package Manager: %s\n", tooling.PackageManager)
	if tooling.TestFramework != "" {
		fmt.Printf("  Test Framework:  %s\n", tooling.TestFramework)
	}
	fmt.Printf("  UI Type:         %s\n", tooling.UIType)
	fmt.Printf("  Web Eligible:    %t\n", tooling.WebEligible)
	fmt.Println()

	// Auth status
	if authInfo != nil && authInfo.Type != "none" {
		fmt.Printf("  Auth:            %s\n", authInfo.Type)
		if authInfo.LoginURL != "" {
			fmt.Printf("  Login URL:       %s\n", authInfo.LoginURL)
		}
		if authInfo.SeedCommand != "" {
			fmt.Printf("  Seed Command:    %s\n", authInfo.SeedCommand)
		}
	} else {
		fmt.Println("  Auth:            none")
	}
	fmt.Println()

	// ProofShot status
	if tooling.WebEligible {
		if tooling.ProofShotReady {
			fmt.Println("  ProofShot:       ready")
		} else {
			fmt.Println("  ProofShot:       not installed (web project — install recommended)")
		}
	} else {
		fmt.Printf("  ProofShot:       not applicable (%s project)\n", tooling.UIType)
	}

	// TrustLayer status
	if tooling.TrustLayerInit {
		fmt.Println("  TrustLayer:      initialized")
	} else {
		fmt.Println("  TrustLayer:      not initialized")
	}
}

func init() {
	projectSetupCmd.Flags().BoolVar(&setupSkipTrustLayer, "skip-trustlayer", false, "Skip TrustLayer check")
	projectSetupCmd.Flags().BoolVar(&setupSkipProofShot, "skip-proofshot", false, "Skip ProofShot check")
	projectSetupCmd.Flags().BoolVar(&setupForce, "force", false, "Re-detect even if already detected")

	projectCmd.AddCommand(projectSetupCmd)
}
