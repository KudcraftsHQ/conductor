package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// PIDFile stores process information for recovery across TUI restarts
type PIDFile struct {
	PID          int              `json:"pid"`
	ProjectName  string           `json:"project"`
	WorktreeName string           `json:"worktree"`
	Mode         config.TunnelMode `json:"mode"`
	Port         int              `json:"port"`
	URL          string           `json:"url"`
	StartedAt    time.Time        `json:"startedAt"`
}

// TunnelsDir returns the path to the tunnels directory
func TunnelsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".conductor", "tunnels"), nil
}

// ProjectTunnelsDir returns the path to a project's tunnels directory
func ProjectTunnelsDir(projectName string) (string, error) {
	tunnelsDir, err := TunnelsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(tunnelsDir, projectName), nil
}

// PIDFilePath returns the path to a worktree's PID file
func PIDFilePath(projectName, worktreeName string) (string, error) {
	projectDir, err := ProjectTunnelsDir(projectName)
	if err != nil {
		return "", err
	}
	return filepath.Join(projectDir, worktreeName+".pid"), nil
}

// WritePIDFile persists tunnel process info to disk
func WritePIDFile(projectName, worktreeName string, pf *PIDFile) error {
	pidPath, err := PIDFilePath(projectName, worktreeName)
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return fmt.Errorf("failed to create tunnels directory: %w", err)
	}

	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal PID file: %w", err)
	}

	if err := os.WriteFile(pidPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	return nil
}

// ReadPIDFile reads persisted tunnel info from disk
func ReadPIDFile(projectName, worktreeName string) (*PIDFile, error) {
	pidPath, err := PIDFilePath(projectName, worktreeName)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read PID file: %w", err)
	}

	var pf PIDFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal PID file: %w", err)
	}

	return &pf, nil
}

// DeletePIDFile removes the PID file for a worktree
func DeletePIDFile(projectName, worktreeName string) error {
	pidPath, err := PIDFilePath(projectName, worktreeName)
	if err != nil {
		return err
	}

	if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete PID file: %w", err)
	}

	return nil
}

// IsProcessRunning checks if a process with the given PID is still alive
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix, sending signal 0 checks if process exists without affecting it
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// KillProcess terminates a tunnel process by PID
func KillProcess(pid int) error {
	if pid <= 0 {
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return nil // Process doesn't exist
	}

	// Try graceful termination first
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// If SIGTERM fails, try SIGKILL
		if err := process.Signal(syscall.SIGKILL); err != nil {
			return fmt.Errorf("failed to kill process %d: %w", pid, err)
		}
	}

	return nil
}

// ListPIDFiles returns all PID files across all projects
func ListPIDFiles() ([]*PIDFile, error) {
	tunnelsDir, err := TunnelsDir()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(tunnelsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var pidFiles []*PIDFile

	// Walk through all project directories
	entries, err := os.ReadDir(tunnelsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read tunnels directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projectName := entry.Name()
		projectDir := filepath.Join(tunnelsDir, projectName)

		// Read all PID files in this project directory
		pidEntries, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}

		for _, pidEntry := range pidEntries {
			if pidEntry.IsDir() || filepath.Ext(pidEntry.Name()) != ".pid" {
				continue
			}

			worktreeName := pidEntry.Name()[:len(pidEntry.Name())-4] // Remove .pid extension
			pf, err := ReadPIDFile(projectName, worktreeName)
			if err != nil || pf == nil {
				continue
			}

			pidFiles = append(pidFiles, pf)
		}
	}

	return pidFiles, nil
}

// CleanupStalePIDFiles removes PID files for processes that are no longer running
func CleanupStalePIDFiles() error {
	pidFiles, err := ListPIDFiles()
	if err != nil {
		return err
	}

	for _, pf := range pidFiles {
		if !IsProcessRunning(pf.PID) {
			_ = DeletePIDFile(pf.ProjectName, pf.WorktreeName)
		}
	}

	return nil
}
