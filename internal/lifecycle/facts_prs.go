package lifecycle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// prCacheTTL is how long a repo's PR list on disk is trusted.
const prCacheTTL = 15 * time.Minute

type ghPR struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	State       string `json:"state"`
	HeadRefName string `json:"headRefName"`
}

// prCache makes one `gh pr list` per repo per pass, however many workers ask.
type prCache struct {
	dir     string
	noCache bool
	mu      sync.Mutex
	repos   map[string]*prOnce
}

type prOnce struct {
	once     sync.Once
	byBranch map[string][]PR
	err      string
}

func (c *prCache) forBranch(ctx context.Context, remote, branch string) ([]PR, string) {
	c.mu.Lock()
	if c.repos == nil {
		c.repos = map[string]*prOnce{}
	}
	r, ok := c.repos[remote]
	if !ok {
		r = &prOnce{}
		c.repos[remote] = r
	}
	c.mu.Unlock()
	r.once.Do(func() {
		list, err := c.load(ctx, remote)
		if err != nil {
			r.err = err.Error()
			return
		}
		r.byBranch = map[string][]PR{}
		for _, p := range list {
			r.byBranch[p.HeadRefName] = append(r.byBranch[p.HeadRefName], PR{Number: p.Number, URL: p.URL, State: p.State})
		}
	})
	if r.err != "" {
		return nil, r.err
	}
	return r.byBranch[branch], ""
}

func (c *prCache) load(ctx context.Context, remote string) ([]ghPR, error) {
	file := filepath.Join(c.dir, "prs-"+strings.ReplaceAll(remote, "/", "-")+".json")
	if !c.noCache {
		if st, err := os.Stat(file); err == nil && time.Since(st.ModTime()) < prCacheTTL {
			if b, err := os.ReadFile(file); err == nil {
				var list []ghPR
				if json.Unmarshal(b, &list) == nil {
					return list, nil
				}
			}
		}
	}
	out, err := fxRun(ctx, "", "gh", "pr", "list", "-R", remote, "--state", "all", "--limit", "1000",
		"--json", "number,url,state,headRefName")
	if err != nil {
		return nil, err
	}
	var list []ghPR
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, err
	}
	// The cache is a convenience: a failed write costs a gh call next time.
	_ = writeFileSync(file, []byte(out), 0o644)
	return list, nil
}
