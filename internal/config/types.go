package config

import "time"

// Config represents the global conductor configuration
type Config struct {
	Version         int                   `json:"version"`
	Defaults        Defaults              `json:"defaults"`
	Updates         UpdateSettings        `json:"updates"`
	PortAllocations map[string]*PortAlloc `json:"portAllocations"`
	Projects        map[string]*Project   `json:"projects"`
}

// Defaults contains default settings
type Defaults struct {
	PortsPerWorktree int            `json:"portsPerWorktree"`
	PortRangeStart   int            `json:"portRangeStart"`
	PortRangeEnd     int            `json:"portRangeEnd"`
	OpenWith         string         `json:"openWith"`
	IDECommand       string         `json:"ideCommand"`
	Tunnel           TunnelDefaults `json:"tunnel,omitempty"`
	// LocalPostgresURL is the connection string for local PostgreSQL
	// Format: postgresql://user:pass@localhost:5432
	LocalPostgresURL string `json:"localPostgresUrl,omitempty"`
	// ClickUp contains global ClickUp agent configuration
	ClickUp *ClickUpConfig `json:"clickup,omitempty"`
	// Tmux contains tmux session settings
	Tmux TmuxDefaults `json:"tmux,omitempty"`
	// Multiplexer selects the terminal multiplexer conductor drives:
	// "tmux", "herdr", "t3", or "auto" (default). Auto picks whichever of T3
	// Code or herdr conductor is already running inside, then herdr when tmux
	// is unavailable, and tmux otherwise.
	Multiplexer string `json:"multiplexer,omitempty"`
}

// TmuxDefaults contains tmux session settings
type TmuxDefaults struct {
	// DisableCC disables iTerm2 -CC control mode integration.
	// When true, conductor uses plain tmux even inside iTerm2,
	// giving full control over pane layouts.
	DisableCC bool `json:"disableCc,omitempty"`
}

// ClickUpConfig contains ClickUp agent configuration (global defaults)
type ClickUpConfig struct {
	APIToken      string `json:"apiToken"`                // pk_xxxxx
	TeamID        string `json:"teamId"`                  // workspace/team ID
	TriggerStatus string `json:"triggerStatus,omitempty"` // default: "in progress"
	WebhookPort   int    `json:"webhookPort,omitempty"`   // default: 9876
	WebhookSecret string `json:"webhookSecret,omitempty"` // set after registration
	WebhookID     string `json:"webhookId,omitempty"`     // set after registration
	PollInterval  int    `json:"pollInterval,omitempty"`  // seconds, default: 30
	PollFallback  bool   `json:"pollFallback,omitempty"`  // true = enable polling fallback
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
	// Database contains database sync configuration (optional)
	Database *DatabaseConfig `json:"database,omitempty"`
	// Tooling contains detected project type and tool availability (per-machine)
	Tooling *ProjectTooling `json:"tooling,omitempty"`
}

// ProjectTooling contains detected project type info and tool installation status
type ProjectTooling struct {
	DetectedAt     time.Time `json:"detectedAt"`
	Framework      string    `json:"framework"`
	Language       string    `json:"language"`
	PackageManager string    `json:"packageManager"`
	TestFramework  string    `json:"testFramework,omitempty"`
	WebEligible    bool      `json:"webEligible"`
	UIType         string    `json:"uiType"`
	ProofShotReady bool      `json:"proofShotReady,omitempty"`
	TrustLayerInit bool      `json:"trustLayerInit,omitempty"`
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
	State      string    `json:"state"` // "open", "closed", "merged", "draft"
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
	// DatabaseName is the name of the worktree's database (e.g., "myapp-3100")
	DatabaseName string `json:"databaseName,omitempty"`
	// DatabaseURL is the full connection string for the worktree's database
	DatabaseURL string `json:"databaseUrl,omitempty"`
	// ClickUpTaskID is the ClickUp task ID that triggered this worktree
	ClickUpTaskID string `json:"clickupTaskId,omitempty"`
	// ClickUpTaskURL is the URL to the ClickUp task
	ClickUpTaskURL string `json:"clickupTaskUrl,omitempty"`
	// MissionID links this worktree to a mission (if created by mission system)
	MissionID string `json:"missionId,omitempty"`
}

// DatabaseMode represents the database sync mode
type DatabaseMode string

const (
	// DatabaseModeLocal uses local PostgreSQL for golden and worktree databases
	DatabaseModeLocal DatabaseMode = "local"
	// DatabaseModeRemote uses remote PostgreSQL server for worktree databases
	DatabaseModeRemote DatabaseMode = "remote"
)

