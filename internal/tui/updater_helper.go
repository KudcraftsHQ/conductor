package tui

import (
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/updater"
)

// performUpdateCheckImpl performs the actual update check
func (m *Model) performUpdateCheckImpl() UpdateCheckMsg {
	u := updater.New(m.version, "")

	updateInfo, err := u.CheckForUpdate()
	if err != nil {
		return UpdateCheckMsg{Err: err}
	}

	// Update last check time in config
	m.config.Updates.LastCheck = time.Now()
	if updateInfo.UpdateAvailable {
		m.config.Updates.LastVersion = updateInfo.LatestVersion
	}
	_ = config.Save(m.config)

	// If auto-download is enabled and update is available, download it
	if updateInfo.UpdateAvailable && m.config.Updates.AutoDownload {
		// Check if we can write to the binary location
		_, writable, err := updater.GetInstallLocation()
		if err == nil && writable {
			// Download and install in background
			go m.downloadAndInstallUpdate(updateInfo)
		}
	}

	return UpdateCheckMsg{
		UpdateAvailable: updateInfo.UpdateAvailable,
		LatestVersion:   updateInfo.LatestVersion,
	}
}

// downloadAndInstallUpdate downloads and installs an update in the background
func (m *Model) downloadAndInstallUpdate(updateInfo *updater.UpdateInfo) {
	u := updater.New(m.version, "")
	err := u.DownloadAndInstall(updateInfo)

	// Note: We can't send a tea.Msg from a goroutine spawned outside of a Command
	// So we'll just update the config here. The actual notification will happen
	// on next update check or manual refresh.
	if err == nil {
		m.config.Updates.LastVersion = updateInfo.LatestVersion
		m.updateDownloaded = true
		_ = config.Save(m.config)
	}
}
