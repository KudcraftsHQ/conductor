package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// Built-in defaults (spec, approved 2026-11-24).
var (
	DefaultSlimAfter           = 24 * time.Hour
	DefaultRetireAfter         = 48 * time.Hour
	DefaultNeedsYouAfter       = 14 * 24 * time.Hour
	DefaultAbandonAfter        = 30 * 24 * time.Hour
	DefaultSweepInterval       = time.Hour
	DefaultIgnoredArchiveCapMB = 50
	// DefaultSlimAllowlist: regenerable directories, matched by basename at any
	// depth (node_modules/.cache and node_modules/.remotion go with their
	// node_modules). Each is confirmed gitignored before deletion.
	DefaultSlimAllowlist = []string{
		"node_modules", ".vite", ".turbo", ".next", ".svelte-kit", ".parcel-cache", ".remotion",
	}
	// NeverSlim are basenames slim must never delete, whatever the allowlist says.
	NeverSlim = []string{".git", ".env", ".cutroom-dev", ".conductor-t3-thread"}
)

// Settings is the lifecycle config resolved for one project: built-in
// defaults, overlaid by defaults.lifecycle, overlaid by projects.<p>.lifecycle.
type Settings struct {
	Enabled             bool          `json:"enabled"`
	SlimAfter           time.Duration `json:"slimAfter"`
	RetireAfter         time.Duration `json:"retireAfter"`
	NeedsYouAfter       time.Duration `json:"needsYouAfter"`
	AbandonAfter        time.Duration `json:"abandonAfter"`
	SweepInterval       time.Duration `json:"sweepInterval"`
	SlimAllowlist       []string      `json:"slimAllowlist"`
	IgnoredArchiveCapMB int           `json:"ignoredArchiveCapMB"`
	// Keep: path globs never touched (already ~-expanded).
	Keep []string `json:"keep,omitempty"`
}

// BuiltinSettings returns the spec's defaults.
func BuiltinSettings() Settings {
	return Settings{
		Enabled:             true,
		SlimAfter:           DefaultSlimAfter,
		RetireAfter:         DefaultRetireAfter,
		NeedsYouAfter:       DefaultNeedsYouAfter,
		AbandonAfter:        DefaultAbandonAfter,
		SweepInterval:       DefaultSweepInterval,
		SlimAllowlist:       append([]string(nil), DefaultSlimAllowlist...),
		IgnoredArchiveCapMB: DefaultIgnoredArchiveCapMB,
	}
}

func (s Settings) overlay(c *config.LifecycleConfig) Settings {
	if c == nil {
		return s
	}
	if c.Enabled != nil {
		s.Enabled = *c.Enabled
	}
	set := func(dst *time.Duration, v config.Duration) {
		if v != 0 {
			*dst = v.D()
		}
	}
	set(&s.SlimAfter, c.SlimAfter)
	set(&s.RetireAfter, c.RetireAfter)
	set(&s.NeedsYouAfter, c.NeedsYouAfter)
	set(&s.AbandonAfter, c.AbandonAfter)
	set(&s.SweepInterval, c.SweepInterval)
	if len(c.SlimAllowlist) > 0 {
		s.SlimAllowlist = append([]string(nil), c.SlimAllowlist...)
	}
	if c.IgnoredArchiveCapMB > 0 {
		s.IgnoredArchiveCapMB = c.IgnoredArchiveCapMB
	}
	if len(c.Keep) > 0 {
		home, _ := os.UserHomeDir()
		s.Keep = nil
		for _, k := range c.Keep {
			if strings.HasPrefix(k, "~/") && home != "" {
				k = filepath.Join(home, k[2:])
			}
			s.Keep = append(s.Keep, k)
		}
	}
	return s
}

// Kept reports whether path matches one of s.Keep, and which pattern.
func (s Settings) Kept(path string) (string, bool) {
	for _, k := range s.Keep {
		if prefix, ok := strings.CutSuffix(k, "/**"); ok {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return k, true
			}
			continue
		}
		if ok, _ := filepath.Match(k, path); ok {
			return k, true
		}
	}
	return "", false
}

// ResolveSettings returns the settings for a project ("" = global only).
func ResolveSettings(cfg *config.Config, project string) Settings {
	s := BuiltinSettings()
	if cfg == nil {
		return s
	}
	s = s.overlay(cfg.Defaults.Lifecycle)
	if p, ok := cfg.Projects[project]; ok && p != nil {
		s = s.overlay(p.Lifecycle)
	}
	return s
}

