package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/detect"
	"github.com/hammashamzah/conductor/internal/mux"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	buildDescription string
	buildSpec        string
	buildNoOpen      bool
)

var buildCmd = &cobra.Command{
	Use:   "build <feature-description>",
	Short: "Create a worktree and launch Claude to build a feature end-to-end",
	Long: `Creates a new worktree, launches a tmux window with Claude Code + dev server,
and gives Claude a prompt to build the feature, run the verification pipeline
(TrustLayer + ProofShot), and create a PR with all artifacts.

You can switch to the tmux tab at any time to steer Claude.

Two modes:
  1. From description: Claude builds freely, then freezes into spec
  2. From spec: Claude implements against an approved spec + evals (TDD)

Examples:
  conductor build "Add user authentication with email/password"
  conductor build "Fix the broken checkout flow" --description "Users get a 500 error"
  conductor build --spec specs/auth.feature
  conductor build --spec auth`,
	Args: cobra.ArbitraryArgs,
	RunE: runBuild,
}

func init() {
	buildCmd.Flags().StringVar(&buildDescription, "description", "", "Additional context or requirements")
	buildCmd.Flags().StringVar(&buildSpec, "spec", "", "Build against an existing spec (scope name or path to .feature file)")
	buildCmd.Flags().BoolVar(&buildNoOpen, "no-open", false, "Don't open tmux window (headless)")

	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	featureTitle := strings.Join(args, " ")

	// Validate: need either a description or a spec
	if featureTitle == "" && buildSpec == "" {
		return fmt.Errorf("provide a feature description or --spec flag\n\nExamples:\n  conductor build \"Add dark mode\"\n  conductor build --spec auth")
	}

	s, err := store.Load()
	if err != nil {
		return err
	}
	defer func() { _, _ = s.Close() }()

	// Detect project from cwd
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	snap := s.GetConfigSnapshot()
	projectName, project, _, err := snap.DetectProject(cwd)
	if err != nil {
		return fmt.Errorf("not in a registered project. Run 'conductor project add' first")
	}

	// Load project config
	projectConfig, err := config.LoadProjectConfig(project.Path)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	// Resolve spec if provided
	var specContent, evalContent, scopeName string
	if buildSpec != "" {
		scopeName, specContent, evalContent, err = resolveSpec(project.Path, buildSpec)
		if err != nil {
			return fmt.Errorf("failed to resolve spec: %w", err)
		}
		if featureTitle == "" {
			featureTitle = scopeName
		}
	}

	// Generate branch name
	branch := "feature/" + slugifyForBranch(featureTitle)

	// Get port count
	portCount := project.DefaultPortsPerWorktree
	if portCount == 0 {
		portCount = 1
	}
	if projectConfig != nil && projectConfig.Ports.Default > 0 {
		portCount = projectConfig.Ports.Default
	}

	// Create workspace manager
	mgr := workspace.NewManagerWithStore(snap, s)

	// Prepare worktree (allocate ports, generate name)
	worktreeName, wt, err := mgr.PrepareWorktree(projectName, branch, portCount)
	if err != nil {
		return fmt.Errorf("failed to prepare worktree: %w", err)
	}

	fmt.Printf("Creating worktree '%s' (branch: %s)...\n", worktreeName, branch)

	// Create git worktree async → setup → open coding window
	err = mgr.CreateWorktreeAsync(projectName, worktreeName, func(success bool, createErr error) {
		if !success {
			log.Printf("build: failed to create git worktree: %v", createErr)
			_ = s.SetWorktreeStatus(projectName, worktreeName, config.SetupStatusFailed)
			return
		}

		// Run setup async
		setupErr := mgr.RunSetupAsync(projectName, worktreeName, func(setupSuccess bool, setupErr error) {
			if !setupSuccess {
				log.Printf("build: setup failed: %v", setupErr)
			}

			// Build the prompt and open coding window
			prompt := buildFeaturePrompt(featureTitle, buildDescription, specContent, evalContent, scopeName, project, projectConfig, wt)

			if buildNoOpen {
				fmt.Printf("Worktree ready at %s\n", wt.Path)
				fmt.Println("Prompt saved — run Claude manually in the worktree.")
				return
			}

			if err := mux.Current().CreateCodingWindowWithTask(projectName, wt.Branch, wt.Path, prompt, codingagent.ClaudeCode); err != nil {
				log.Printf("build: failed to create coding window: %v", err)
				return
			}

			fmt.Printf("\nWorktree '%s' ready!\n", worktreeName)
			fmt.Printf("  Branch:  %s\n", branch)
			fmt.Printf("  Path:    %s\n", wt.Path)
			if len(wt.Ports) > 0 {
				fmt.Printf("  Port:    %d\n", wt.Ports[0])
			}
			fmt.Printf("  Window:  %s/%s\n", projectName, branch)
			fmt.Println("\nClaude is building your feature. Switch to the tmux tab to steer.")
		})
		if setupErr != nil {
			log.Printf("build: failed to start setup: %v", setupErr)
			// Still open the window
			fallbackPrompt := buildFeaturePrompt(featureTitle, buildDescription, specContent, evalContent, scopeName, project, projectConfig, wt)
			if !buildNoOpen {
				_ = mux.Current().CreateCodingWindowWithTask(projectName, wt.Branch, wt.Path, fallbackPrompt, codingagent.ClaudeCode)
			}
		}
	})
	if err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	// Wait a moment for the async operations to complete
	// The goroutines handle everything; we just need to not exit immediately
	fmt.Println("Setting up worktree in background...")
	time.Sleep(2 * time.Second)

	return nil
}

