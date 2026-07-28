package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Restart replaces the current process with the (updated) binary on disk.
// The calling code should save session state BEFORE calling this.
// This function does not return on success.
func Restart() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	// Re-exec with the same args and environment.
	// The binary on disk has already been replaced by DownloadAndInstall.
	return syscall.Exec(executable, os.Args, os.Environ())
}
