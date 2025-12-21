package workspace

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// SetupManager handles background setup processes
type SetupManager struct {
	mu      sync.RWMutex
	logs    map[string]*bytes.Buffer // key: "project/worktree"
	running map[string]bool
}

// NewSetupManager creates a new setup manager
func NewSetupManager() *SetupManager {
	return &SetupManager{
		logs:    make(map[string]*bytes.Buffer),
		running: make(map[string]bool),
	}
}

// Global setup manager instance
var globalSetupManager = NewSetupManager()

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
	projectPath, worktreePath, projectName, worktreeName string,
	worktree *config.Worktree,
	onComplete func(success bool, err error),
) {
	key := sm.key(projectName, worktreeName)

	// Initialize log buffer
	sm.mu.Lock()
	sm.logs[key] = &bytes.Buffer{}
	sm.running[key] = true
	worktree.SetupStatus = config.SetupStatusRunning
	sm.mu.Unlock()

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
				logFile.Close()
			}

			sm.mu.Lock()
			sm.running[key] = false
			if success {
				worktree.SetupStatus = config.SetupStatusDone
			} else {
				worktree.SetupStatus = config.SetupStatusFailed
			}
			sm.mu.Unlock()

			if onComplete != nil {
				onComplete(success, setupErr)
			}
		}()

		// Check for setup script in .conductor-scripts/setup.sh
		scriptPath := filepath.Join(projectPath, ".conductor-scripts", "setup.sh")
		var cmd *exec.Cmd

		if _, err := os.Stat(scriptPath); err == nil {
			cmd = exec.Command("bash", scriptPath)
		} else {
			// Check for inline setup script in conductor.json
			projectConfig, err := config.LoadProjectConfig(projectPath)
			if err != nil || projectConfig == nil || projectConfig.Scripts == nil {
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

		cmd.Dir = worktreePath

		// Build environment variables
		env := os.Environ()
		env = append(env, fmt.Sprintf("CONDUCTOR_PROJECT=%s", projectName))
		env = append(env, fmt.Sprintf("CONDUCTOR_WORKTREE=%s", worktreeName))
		env = append(env, fmt.Sprintf("CONDUCTOR_WORKTREE_PATH=%s", worktreePath))
		if len(worktree.Ports) > 0 {
			env = append(env, fmt.Sprintf("CONDUCTOR_PORT=%d", worktree.Ports[0]))
		}
		cmd.Env = env

		// Capture output to both memory buffer and file
		sm.mu.RLock()
		logBuf := sm.logs[key]
		sm.mu.RUnlock()

		// Write header to log
		header := fmt.Sprintf("=== Setup started at %s ===\n", time.Now().Format(time.RFC3339))
		logBuf.WriteString(header)
		if logFile != nil {
			logFile.WriteString(header)
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
		logBuf.WriteString(footer)
		if logFile != nil {
			logFile.WriteString(footer)
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
	projectPath, worktreePath, projectName, worktreeName string,
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
			logFile.Close()
		}
	}()

	// Check for archive script in .conductor-scripts/archive.sh
	scriptPath := filepath.Join(projectPath, ".conductor-scripts", "archive.sh")
	var cmd *exec.Cmd

	if _, err := os.Stat(scriptPath); err == nil {
		cmd = exec.Command("bash", scriptPath)
	} else {
		// Check for inline archive script in conductor.json
		projectConfig, err := config.LoadProjectConfig(projectPath)
		if err != nil || projectConfig == nil || projectConfig.Scripts == nil {
			return nil // No archive script, nothing to do
		}

		script, ok := projectConfig.Scripts["archive"]
		if !ok {
			return nil // No archive script, nothing to do
		}

		cmd = exec.Command("bash", "-c", script)
	}

	cmd.Dir = worktreePath

	// Build environment variables
	env := os.Environ()
	env = append(env, fmt.Sprintf("CONDUCTOR_PROJECT=%s", projectName))
	env = append(env, fmt.Sprintf("CONDUCTOR_WORKTREE=%s", worktreeName))
	env = append(env, fmt.Sprintf("CONDUCTOR_WORKTREE_PATH=%s", worktreePath))
	if len(worktree.Ports) > 0 {
		env = append(env, fmt.Sprintf("CONDUCTOR_PORT=%d", worktree.Ports[0]))
	}
	cmd.Env = env

	// Write header to log
	header := fmt.Sprintf("=== Archive started at %s ===\n", time.Now().Format(time.RFC3339))
	if logFile != nil {
		logFile.WriteString(header)
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
		logFile.WriteString(footer)
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
