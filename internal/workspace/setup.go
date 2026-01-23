package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/database"
	"github.com/hammashamzah/conductor/internal/runner"
	"github.com/hammashamzah/conductor/internal/store"
)

// Note: conductorDir parameter kept for API compatibility but unused in V3

// SetupManager handles background setup processes
type SetupManager struct {
	mu      sync.RWMutex
	logs    map[string]*bytes.Buffer // key: "project/worktree"
	running map[string]bool
	store   *store.Store
}

// NewSetupManager creates a new setup manager
func NewSetupManager(s *store.Store) *SetupManager {
	return &SetupManager{
		logs:    make(map[string]*bytes.Buffer),
		running: make(map[string]bool),
		store:   s,
	}
}

// Global setup manager instance (initialized via InitSetupManager)
var globalSetupManager *SetupManager

// InitSetupManager initializes the global setup manager with a store
func InitSetupManager(s *store.Store) {
	globalSetupManager = NewSetupManager(s)
}

// GetSetupManager returns the global setup manager
func GetSetupManager() *SetupManager {
	return globalSetupManager
}

// key generates a unique key for project/worktree
func (sm *SetupManager) key(projectName, worktreeName string) string {
	return projectName + "/" + worktreeName
}

// IsRunning checks if setup is running for a worktree
func (sm *SetupManager) IsRunning(projectName, worktreeName string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.running[sm.key(projectName, worktreeName)]
}

// GetLogs returns the setup logs for a worktree (from memory or file)
func (sm *SetupManager) GetLogs(projectName, worktreeName string) string {
	sm.mu.RLock()
	if buf, ok := sm.logs[sm.key(projectName, worktreeName)]; ok {
		sm.mu.RUnlock()
		return buf.String()
	}
	sm.mu.RUnlock()

	// Try to read from persisted log file
	logPath := sm.LogFilePath(projectName, worktreeName)
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(data)
}

// LogFilePath returns the path to the setup log file
func (sm *SetupManager) LogFilePath(projectName, worktreeName string) string {
	conductorDir, err := config.ConductorDir()
	if err != nil {
		return ""
	}
	return filepath.Join(conductorDir, "logs", projectName, worktreeName+"-setup.log")
}

// RunSetupResult contains the result of a setup run
type RunSetupResult struct {
	ProjectName  string
	WorktreeName string
	Success      bool
	Error        error
}