// resolveSpec finds a spec file and its eval file from a scope name or path
func resolveSpec(projectPath, spec string) (scopeName, specContent, evalContent string, err error) {
	// Try as direct path first
	specPath := spec
	if !strings.HasSuffix(specPath, ".feature") {
		specPath = fmt.Sprintf("specs/%s.feature", spec)
	}

	fullSpecPath := specPath
	if !filepath.IsAbs(fullSpecPath) {
		fullSpecPath = filepath.Join(projectPath, fullSpecPath)
	}

	specData, err := os.ReadFile(fullSpecPath)
	if err != nil {
		return "", "", "", fmt.Errorf("spec file not found: %s", fullSpecPath)
	}
	specContent = string(specData)

	// Derive scope name
	base := filepath.Base(specPath)
	scopeName = strings.TrimSuffix(base, ".feature")

	// Try to find matching eval file
	evalPath := filepath.Join(projectPath, "specs", "evals", scopeName+".eval.yaml")
	evalData, err := os.ReadFile(evalPath)
	if err == nil {
		evalContent = string(evalData)
	}

	return scopeName, specContent, evalContent, nil
}

func buildFeaturePrompt(title, description, specContent, evalContent, scopeName string, project *config.Project, projectConfig *config.ProjectConfig, wt *config.Worktree) string {
	var sb strings.Builder

	// Detect tooling context
	info, _ := detect.DetectProject(project.Path)
	webEligible := info != nil && info.WebEligible
	hasTrustLayer := project.Tooling != nil && project.Tooling.TrustLayerInit
	hasProofShot := project.Tooling != nil && project.Tooling.ProofShotReady
	isSpecMode := specContent != ""

	// Auth context
	var authType, authLoginURL, authSeedCmd string
	if projectConfig != nil && projectConfig.Auth != nil {
		authType = projectConfig.Auth.Type
		authLoginURL = projectConfig.Auth.LoginURL
		authSeedCmd = projectConfig.Auth.SeedCommand
	}

	sb.WriteString(fmt.Sprintf("## Feature: %s\n\n", title))

	if description != "" {
		sb.WriteString(description)
		sb.WriteString("\n\n")
	}

	// Port info
	if len(wt.Ports) > 0 {
		sb.WriteString(fmt.Sprintf("Dev server port: %d\n", wt.Ports[0]))
	}
	if projectConfig != nil {
		if runCmd, ok := projectConfig.Scripts["run"]; ok {
			sb.WriteString(fmt.Sprintf("Dev server command: %s\n", runCmd))
		}
	}

	// Auth info
	if authType != "" && authType != "none" {
		sb.WriteString(fmt.Sprintf("Auth type: %s\n", authType))
		if authLoginURL != "" {
			sb.WriteString(fmt.Sprintf("Login URL: %s\n", authLoginURL))
		}
		if authSeedCmd != "" {
			sb.WriteString(fmt.Sprintf("Seed command: %s\n", authSeedCmd))
		}
		if authType == "email-password" {
			sb.WriteString("Test credentials: read CONDUCTOR_TEST_EMAIL and CONDUCTOR_TEST_PASSWORD from .env\n")
		}
	}
	sb.WriteString("\n")

	// === SPEC MODE: implement against existing spec ===
	if isSpecMode {
		sb.WriteString("## Mode: Build Against Spec\n\n")
		sb.WriteString("You have an approved spec and evals. Implement using TDD.\n\n")

		sb.WriteString("### Spec (source of truth)\n")
		sb.WriteString("```gherkin\n")
		sb.WriteString(specContent)
		sb.WriteString("\n```\n\n")

		if evalContent != "" {
			sb.WriteString("### Evals (acceptance criteria)\n")
			sb.WriteString("```yaml\n")
			sb.WriteString(evalContent)
			sb.WriteString("\n```\n\n")
		}

		step := 1

		sb.WriteString(fmt.Sprintf("### %d. Implement with TDD\n", step))
		sb.WriteString("Run /spec-build to implement against the evals. Follow RED → GREEN:\n")
		sb.WriteString("- Write failing tests from evals first\n")
		sb.WriteString("- Implement until all tests pass\n")
		sb.WriteString("- Refactor while keeping tests green\n\n")
		step++

		sb.WriteString(fmt.Sprintf("### %d. Verify with pipeline\n", step))
		sb.WriteString("Run /spec-pipeline to run the full verification:\n")
		sb.WriteString("- Review (read-only verification of spec coverage)\n")
		sb.WriteString("- Break (adversarial red-team testing)\n")
		if webEligible && hasProofShot {
			sb.WriteString("- ProofShot (visual verification with video proof)\n")
		}
		sb.WriteString("\n")
		step++

		// ProofShot (if spec-pipeline doesn't cover it or extra step)
		if webEligible && hasProofShot {
			sb.WriteString(fmt.Sprintf("### %d. Visual verification (ProofShot)\n", step))
			sb.WriteString("If /spec-pipeline didn't run ProofShot, verify manually:\n")
			writeProofShotBlock(&sb, projectConfig, wt, title, authType, authLoginURL, authSeedCmd)
			step++
		}

		sb.WriteString(fmt.Sprintf("### %d. Create Pull Request\n", step))
		sb.WriteString("When the merge gate passes:\n")
		sb.WriteString("- Commit all changes\n")
		sb.WriteString("- Create PR with merge gate summary in the body\n")
		if webEligible && hasProofShot {
			sb.WriteString("- Run `proofshot pr` to attach visual proof\n")
		}
		sb.WriteString("- Include eval pass rates and reviewer/breaker findings\n")

		return sb.String()
	}

	// === DESCRIPTION MODE: build from scratch ===
	sb.WriteString("## Instructions\n\n")
	sb.WriteString("Build this feature end-to-end. Follow this workflow:\n\n")

	step := 1
	sb.WriteString(fmt.Sprintf("### %d. Implement the feature\n", step))
	sb.WriteString("- Read the existing codebase to understand patterns and conventions\n")
	sb.WriteString("- Implement the feature with clean, tested code\n")
	sb.WriteString("- Write unit tests alongside the implementation\n\n")
	step++

	// TrustLayer freeze step
	if hasTrustLayer {
		sb.WriteString(fmt.Sprintf("### %d. Freeze into spec (TrustLayer)\n", step))
		sb.WriteString("After implementation is complete, run /spec-freeze to create a Gherkin spec and atomic evals from your implementation.\n")
		sb.WriteString("Present the evals for review, then run /spec-pipeline to verify.\n\n")
		step++
	}

	// ProofShot step
	if webEligible && hasProofShot {
		sb.WriteString(fmt.Sprintf("### %d. Visual verification (ProofShot)\n", step))
		sb.WriteString("After implementation, verify the UI visually:\n")
		writeProofShotBlock(&sb, projectConfig, wt, title, authType, authLoginURL, authSeedCmd)
		step++
	}

	sb.WriteString(fmt.Sprintf("### %d. Create Pull Request\n", step))
	sb.WriteString("When everything is verified:\n")
	sb.WriteString("- Commit all changes with a descriptive message\n")
	sb.WriteString("- Create a PR with a clear description of what was built\n")
	if webEligible && hasProofShot {
		sb.WriteString("- Run `proofshot pr` to attach visual proof artifacts to the PR\n")
	}
	sb.WriteString("- Include test results and any spec/eval summaries in the PR body\n")

	return sb.String()
}

