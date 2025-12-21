package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hammashamzah/conductor/internal/config"
)

// Runner executes conductor scripts
type Runner struct {
	config *config.Config
}

// NewRunner creates a new script runner
func NewRunner(cfg *config.Config) *Runner {
	return &Runner{config: cfg}
}

// Run executes a named script (setup, run, archive, etc.)
func (r *Runner) Run(projectName, worktreeName, scriptName string) error {
	project, ok := r.config.GetProject(projectName)
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	worktree, ok := project.Worktrees[worktreeName]
	if !ok {
		return fmt.Errorf("worktree '%s' not found", worktreeName)
	}

	// Load project config
	projectConfig, err := config.LoadProjectConfig(project.Path)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	// Find script
	script, scriptPath, err := r.findScript(project.Path, scriptName, projectConfig)
	if err != nil {
		return err
	}

	// Build environment
	env := BuildEnv(projectName, project, worktreeName, worktree, projectConfig)

	// Execute script
	return r.executeScript(script, scriptPath, worktree.Path, env)
}

// findScript locates the script to run
// Priority: .conductor-scripts/{name}.sh > conductor.json inline
func (r *Runner) findScript(projectPath, scriptName string, projectConfig *config.ProjectConfig) (string, string, error) {
	// Check for external script first
	scriptPath := filepath.Join(projectPath, ".conductor-scripts", scriptName+".sh")
	if _, err := os.Stat(scriptPath); err == nil {
		content, err := os.ReadFile(scriptPath)
		if err != nil {
			return "", "", fmt.Errorf("failed to read script: %w", err)
		}
		return string(content), scriptPath, nil
	}

	// Fall back to inline script in conductor.json
	if projectConfig != nil && projectConfig.Scripts != nil {
		if script, ok := projectConfig.Scripts[scriptName]; ok {
			return script, "", nil
		}
	}

	return "", "", fmt.Errorf("script '%s' not found", scriptName)
}

// executeScript runs the script in the given directory
func (r *Runner) executeScript(script, scriptPath, workDir string, env []string) error {
	var cmd *exec.Cmd

	if scriptPath != "" {
		// Execute external script file
		cmd = exec.Command("bash", scriptPath)
	} else {
		// Execute inline script
		cmd = exec.Command("bash", "-c", script)
	}

	cmd.Dir = workDir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd.Run()
}

// HasScript checks if a script exists for a project
func (r *Runner) HasScript(projectPath, scriptName string) bool {
	// Check external script
	scriptPath := filepath.Join(projectPath, ".conductor-scripts", scriptName+".sh")
	if _, err := os.Stat(scriptPath); err == nil {
		return true
	}

	// Check inline script
	projectConfig, err := config.LoadProjectConfig(projectPath)
	if err != nil || projectConfig == nil {
		return false
	}

	_, ok := projectConfig.Scripts[scriptName]
	return ok
}

// ListScripts returns available scripts for a project
func (r *Runner) ListScripts(projectPath string) []string {
	scripts := make(map[string]bool)

	// Check external scripts
	scriptsDir := filepath.Join(projectPath, ".conductor-scripts")
	entries, _ := os.ReadDir(scriptsDir)
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sh" {
			name := entry.Name()[:len(entry.Name())-3]
			scripts[name] = true
		}
	}

	// Check inline scripts
	projectConfig, err := config.LoadProjectConfig(projectPath)
	if err == nil && projectConfig != nil && projectConfig.Scripts != nil {
		for name := range projectConfig.Scripts {
			scripts[name] = true
		}
	}

	result := make([]string, 0, len(scripts))
	for name := range scripts {
		result = append(result, name)
	}
	return result
}