// RunSetupAsync runs the setup script in the background
// Returns a channel that will receive the result when done
func (sm *SetupManager) RunSetupAsync(
	project *config.Project, projectName, worktreeName string,
	worktree *config.Worktree,
	onComplete func(success bool, err error),
) {
	key := sm.key(projectName, worktreeName)

	// Initialize log buffer
	sm.mu.Lock()
	sm.logs[key] = &bytes.Buffer{}
	sm.running[key] = true
	sm.mu.Unlock()

	// Update status via store
	_ = sm.store.SetWorktreeStatus(projectName, worktreeName, config.SetupStatusRunning)

	go func() {
		var success bool
		var setupErr error

		// Create log file
		logPath := sm.LogFilePath(projectName, worktreeName)
		if logPath != "" {
			if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create log directory: %v\n", err)
			}
		}
		var logFile *os.File
		var fileErr error
		if logPath != "" {
			logFile, fileErr = os.Create(logPath)
		}
		if fileErr != nil {
			logFile = nil
		}

		defer func() {
			if logFile != nil {
				_ = logFile.Close()
			}

			sm.mu.Lock()
			sm.running[key] = false
			sm.mu.Unlock()

			// Update status via store
			if success {
				_ = sm.store.SetWorktreeStatus(projectName, worktreeName, config.SetupStatusDone)
			} else {
				_ = sm.store.SetWorktreeStatus(projectName, worktreeName, config.SetupStatusFailed)
			}

			if onComplete != nil {
				onComplete(success, setupErr)
			}
		}()

		// Load project config for environment variables
		projectConfig, _ := config.LoadProjectConfig(project.Path)

		// Clone database if configured for this worktree
		if worktree.DatabaseName != "" {
			defaults := sm.store.GetDefaults()
			if defaults.LocalPostgresURL != "" {
				// Log database cloning
				dbMsg := fmt.Sprintf("Cloning database to %s...\n", worktree.DatabaseName)
				sm.mu.Lock()
				if buf, ok := sm.logs[key]; ok {
					buf.WriteString(dbMsg)
				}
				sm.mu.Unlock()
				if logFile != nil {
					logFile.WriteString(dbMsg)
				}

				// Get conductor directory for dbsync storage
				conductorDir, _ := config.ConductorDir()

				// Clone the database
				if err := cloneWorktreeDB(defaults.LocalPostgresURL, worktree.DatabaseName, projectName, conductorDir); err != nil {
					errMsg := fmt.Sprintf("Warning: database clone failed: %v\n", err)
					sm.mu.Lock()
					if buf, ok := sm.logs[key]; ok {
						buf.WriteString(errMsg)
					}
					sm.mu.Unlock()
					if logFile != nil {
						logFile.WriteString(errMsg)
					}
					// Continue anyway - setup script may handle missing DB
				} else {
					successMsg := fmt.Sprintf("Database %s created successfully\n", worktree.DatabaseName)
					sm.mu.Lock()
					if buf, ok := sm.logs[key]; ok {
						buf.WriteString(successMsg)
					}
					sm.mu.Unlock()
					if logFile != nil {
						logFile.WriteString(successMsg)
					}
				}
			}
		}

		// Check for setup script in .conductor-scripts/setup.sh
		scriptPath := filepath.Join(project.Path, ".conductor-scripts", "setup.sh")
		var cmd *exec.Cmd

		if _, err := os.Stat(scriptPath); err == nil {
			cmd = exec.Command("bash", scriptPath)
		} else {
			// Check for inline setup script in conductor.json
			if projectConfig == nil || projectConfig.Scripts == nil {
				success = true // No setup script, consider it success
				return
			}

			script, ok := projectConfig.Scripts["setup"]
			if !ok {
				success = true // No setup script, consider it success
				return
			}

			cmd = exec.Command("bash", "-c", script)
		}

		cmd.Dir = worktree.Path

		// Build environment variables using the same function as CLI runner
		cmd.Env = runner.BuildEnv(projectName, project, worktreeName, worktree, projectConfig)

		// Capture output to both memory buffer and file
		sm.mu.RLock()
		logBuf := sm.logs[key]
		sm.mu.RUnlock()

		// Write header to log
		header := fmt.Sprintf("=== Setup started at %s ===\n", time.Now().Format(time.RFC3339))
		_, _ = logBuf.WriteString(header)
		if logFile != nil {
			_, _ = logFile.WriteString(header)
		}

		// Create multi-writer to write to both buffer and file
		var output io.Writer = logBuf
		if logFile != nil {
			output = io.MultiWriter(logBuf, logFile)
		}

		cmd.Stdout = output
		cmd.Stderr = output

		setupErr = cmd.Run()
		success = setupErr == nil

		// Write footer to log
		var footer string
		if success {
			footer = fmt.Sprintf("\n=== Setup completed successfully at %s ===\n", time.Now().Format(time.RFC3339))
		} else {
			footer = fmt.Sprintf("\n=== Setup FAILED at %s: %v ===\n", time.Now().Format(time.RFC3339), setupErr)
		}
		_, _ = logBuf.WriteString(footer)
		if logFile != nil {
			_, _ = logFile.WriteString(footer)
		}
	}()
}

// ClearLogs clears the logs for a worktree
func (sm *SetupManager) ClearLogs(projectName, worktreeName string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.logs, sm.key(projectName, worktreeName))
}

// ArchiveLogFilePath returns the path to the archive log file
func (sm *SetupManager) ArchiveLogFilePath(projectName, worktreeName string) string {
	conductorDir, err := config.ConductorDir()
	if err != nil {
		return ""
	}
	return filepath.Join(conductorDir, "logs", projectName, worktreeName+"-archive.log")
}

// RunArchiveScript runs the archive script synchronously and logs output
// Returns error if script fails, but caller should still proceed with deletion
func (sm *SetupManager) RunArchiveScript(
	project *config.Project, projectName, worktreeName string,
	worktree *config.Worktree,
) error {
	// Create log file
	logPath := sm.ArchiveLogFilePath(projectName, worktreeName)
	var logFile *os.File
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create log directory: %v\n", err)
		}
		var err error
		logFile, err = os.Create(logPath)
		if err != nil {
			logFile = nil
		}
	}
	defer func() {
		if logFile != nil {
			_ = logFile.Close()
		}
	}()

	// Load project config for environment variables
	projectConfig, _ := config.LoadProjectConfig(project.Path)

	// Check for archive script in .conductor-scripts/archive.sh
	scriptPath := filepath.Join(project.Path, ".conductor-scripts", "archive.sh")
	var cmd *exec.Cmd

	if _, err := os.Stat(scriptPath); err == nil {
		cmd = exec.Command("bash", scriptPath)
	} else {
		// Check for inline archive script in conductor.json
		if projectConfig == nil || projectConfig.Scripts == nil {
			return nil // No archive script, nothing to do
		}

		script, ok := projectConfig.Scripts["archive"]
		if !ok {
			return nil // No archive script, nothing to do
		}

		cmd = exec.Command("bash", "-c", script)
	}

	cmd.Dir = worktree.Path

	// Build environment variables using the same function as CLI runner
	cmd.Env = runner.BuildEnv(projectName, project, worktreeName, worktree, projectConfig)

	// Write header to log
	header := fmt.Sprintf("=== Archive started at %s ===\n", time.Now().Format(time.RFC3339))
	if logFile != nil {
		_, _ = logFile.WriteString(header)
	}

	// Capture output to file
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	archiveErr := cmd.Run()

	// Write footer to log
	var footer string
	if archiveErr == nil {
		footer = fmt.Sprintf("\n=== Archive completed successfully at %s ===\n", time.Now().Format(time.RFC3339))
	} else {
		footer = fmt.Sprintf("\n=== Archive FAILED at %s: %v ===\n", time.Now().Format(time.RFC3339), archiveErr)
	}
	if logFile != nil {
		_, _ = logFile.WriteString(footer)
	}

	return archiveErr
}

