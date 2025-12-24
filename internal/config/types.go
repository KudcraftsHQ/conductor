package config

import "time"

// Config represents the global conductor configuration
type Config struct {
	Version         int                    `json:"version"`
	Defaults        Defaults               `json:"defaults"`
	PortAllocations map[string]*PortAlloc  `json:"portAllocations"`
	Projects        map[string]*Project    `json:"projects"`
}

// Defaults contains default settings
type Defaults struct {
	PortsPerWorktree int    `json:"portsPerWorktree"`
	PortRangeStart   int    `json:"portRangeStart"`
	PortRangeEnd     int    `json:"portRangeEnd"`
	OpenWith         string `json:"openWith"`
	IDECommand       string `json:"ideCommand"`
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
	SetupStatus   SetupStatus   `json:"-"` // Runtime only, not persisted
	ArchiveStatus ArchiveStatus `json:"-"` // Runtime only, not persisted
}

// ProjectConfig represents project-level conductor.json
type ProjectConfig struct {
	Scripts map[string]string `json:"scripts"`
	Ports   PortConfig        `json:"ports"`
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
