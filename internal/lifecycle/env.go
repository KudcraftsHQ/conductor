package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// Env is where everything lives on this machine. Build it once per command
// with NewEnv; every path is overridable so a dry run can point elsewhere.
type Env struct {
	Home         string
	ConductorDir string // ~/.conductor (honours CONDUCTOR_CONFIG_DIR)
	// Config is a read-only snapshot of conductor.json. Nothing in this
	// package writes conductor.json; mutations go through internal/store.
	Config *config.Config
	// T3Root is where T3 creates worktrees: ~/.t3/worktrees (<repo>/<name>).
	T3Root string
	// T3StateDB is T3's state database, read-only:
	// ~/.t3/userdata/statev2.sqlite, table orchestration_v2_projection_threads.
	T3StateDB string
	// StateDir holds the action log and caches: ~/.local/state/conductor.
	StateDir string
	// GraveyardDir holds one directory per retired worktree: ~/.conductor/graveyard.
	GraveyardDir string
	// InventoryPath is the inventory store: ~/.conductor/lifecycle.json.
	InventoryPath string
	// PauseFile stops every action while it exists: ~/.conductor/lifecycle.paused.
	PauseFile string
	Now       func() time.Time
}

// NewEnv resolves the standard locations and loads conductor.json read-only.
func NewEnv() (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home directory: %w", err)
	}
	cdir, err := config.ConductorDir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	state := filepath.Join(home, ".local", "state", "conductor")
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		state = filepath.Join(x, "conductor")
	}
	return &Env{
		Home:          home,
		ConductorDir:  cdir,
		Config:        cfg,
		T3Root:        filepath.Join(home, ".t3", "worktrees"),
		T3StateDB:     filepath.Join(home, ".t3", "userdata", "statev2.sqlite"),
		StateDir:      state,
		GraveyardDir:  filepath.Join(cdir, "graveyard"),
		InventoryPath: filepath.Join(cdir, "lifecycle.json"),
		PauseFile:     filepath.Join(cdir, "lifecycle.paused"),
		Now:           time.Now,
	}, nil
}

// LogPath is the JSONL action log.
func (e *Env) LogPath() string { return filepath.Join(e.StateDir, "lifecycle.log") }

// CacheDir holds short-lived caches (gh PR lists).
func (e *Env) CacheDir() string { return filepath.Join(e.StateDir, "cache") }

// ProjectFor returns the conductor project whose root is repoRoot (realpath
// compare), or "" and nil.
func (e *Env) ProjectFor(repoRoot string) (string, *config.Project) {
	if e.Config == nil || repoRoot == "" {
		return "", nil
	}
	want := realpath(repoRoot)
	for name, p := range e.Config.Projects {
		if p != nil && p.Path != "" && realpath(p.Path) == want {
			return name, p
		}
	}
	return "", nil
}

// realpath resolves symlinks and cleans; it returns the cleaned input when the
// path does not exist (a pruned worktree must still compare equal to itself).
func realpath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
