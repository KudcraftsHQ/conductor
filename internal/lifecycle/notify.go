package lifecycle

import "context"

// PHASE 2 — agent A owns this file.

// SweepSummary is what one executed sweep did; Notify turns it into at most
// one ntfy message.
type SweepSummary struct {
	Pass       *Pass      `json:"-"`
	Slimmed    []string   `json:"slimmed,omitempty"` // paths
	Retired    []*Record  `json:"retired,omitempty"`
	NeedsYou   []NeedsYou `json:"needsYou,omitempty"` // newly snapshotted, each reported once
	Failed     []string   `json:"failed,omitempty"`   // "path: error"
	FreedBytes int64      `json:"freedBytes"`
}

// NeedsYou is one worktree Hammas must look at before RetireAt.
type NeedsYou struct {
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	Work     string `json:"work"` // GitSummary
	RetireAt string `json:"retireAt"`
}

// Notify sends one summary to ntfy topic hammas-dev, reading NTFY_URL and
// NTFY_TOKEN from ~/.config/ntfy-hammas-dev/env at runtime (never committed,
// never printed). Priority: low for slimmed/retired + freed GB, default when
// there are needs-you items, high for failures. Quiet pass → no message.
func Notify(ctx context.Context, env *Env, sum *SweepSummary) error {
	return ErrNotImplemented
}
