package updater

import "time"

// UpdateInfo contains information about an available update
type UpdateInfo struct {
	CurrentVersion  string  `json:"currentVersion"`
	LatestVersion   string  `json:"latestVersion"`
	UpdateAvailable bool    `json:"updateAvailable"`
	ReleaseURL      string  `json:"releaseUrl"`
	Assets          []Asset `json:"assets"`
}

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset represents a release asset
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// UpdateSettings represents update configuration
type UpdateSettings struct {
	AutoCheck     bool      `json:"autoCheck"`
	AutoDownload  bool      `json:"autoDownload"`
	CheckInterval string    `json:"checkInterval"`
	Channel       string    `json:"channel"`
	LastCheck     time.Time `json:"lastCheck"`
	LastVersion   string    `json:"lastVersion"`
	NotifyInTUI   bool      `json:"notifyInTUI"`
}

// DefaultUpdateSettings returns the default update settings
func DefaultUpdateSettings() UpdateSettings {
	return UpdateSettings{
		AutoCheck:     true,
		AutoDownload:  true,
		CheckInterval: "6h",
		Channel:       "stable",
		LastCheck:     time.Time{},
		LastVersion:   "",
		NotifyInTUI:   true,
	}
}