// GetArchiveLogs returns the archive logs for a worktree from file
func (sm *SetupManager) GetArchiveLogs(projectName, worktreeName string) string {
	logPath := sm.ArchiveLogFilePath(projectName, worktreeName)
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(data)
}

// runSetupSync runs the setup script synchronously (for CLI use)
// This is a standalone function that doesn't require SetupManager
func runSetupSync(project *config.Project, projectName, worktreeName string, worktree *config.Worktree) error {
	// Create log file
	conductorDir, err := config.ConductorDir()
	if err != nil {
		return fmt.Errorf("failed to get conductor dir: %w", err)
	}
	logPath := filepath.Join(conductorDir, "logs", projectName, worktreeName+"-setup.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create log directory: %v\n", err)
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		logFile = nil
	}
	defer func() {
		if logFile != nil {
			_ = logFile.Close()
		}
	}()

	// Clone database if configured for this worktree (V3 only)
	if worktree.DatabaseName != "" {
		cfg, _ := config.Load()
		if cfg != nil && cfg.Defaults.LocalPostgresURL != "" {
			fmt.Printf("Cloning database to %s...\n", worktree.DatabaseName)
			if logFile != nil {
				fmt.Fprintf(logFile, "Cloning database to %s...\n", worktree.DatabaseName)
			}

			if err := cloneWorktreeDB(cfg.Defaults.LocalPostgresURL, worktree.DatabaseName, projectName, conductorDir); err != nil {
				warnMsg := fmt.Sprintf("Warning: database clone failed: %v\n", err)
				fmt.Print(warnMsg)
				if logFile != nil {
					logFile.WriteString(warnMsg)
				}
				// Continue anyway - setup script may handle missing DB
			} else {
				successMsg := fmt.Sprintf("Database %s cloned successfully\n", worktree.DatabaseName)
				fmt.Print(successMsg)
				if logFile != nil {
					logFile.WriteString(successMsg)
				}
			}
		}
	}

	// Load project config for environment variables
	projectConfig, _ := config.LoadProjectConfig(project.Path)

	// Check for setup script in .conductor-scripts/setup.sh
	scriptPath := filepath.Join(project.Path, ".conductor-scripts", "setup.sh")
	var cmd *exec.Cmd

	if _, err := os.Stat(scriptPath); err == nil {
		cmd = exec.Command("bash", scriptPath)
	} else {
		// Check for inline setup script in conductor.json
		if projectConfig == nil || projectConfig.Scripts == nil {
			return nil // No setup script, consider it success
		}

		script, ok := projectConfig.Scripts["setup"]
		if !ok {
			return nil // No setup script, consider it success
		}

		cmd = exec.Command("bash", "-c", script)
	}

	cmd.Dir = worktree.Path

	// Build environment variables using the same function as CLI runner
	cmd.Env = runner.BuildEnv(projectName, project, worktreeName, worktree, projectConfig)

	// Write header to log and stdout
	header := fmt.Sprintf("=== Setup started at %s ===\n", time.Now().Format(time.RFC3339))
	fmt.Print(header)
	if logFile != nil {
		_, _ = logFile.WriteString(header)
	}

	// Capture output to both stdout and file
	if logFile != nil {
		cmd.Stdout = io.MultiWriter(os.Stdout, logFile)
		cmd.Stderr = io.MultiWriter(os.Stderr, logFile)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	setupErr := cmd.Run()

	// Write footer to log and stdout
	var footer string
	if setupErr == nil {
		footer = fmt.Sprintf("\n=== Setup completed successfully at %s ===\n", time.Now().Format(time.RFC3339))
	} else {
		footer = fmt.Sprintf("\n=== Setup FAILED at %s: %v ===\n", time.Now().Format(time.RFC3339), setupErr)
	}
	fmt.Print(footer)
	if logFile != nil {
		_, _ = logFile.WriteString(footer)
	}

	return setupErr
}


// cloneWorktreeDB clones the golden database to a worktree database (V3 only)
func cloneWorktreeDB(localURL, dbName, projectName, conductorDir string) error {
	// Use V3 golden database clone (no file-based fallback)
	return database.CloneFromGoldenDB(context.Background(), localURL, projectName, dbName, nil)
}

