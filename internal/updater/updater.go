package updater

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
)

const (
	githubAPIURL      = "https://api.github.com/repos/hammashamzah/conductor/releases/latest"
	checkInterval     = 6 * time.Hour
	updateCheckTimeout = 10 * time.Second
)

// Updater handles auto-updates for conductor
type Updater struct {
	currentVersion string
	configPath     string
}

// New creates a new updater instance
func New(currentVersion string, configPath string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		configPath:     configPath,
	}
}

// CheckForUpdate checks if a newer version is available
func (u *Updater) CheckForUpdate() (*UpdateInfo, error) {
	client := &http.Client{
		Timeout: updateCheckTimeout,
	}

	resp, err := client.Get(githubAPIURL)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release info: %w", err)
	}

	// Remove 'v' prefix from tag if present
	latestVersion := strings.TrimPrefix(release.TagName, "v")

	// Compare versions
	current, err := version.NewVersion(u.currentVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid current version: %w", err)
	}

	latest, err := version.NewVersion(latestVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid latest version: %w", err)
	}

	updateAvailable := latest.GreaterThan(current)

	return &UpdateInfo{
		CurrentVersion:  u.currentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: updateAvailable,
		ReleaseURL:      release.HTMLURL,
		Assets:          release.Assets,
	}, nil
}

// DownloadAndInstall downloads and installs the update
func (u *Updater) DownloadAndInstall(updateInfo *UpdateInfo) error {
	// Get current executable path
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	// Check if we can write to the binary location
	if !canWrite(executable) {
		return fmt.Errorf("insufficient permissions to update binary at %s\nConsider running 'conductor migrate' to move to user directory", executable)
	}

	// Find the appropriate binary for this platform
	binaryName := u.getBinaryName()
	var asset *Asset
	for i := range updateInfo.Assets {
		if strings.Contains(updateInfo.Assets[i].Name, binaryName) {
			asset = &updateInfo.Assets[i]
			break
		}
	}

	if asset == nil {
		return fmt.Errorf("no binary found for platform: %s-%s", runtime.GOOS, runtime.GOARCH)
	}

	// Download the binary
	tmpDir, err := os.MkdirTemp("", "conductor-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, asset.Name)
	if err := u.downloadFile(tmpFile, asset.BrowserDownloadURL); err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}

	// Extract if it's an archive
	var binaryPath string
	if strings.HasSuffix(asset.Name, ".tar.gz") {
		binaryPath, err = u.extractTarGz(tmpFile, tmpDir)
		if err != nil {
			return fmt.Errorf("failed to extract archive: %w", err)
		}
	} else if strings.HasSuffix(asset.Name, ".zip") {
		// TODO: Implement zip extraction for Windows
		return fmt.Errorf("zip extraction not yet implemented")
	} else {
		binaryPath = tmpFile
	}

	// Verify checksum (if available)
	// Note: We would need to download checksums.txt separately
	// For now, we'll skip checksum verification

	// Backup current binary
	backupPath := executable + ".backup"
	if err := copyFile(executable, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// Replace binary
	if err := copyFile(binaryPath, executable); err != nil {
		// Rollback
		copyFile(backupPath, executable)
		return fmt.Errorf("failed to install update: %w", err)
	}

	// Set executable permissions
	if err := os.Chmod(executable, 0755); err != nil {
		// Rollback
		copyFile(backupPath, executable)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Clean up backup
	os.Remove(backupPath)

	return nil
}

// ShouldCheckForUpdate determines if we should check for updates
func (u *Updater) ShouldCheckForUpdate(lastCheck time.Time) bool {
	return time.Since(lastCheck) > checkInterval
}

// getBinaryName returns the expected binary name for the current platform
func (u *Updater) getBinaryName() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	return fmt.Sprintf("conductor-%s-%s", goos, goarch)
}

// downloadFile downloads a file from a URL
func (u *Updater) downloadFile(filepath string, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// extractTarGz extracts a .tar.gz file and returns the path to the binary
func (u *Updater) extractTarGz(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var binaryPath string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		target := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return "", err
			}
			f.Close()

			// This is likely our binary
			if strings.Contains(header.Name, "conductor") && !strings.Contains(header.Name, ".tar.gz") {
				binaryPath = target
			}
		}
	}

	if binaryPath == "" {
		return "", fmt.Errorf("binary not found in archive")
	}

	return binaryPath, nil
}

// canWrite checks if we have write permission to a file
func canWrite(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	// Try to open for writing
	f, err := os.OpenFile(path, os.O_WRONLY, info.Mode())
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// Copy permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, sourceInfo.Mode())
}

// verifySHA256 verifies the SHA256 checksum of a file
func verifySHA256(filePath, expectedChecksum string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}

	actualChecksum := hex.EncodeToString(hash.Sum(nil))
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

// GetInstallLocation returns the current installation path and whether it's user-writable
func GetInstallLocation() (path string, writable bool, err error) {
	executable, err := os.Executable()
	if err != nil {
		return "", false, err
	}

	// Resolve symlinks
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", false, err
	}

	return executable, canWrite(executable), nil
}

// IsSystemInstallation checks if conductor is installed in a system directory
func IsSystemInstallation() bool {
	path, _, err := GetInstallLocation()
	if err != nil {
		return false
	}

	// Common system paths
	systemPaths := []string{
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"C:\\Program Files",
	}

	for _, sysPath := range systemPaths {
		if strings.HasPrefix(path, sysPath) {
			return true
		}
	}

	return false
}