// DatabaseConfig is the configuration for database sync
type DatabaseConfig struct {
	// Source is the connection string to the source database (production/staging)
	// Format: postgresql://user:pass@host:port/dbname
	Source string `json:"source"`
	// ExcludeTables is a list of tables to exclude from data sync (schema only)
	ExcludeTables []string `json:"excludeTables,omitempty"`
	// FilterTables maps table names to WHERE clauses for partial data sync
	// Example: {"public.webhook_events": "created_at > NOW() - INTERVAL '30 days'"}
	FilterTables map[string]string `json:"filterTables,omitempty"`
	// SizeThresholdMB auto-excludes tables larger than this size (0 = disabled)
	SizeThresholdMB int `json:"sizeThresholdMB,omitempty"`
	// SyncSchedule is a cron expression for automatic sync (empty = manual only)
	SyncSchedule string `json:"syncSchedule,omitempty"`
	// DBNamePattern is the pattern for worktree database names
	// Default: "{project}-{port}"
	// Available variables: {project}, {port}, {worktree}
	DBNamePattern string `json:"dbNamePattern,omitempty"`
	// SyncStatus tracks the last sync state (persisted)
	SyncStatus *DatabaseSyncStatus `json:"syncStatus,omitempty"`

	// Mode is "local" (default) or "remote"
	// local: golden DB on local postgres, worktree DBs local
	// remote: clone via SSH, worktree DBs on remote server
	Mode DatabaseMode `json:"mode,omitempty"`
	// SSHHost is the SSH connection string for remote operations (e.g., "root@152.53.19.193")
	// Used to execute pg_dump | psql on the server for fast cloning
	SSHHost string `json:"sshHost,omitempty"`
	// CloneURL is the connection string for reading source DB (executed on the server via SSH)
	// Should use internal/localhost address since it runs on the server
	CloneURL string `json:"cloneUrl,omitempty"`
	// DevURL is the base connection string for dev databases (executed on the server via SSH)
	// Should use internal/localhost address. Database name is appended: {DevURL}/dev_{worktree}
	DevURL string `json:"devUrl,omitempty"`
	// DevURLExternal is the external connection string for dev databases (used by worktrees)
	// This is what gets written to .env for the app to connect. Database name is appended.
	DevURLExternal string `json:"devUrlExternal,omitempty"`
}

// DatabaseSyncStatus tracks the status of database synchronization
type DatabaseSyncStatus struct {
	// LastSyncAt is when the golden copy was last updated from source
	LastSyncAt string `json:"lastSyncAt,omitempty"`
	// GoldenCopySize is the size of the golden copy in bytes
	GoldenCopySize int64 `json:"goldenCopySize,omitempty"`
	// TableCount is the number of tables in the source database
	TableCount int `json:"tableCount,omitempty"`
	// ExcludedCount is the number of tables excluded from data sync
	ExcludedCount int `json:"excludedCount,omitempty"`
	// LastError is the last sync error message (empty if successful)
	LastError string `json:"lastError,omitempty"`
	// Status is the current sync status: "synced", "syncing", "failed", "never"
	Status string `json:"status,omitempty"`
}

// ProjectConfig represents project-level conductor.json
type ProjectConfig struct {
	Scripts map[string]string     `json:"scripts"`
	Ports   PortConfig            `json:"ports"`
	Tunnel  *ProjectTunnelConfig  `json:"tunnel,omitempty"`
	ClickUp *ProjectClickUpConfig `json:"clickup,omitempty"`
	// Tooling contains detected project type info (committed to repo)
	Tooling *ProjectToolingConfig `json:"tooling,omitempty"`
	// Auth contains test authentication configuration
	Auth *AuthConfig `json:"auth,omitempty"`
}

// AuthConfig contains authentication settings for testing
type AuthConfig struct {
	// Type is the auth method: "none", "dev-bypass", "email-password", "oauth"
	Type string `json:"type"`
	// LoginURL is the path to the login page (e.g., "/login", "/auth/signin")
	LoginURL string `json:"loginUrl,omitempty"`
	// SeedCommand creates the test account if it doesn't exist (e.g., "bun run seed-user")
	SeedCommand string `json:"seedCommand,omitempty"`
	// CallbackURL is where to redirect after login (e.g., "/app", "/dashboard")
	CallbackURL string `json:"callbackUrl,omitempty"`
}

// ProjectToolingConfig contains project type info stored in conductor.json (repo-level)
type ProjectToolingConfig struct {
	Framework      string `json:"framework"`
	Language       string `json:"language"`
	PackageManager string `json:"packageManager"`
	WebEligible    bool   `json:"webEligible"`
	UIType         string `json:"uiType"`
}

// AgentMode determines how the agent processes tasks for a project
type AgentMode string

const (
	AgentModeParallel   AgentMode = "parallel"
	AgentModeSequential AgentMode = "sequential"
)

// ProjectClickUpConfig contains project-level ClickUp settings
type ProjectClickUpConfig struct {
	ListID        string    `json:"listId"`                  // ClickUp list ID for this project
	TriggerStatus string    `json:"triggerStatus,omitempty"` // Override global trigger status
	Mode          AgentMode `json:"mode,omitempty"`          // "parallel" (default) or "sequential"
	DoneStatus    string    `json:"doneStatus,omitempty"`    // Status to set when task completes (default: "done")
	ReadyStatus   string    `json:"readyStatus,omitempty"`   // Status to filter for AI pick (default: "to do")
	AutoPick      bool      `json:"autoPick,omitempty"`      // Auto-pick next task via AI when current completes
}

// GetMode returns the agent mode, defaulting to parallel
func (c *ProjectClickUpConfig) GetMode() AgentMode {
	if c.Mode == AgentModeSequential {
		return AgentModeSequential
	}
	return AgentModeParallel
}

// GetDoneStatus returns the done status, defaulting to "done"
func (c *ProjectClickUpConfig) GetDoneStatus() string {
	if c.DoneStatus != "" {
		return c.DoneStatus
	}
	return "done"
}

// GetReadyStatus returns the ready status, defaulting to "to do"
func (c *ProjectClickUpConfig) GetReadyStatus() string {
	if c.ReadyStatus != "" {
		return c.ReadyStatus
	}
	return "to do"
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
			Multiplexer:      "auto",
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