// SettingsFor resolves settings for the project that owns repoRoot.
func (e *Env) SettingsFor(repoRoot string) Settings {
	name, _ := e.ProjectFor(repoRoot)
	return ResolveSettings(e.Config, name)
}

// SettingKeys are the keys `conductor wt config set` accepts.
var SettingKeys = []string{
	"enabled", "slimAfter", "retireAfter", "needsYouAfter", "abandonAfter",
	"sweepInterval", "slimAllowlist", "ignoredArchiveCapMB", "keep",
}

// SetSetting writes one key into c (allocating it if nil) and returns it.
// value "" (or "default") clears the key back to inheriting.
func SetSetting(c *config.LifecycleConfig, key, value string) (*config.LifecycleConfig, error) {
	if c == nil {
		c = &config.LifecycleConfig{}
	}
	clear := value == "" || value == "default"
	dur := func(dst *config.Duration) error {
		if clear {
			*dst = 0
			return nil
		}
		d, err := config.ParseDuration(value)
		if err != nil {
			return err
		}
		if d <= 0 {
			return fmt.Errorf("%s must be positive", key)
		}
		*dst = d
		return nil
	}
	switch strings.ToLower(key) {
	case "enabled":
		if clear {
			c.Enabled = nil
			break
		}
		b, err := strconv.ParseBool(value)
		if err != nil {
			return c, fmt.Errorf("enabled must be true or false")
		}
		c.Enabled = &b
	case "slimafter":
		return c, dur(&c.SlimAfter)
	case "retireafter":
		return c, dur(&c.RetireAfter)
	case "needsyouafter":
		return c, dur(&c.NeedsYouAfter)
	case "abandonafter":
		return c, dur(&c.AbandonAfter)
	case "sweepinterval":
		return c, dur(&c.SweepInterval)
	case "slimallowlist":
		if clear {
			c.SlimAllowlist = nil
			break
		}
		var list []string
		for _, s := range strings.Split(value, ",") {
			if s = strings.TrimSpace(s); s != "" {
				for _, never := range NeverSlim {
					if s == never || strings.HasPrefix(s, ".env") {
						return c, fmt.Errorf("%q can never be slimmed", s)
					}
				}
				list = append(list, s)
			}
		}
		c.SlimAllowlist = list
	case "keep":
		c.Keep = nil
		if !clear {
			for _, k := range strings.Split(value, ",") {
				if k = strings.TrimSpace(k); k != "" {
					if _, err := filepath.Match(strings.TrimSuffix(k, "/**"), "/"); err != nil {
						return c, fmt.Errorf("bad keep pattern %q: %w", k, err)
					}
					c.Keep = append(c.Keep, k)
				}
			}
		}
	case "ignoredarchivecapmb":
		if clear {
			c.IgnoredArchiveCapMB = 0
			break
		}
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return c, fmt.Errorf("ignoredArchiveCapMB must be a positive integer")
		}
		c.IgnoredArchiveCapMB = n
	default:
		return c, fmt.Errorf("unknown lifecycle key %q (keys: %s)", key, strings.Join(SettingKeys, ", "))
	}
	return c, nil
}

// PauseInfo is what the pause file holds.
type PauseInfo struct {
	PausedAt time.Time `json:"pausedAt"`
	Reason   string    `json:"reason,omitempty"`
}

// Paused reports whether the pause file exists, and what it says.
// Any pause file stops every action, even one that cannot be parsed.
func (e *Env) Paused() (bool, PauseInfo) {
	b, err := os.ReadFile(e.PauseFile)
	if err != nil {
		return !errors.Is(err, os.ErrNotExist), PauseInfo{}
	}
	var info PauseInfo
	_ = json.Unmarshal(b, &info)
	return true, info
}

// Pause writes the pause file.
func (e *Env) Pause(reason string) error {
	if err := os.MkdirAll(filepath.Dir(e.PauseFile), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(PauseInfo{PausedAt: e.Now().UTC(), Reason: reason}, "", "  ")
	return writeFileSync(e.PauseFile, append(b, '\n'), 0o644)
}

// Resume removes the pause file. Not an error when it was not paused.
func (e *Env) Resume() error {
	err := os.Remove(e.PauseFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
