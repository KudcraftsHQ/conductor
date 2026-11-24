package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LifecycleConfig holds the worktree-lifecycle settings (docs/WORKTREE-LIFECYCLE.md).
//
// It appears twice: as defaults.lifecycle in conductor.json, and as
// projects.<name>.lifecycle for a per-project override. Every field is
// optional; an unset field falls through to the global value and then to the
// built-in default, so an empty struct means "built-in defaults". Resolution
// lives in internal/lifecycle (ResolveSettings), not here.
type LifecycleConfig struct {
	// Enabled turns automatic actions on. nil means the built-in default (on).
	Enabled *bool `json:"enabled,omitempty"`
	// SlimAfter: settled (or idle, without a thread) this long → slim.
	SlimAfter Duration `json:"slimAfter,omitempty"`
	// RetireAfter: clean, PR merged/closed, settled this long → retire.
	RetireAfter Duration `json:"retireAfter,omitempty"`
	// NeedsYouAfter: unsaved work, settled this long → snapshot + tell Hammas.
	NeedsYouAfter Duration `json:"needsYouAfter,omitempty"`
	// AbandonAfter: a needs-you worktree is retired (from its snapshot) after this long.
	AbandonAfter Duration `json:"abandonAfter,omitempty"`
	// SweepInterval is how often the sweeper runs.
	SweepInterval Duration `json:"sweepInterval,omitempty"`
	// SlimAllowlist names the regenerable directories slim may delete
	// (basename match at any depth, each confirmed gitignored). Replaces, not
	// extends, the default list when set.
	SlimAllowlist []string `json:"slimAllowlist,omitempty"`
	// IgnoredArchiveCapMB caps the ignored-but-not-regenerable files archived
	// on retire. 0 means the built-in default.
	IgnoredArchiveCapMB int `json:"ignoredArchiveCapMB,omitempty"`
	// Keep lists worktree paths the lifecycle never touches: globs, "~/"
	// allowed, a trailing "/**" matches everything below. Replaces the
	// inherited list when set. For tool-owned worktrees such as e2e runners'.
	Keep []string `json:"keep,omitempty"`
}

// Duration is a time.Duration that reads and writes as a string and also
// accepts a day suffix ("14d", "1d12h"), which time.ParseDuration does not.
type Duration time.Duration

// ParseDuration parses "24h", "90m", "14d", "1d12h".
func ParseDuration(s string) (Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	var days float64
	if i := strings.Index(s, "d"); i >= 0 {
		n, err := strconv.ParseFloat(s[:i], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		days = n
		s = s[i+1:]
	}
	var rest time.Duration
	if s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		rest = d
	}
	return Duration(time.Duration(days*float64(24*time.Hour)) + rest), nil
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// String renders whole days as "Nd", otherwise Go's form ("36h0m0s" → "36h").
func (d Duration) String() string {
	td := time.Duration(d)
	if td == 0 {
		return "0"
	}
	if td%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", td/(24*time.Hour))
	}
	s := td.String()
	s = strings.TrimSuffix(s, "0s")
	s = strings.TrimSuffix(s, "0m")
	return s
}

// MarshalJSON writes the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON accepts a duration string or a number of nanoseconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := ParseDuration(s)
		if err != nil {
			return err
		}
		*d = v
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("duration must be a string like \"24h\" or \"14d\"")
	}
	*d = Duration(n)
	return nil
}