func writeProofShotBlock(sb *strings.Builder, projectConfig *config.ProjectConfig, wt *config.Worktree, title, authType, authLoginURL, authSeedCmd string) {
	// Seed command
	if authSeedCmd != "" && authType == "email-password" {
		sb.WriteString("First, ensure the test account exists:\n")
		sb.WriteString("```bash\n")
		sb.WriteString(authSeedCmd + "\n")
		sb.WriteString("```\n\n")
	}

	sb.WriteString("```bash\n")
	if projectConfig != nil {
		if runCmd, ok := projectConfig.Scripts["run"]; ok && len(wt.Ports) > 0 {
			fmt.Fprintf(sb, "proofshot start --run \"%s\" --port %d --description \"%s\"\n", runCmd, wt.Ports[0], title)
		}
	}

	// Auth login step
	switch authType {
	case "email-password":
		sb.WriteString("\n# Login with test account\n")
		if len(wt.Ports) > 0 {
			fmt.Fprintf(sb, "proofshot exec open http://localhost:%d%s\n", wt.Ports[0], authLoginURL)
		}
		sb.WriteString("proofshot exec snapshot -i\n")
		sb.WriteString("# Find the email and password fields and fill them\n")
		sb.WriteString("# Read CONDUCTOR_TEST_EMAIL and CONDUCTOR_TEST_PASSWORD from .env\n")
		sb.WriteString("proofshot exec fill @email \"$CONDUCTOR_TEST_EMAIL\"\n")
		sb.WriteString("proofshot exec fill @password \"$CONDUCTOR_TEST_PASSWORD\"\n")
		sb.WriteString("proofshot exec click @submit\n")
		sb.WriteString("proofshot exec screenshot step-logged-in.png\n\n")
		sb.WriteString("# Now test the feature\n")
	case "dev-bypass":
		sb.WriteString("\n# Auth: dev-bypass — no login needed, navigate directly\n")
	default:
		sb.WriteString("\n")
	}

	sb.WriteString("proofshot exec snapshot -i\n")
	sb.WriteString("# Navigate, interact, and take screenshots of key states\n")
	sb.WriteString("proofshot exec screenshot step-verification.png\n")
	sb.WriteString("proofshot stop\n")
	sb.WriteString("```\n\n")
}

//nolint:unused // wired up in a follow-up
func boolToStep(condition bool, ifTrue, ifFalse int) int {
	if condition {
		return ifTrue
	}
	return ifFalse
}

func slugifyForBranch(s string) string {
	s = strings.ToLower(s)
	var result strings.Builder
	lastHyphen := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen {
			result.WriteRune('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(result.String(), "-")
	if len(out) > 50 {
		out = out[:50]
		out = strings.TrimRight(out, "-")
	}
	return out
}
