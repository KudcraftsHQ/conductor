package lifecycle

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SweepSummary is what one executed sweep did; Notify turns it into at most
// one ntfy message.
type SweepSummary struct {
	Pass     *Pass      `json:"-"`
	Slimmed  []string   `json:"slimmed,omitempty"` // paths
	Retired  []*Record  `json:"retired,omitempty"`
	NeedsYou []NeedsYou `json:"needsYou,omitempty"` // newly snapshotted, each reported once
	Failed   []string   `json:"failed,omitempty"`   // "path: error", new this pass (notified)
	// FailedAgain: the same failure as the previous pass; shown, not re-notified.
	FailedAgain []string `json:"failedAgain,omitempty"`
	// Skipped: "path: why" — re-checks that said no; never notified.
	Skipped []string `json:"skipped,omitempty"`
	// Lines: one human line per action taken (for the CLI).
	Lines      []string `json:"lines,omitempty"`
	FreedBytes int64    `json:"freedBytes"`
	// DiskFreed is the change in free space on the home filesystem across the
	// pass: what was actually given back once bun-cache hardlinks are counted.
	DiskFreed int64  `json:"diskFreed"`
	Notified  bool   `json:"notified,omitempty"`
	NotifyErr string `json:"notifyErr,omitempty"`
}

// NeedsYou is one worktree Hammas must look at before RetireAt.
type NeedsYou struct {
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	Work     string `json:"work"` // GitSummary
	RetireAt string `json:"retireAt"`
	Ref      string `json:"ref,omitempty"`
}

// Quiet reports a pass with nothing worth telling Hammas.
func (s *SweepSummary) Quiet() bool {
	return len(s.Slimmed) == 0 && len(s.Retired) == 0 && len(s.NeedsYou) == 0 && len(s.Failed) == 0
}

// ntfyEnvFile holds NTFY_URL (topic URL) and NTFY_TOKEN; read at send time.
const ntfyEnvFile = ".config/ntfy-hammas-dev/env"

// ntfyTopic is the topic appended when NTFY_URL names only the server.
const ntfyTopic = "hammas-dev"

// Notify sends one summary to ntfy topic hammas-dev, reading NTFY_URL and
// NTFY_TOKEN from ~/.config/ntfy-hammas-dev/env at runtime (never committed,
// never printed). Priority: low for slimmed/retired + freed GB, default when
// there are needs-you items, high for failures. Quiet pass → no message.
func Notify(ctx context.Context, env *Env, sum *SweepSummary) error {
	if sum == nil || sum.Quiet() {
		return nil
	}
	vars, err := readEnvFile(filepath.Join(env.Home, ntfyEnvFile))
	if err != nil {
		return fmt.Errorf("ntfy env: %w", err)
	}
	url, token := vars["NTFY_URL"], vars["NTFY_TOKEN"]
	if url == "" || token == "" {
		return errors.New("ntfy env: NTFY_URL or NTFY_TOKEN missing")
	}
	topic := firstNonEmpty(vars["NTFY_TOPIC"], ntfyTopic)
	if !strings.HasSuffix(strings.TrimSuffix(url, "/"), "/"+topic) {
		url = strings.TrimSuffix(url, "/") + "/" + topic
	}

	title, body, priority, tags := composeNotification(env, sum)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("ntfy request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tags)
	req.Header.Set("User-Agent", "conductor-lifecycle/1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The URL may embed the server name; keep it out of logs.
		return errors.New("ntfy: request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy: HTTP %d", resp.StatusCode)
	}
	return nil
}

func composeNotification(env *Env, sum *SweepSummary) (title, body, priority, tags string) {
	freed := sum.DiskFreed
	if freed <= 0 {
		freed = sum.FreedBytes
	}
	var head []string
	if len(sum.Slimmed) > 0 || len(sum.Retired) > 0 {
		head = append(head, fmt.Sprintf("slimmed %d, retired %d, freed %s", len(sum.Slimmed), len(sum.Retired), gbString(freed)))
	}
	if n := len(sum.NeedsYou); n > 0 {
		head = append(head, fmt.Sprintf("%d need you", n))
	}
	if n := len(sum.Failed); n > 0 {
		head = append(head, fmt.Sprintf("%d failed", n))
	}
	title = "worktrees: " + strings.Join(head, " · ")

	var b strings.Builder
	section := func(name string, items []string) {
		if len(items) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s (%d):\n", name, len(items))
		for i, it := range items {
			if i == 12 {
				fmt.Fprintf(&b, "…and %d more\n", len(items)-12)
				break
			}
			b.WriteString("• " + it + "\n")
		}
	}
	var fails, needs, retired, slimmed []string
	for _, f := range sum.Failed {
		fails = append(fails, tildeIn(env, f))
	}
	for _, n := range sum.NeedsYou {
		needs = append(needs, fmt.Sprintf("%s (%s): %s — retire on %s", tildeIn(env, n.Path), firstNonEmpty(n.Branch, "detached"), n.Work, n.RetireAt))
	}
	for _, r := range sum.Retired {
		why := r.Reason
		if r.PR != nil {
			why = fmt.Sprintf("PR #%d %s", r.PR.Number, strings.ToLower(r.PR.State))
		}
		retired = append(retired, fmt.Sprintf("%s (%s)", tildeIn(env, r.Path), why))
	}
	for _, p := range sum.Slimmed {
		slimmed = append(slimmed, tildeIn(env, p))
	}
	section("Failed", fails)
	section("Needs you — unsaved work snapshotted to refs/graveyard, nothing pushed", needs)
	section("Retired (revive: conductor wt revive <path>)", retired)
	section("Slimmed (deps reinstall on wake)", slimmed)
	body = b.String()
	if len(body) > 3900 {
		body = body[:3900] + "…"
	}

	switch {
	case len(sum.Failed) > 0:
		priority, tags = "high", "warning"
	case len(sum.NeedsYou) > 0:
		priority, tags = "default", "eyes"
	default:
		priority, tags = "low", "broom"
	}
	return title, body, priority, tags
}

func gbString(n int64) string {
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

func tildeIn(env *Env, s string) string {
	if env.Home != "" {
		return strings.ReplaceAll(s, env.Home+"/", "~/")
	}
	return s
}

var envLine = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// readEnvFile parses KEY=VALUE lines (optional export, optional quotes).
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := envLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[2])
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[m[1]] = v
	}
	return out, sc.Err()
}
