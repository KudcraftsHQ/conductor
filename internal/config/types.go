package config

import "time"

// Config represents the global conductor configuration
type Config struct {
	Version         int                    `json:"version"`
	Defaults        Defaults               `json:"defaults"`
	Updates         UpdateSettings         `json:"updates"`
	PortAllocations map[string]*PortAlloc  `json:"portAllocations"`
	Projects        map[string]*Project    `json:"projects"`
}

// Defaults contains default settings
type Defaults struct {
	PortsPerWorktree int            `json:"portsPerWorktree"`
	PortRangeStart   int            `json:"portRangeStart"`
	PortRangeEnd     int            `json:"portRangeEnd"`
	OpenWith         string         `json:"openWith"`
	IDECommand       string         `json:"ideCommand"`
	Tunnel           TunnelDefaults `json:"tunnel,omitempty"`
}

// PortAlloc represents a single port allocation
type PortAlloc struct {
	Project  string `json:"project"`
	Worktree string `json:"worktree"`
	Index    int    `json:"index"`
}

// Project represents a registered project
type Project struct {
	Path                    string               `json:"path"`
	AddedAt                 time.Time            `json:"addedAt"`
	DefaultPortsPerWorktree int                  `json:"defaultPortsPerWorktree"`
	GitHubOwner             string               `json:"github_owner,omitempty"`
	GitHubRepo              string               `json:"github_repo,omitempty"`
	Worktrees               map[string]*Worktree `json:"worktrees"`
}

// SetupStatus represents the state of worktree setup
type SetupStatus string

const (
	SetupStatusNone     SetupStatus = ""
	SetupStatusCreating SetupStatus = "creating"
	SetupStatusRunning  SetupStatus = "running"
	SetupStatusDone     SetupStatus = "done"
	SetupStatusFailed   SetupStatus = "failed"
)

// ArchiveStatus represents the state of worktree archiving
type ArchiveStatus string

const (
	ArchiveStatusNone    ArchiveStatus = ""
	ArchiveStatusRunning ArchiveStatus = "running"
)

// TunnelMode represents the type of tunnel
type TunnelMode string

const (
	TunnelModeNone  TunnelMode = ""
	TunnelModeQuick TunnelMode = "quick" // Random trycloudflare.com URL
	TunnelModeNamed TunnelMode = "named" // Custom domain via Cloudflare API
)

// TunnelState represents the current state of a tunnel for a worktree
type TunnelState struct {
	Active    bool       `json:"active"`
	Mode      TunnelMode `json:"mode"`
	URL       string     `json:"url,omitempty"`
	Port      int        `json:"port"`
	PID       int        `json:"pid,omitempty"`
	StartedAt time.Time  `json:"startedAt,omitempty"`
}

// TunnelDefaults contains global tunnel defaults
type TunnelDefaults struct {
	Domain string `json:"domain,omitempty"` // Fallback domain e.g., "kudcrafts.com"
	// Note: Authentication is handled by cloudflared CLI via `cloudflared tunnel login`
	// The following fields are deprecated and kept for backwards compatibility
	CloudflareToken string `json:"cloudflareToken,omitempty"` // Deprecated: use cloudflared tunnel login
	AccountID       string `json:"accountId,omitempty"`       // Deprecated: use cloudflared tunnel login
	ZoneID          string `json:"zoneId,omitempty"`          // Deprecated: use cloudflared tunnel login
}

// ProjectTunnelConfig contains project-level tunnel settings
type ProjectTunnelConfig struct {
	Domain     string `json:"domain,omitempty"`     // Override global domain
	TunnelID   string `json:"tunnelId,omitempty"`   // Existing tunnel ID for named mode
	TunnelName string `json:"tunnelName,omitempty"` // Human-readable tunnel name
}

// PRInfo represents a GitHub pull request linked to a worktree
type PRInfo struct {
	Number     int       `json:"number"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	State      string    `json:"state"`  // "open", "closed", "merged", "draft"
	Author     string    `json:"author"`
	HeadBranch string    `json:"head_branch"` // The branch being merged (PR source branch)
	UpdatedAt  time.Time `json:"updated_at"`
}

// Worktree represents a git worktree with its allocated ports
type Worktree struct {
	Path          string        `json:"path"`
	Branch        string        `json:"branch"`
	IsRoot        bool          `json:"isRoot"`
	Ports         []int         `json:"ports"`
	CreatedAt     time.Time     `json:"createdAt"`
	Archived      bool          `json:"archived,omitempty"`
	ArchivedAt    time.Time     `json:"archivedAt,omitempty"`
	PRs           []PRInfo      `json:"prs,omitempty"`
	SetupStatus   SetupStatus   `json:"setupStatus,omitempty"`
	ArchiveStatus ArchiveStatus `json:"archiveStatus,omitempty"`
	Tunnel        *TunnelState  `json:"tunnel,omitempty"`
}

// ProjectConfig represents project-level conductor.json
type ProjectConfig struct {
	Scripts map[string]string    `json:"scripts"`
	Ports   PortConfig           `json:"ports"`
	Tunnel  *ProjectTunnelConfig `json:"tunnel,omitempty"`
}

// PortConfig defines port settings for a project
type PortConfig struct {
	Default int      `json:"default"`
	Labels  []string `json:"labels"`
}

// NewConfig creates a new config with defaults
func NewConfig() *Config {
	return &Config{
		Version: 1,
		Defaults: Defaults{
			PortsPerWorktree: 1,
			PortRangeStart:   3100,
			PortRangeEnd:     3999,
			OpenWith:         "iterm",
			IDECommand:       "cursor",
		},
		Updates:         DefaultUpdateSettings(),
		PortAllocations: make(map[string]*PortAlloc),
		Projects:        make(map[string]*Project),
	}
}

// NewProject creates a new project with defaults
func NewProject(path string, defaultPorts int) *Project {
	return &Project{
		Path:                    path,
		AddedAt:                 time.Now(),
		DefaultPortsPerWorktree: defaultPorts,
		Worktrees:               make(map[string]*Worktree),
	}
}

// NewWorktree creates a new worktree entry
func NewWorktree(path, branch string, isRoot bool, ports []int) *Worktree {
	return &Worktree{
		Path:      path,
		Branch:    branch,
		IsRoot:    isRoot,
		Ports:     ports,
		CreatedAt: time.Now(),
	}
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
