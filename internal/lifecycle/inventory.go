package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// Inventory is what the lifecycle remembers between sweeps, keyed by realpath.
// It lives in its own file (~/.conductor/lifecycle.json), not conductor.json:
// the inventory covers every worktree on the machine, most of which conductor
// never registered, and writing them into conductor.json would make them
// visible to code that assumes a registered worktree has ports and a database.
// An inventory entry allocates nothing and the T3 watcher never reads it.
type Inventory struct {
	Version   int               `json:"version"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Entries   map[string]*Entry `json:"entries"`
}

// Entry is one worktree's lifecycle memory.
type Entry struct {
	Path     string `json:"path"`
	RepoRoot string `json:"repoRoot,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Remote   string `json:"remote,omitempty"`

	FirstSeenAt time.Time `json:"firstSeenAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	// State is the last classified state; Reason its human line.
	State  State  `json:"state"`
	Reason string `json:"reason,omitempty"`

	// Slim: when, and which relative paths were removed.
	SlimmedAt    time.Time `json:"slimmedAt,omitempty"`
	SlimmedPaths []string  `json:"slimmedPaths,omitempty"`
	// HydratedAt: when deps were last reinstalled (clears the slim mark).
	HydratedAt time.Time `json:"hydratedAt,omitempty"`

	// Needs-you: the snapshot taken and when Hammas was told (once).
	SnapshotSHA        string    `json:"snapshotSha,omitempty"`
	SnapshotRef        string    `json:"snapshotRef,omitempty"`
	SnapshotHead       string    `json:"snapshotHead,omitempty"` // HEAD the snapshot was taken on
	SnapshotAt         time.Time `json:"snapshotAt,omitempty"`
	NeedsYouNotifiedAt time.Time `json:"needsYouNotifiedAt,omitempty"`
	RetireAt           time.Time `json:"retireAt,omitempty"`

	// Retire: the record id once retired (the record is the authority).
	RecordID  string    `json:"recordId,omitempty"`
	RetiredAt time.Time `json:"retiredAt,omitempty"`
	// RevivedAt: when a retired worktree was brought back.
	RevivedAt time.Time `json:"revivedAt,omitempty"`

	// LastAction / LastError: the most recent executor outcome.
	LastAction   Action    `json:"lastAction,omitempty"`
	LastActionAt time.Time `json:"lastActionAt,omitempty"`
	LastError    string    `json:"lastError,omitempty"`
}

// Slimmed reports a slim mark newer than the last hydrate.
func (e *Entry) Slimmed() bool {
	return e != nil && !e.SlimmedAt.IsZero() && e.SlimmedAt.After(e.HydratedAt)
}

// LoadInventory reads the inventory; a missing file is an empty inventory.
func (e *Env) LoadInventory() (*Inventory, error) {
	inv := &Inventory{Version: 1, Entries: map[string]*Entry{}}
	b, err := os.ReadFile(e.InventoryPath)
	if errors.Is(err, os.ErrNotExist) {
		return inv, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, inv); err != nil {
		return nil, fmt.Errorf("parse %s: %w", e.InventoryPath, err)
	}
	if inv.Entries == nil {
		inv.Entries = map[string]*Entry{}
	}
	return inv, nil
}

// SaveInventory writes the inventory atomically (temp file, fsync, rename).
func (e *Env) SaveInventory(inv *Inventory) error {
	inv.Version = 1
	inv.UpdatedAt = e.Now().UTC()
	b, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return err
	}
	return writeFileSync(e.InventoryPath, append(b, '\n'), 0o644)
}

// UpdateInventory loads, applies fn and saves under a cross-process lock, so
// a sweep and a manual command cannot lose each other's writes.
func (e *Env) UpdateInventory(fn func(inv *Inventory) error) error {
	release, err := config.AcquireLock("lifecycle-inventory", 30*time.Second)
	if err != nil {
		return fmt.Errorf("lock inventory: %w", err)
	}
	defer release()
	inv, err := e.LoadInventory()
	if err != nil {
		return err
	}
	if err := fn(inv); err != nil {
		return err
	}
	return e.SaveInventory(inv)
}

// Get returns the entry for a path (realpath'd), or nil.
func (inv *Inventory) Get(path string) *Entry {
	if inv == nil {
		return nil
	}
	if en, ok := inv.Entries[path]; ok {
		return en
	}
	return inv.Entries[realpath(path)]
}

// Ensure returns the entry for a path, creating it if needed.
func (inv *Inventory) Ensure(path string, now time.Time) *Entry {
	if en := inv.Get(path); en != nil {
		return en
	}
	en := &Entry{Path: path, FirstSeenAt: now}
	inv.Entries[path] = en
	return en
}

// Observe refreshes entries from a classified pass. Worktrees no longer on
// disk keep their entry (a retired one is still wanted for revive).
func (inv *Inventory) Observe(facts []*Facts, plans []Plan, now time.Time) {
	for i, f := range facts {
		en := inv.Ensure(f.Path, now)
		en.RepoRoot, en.Branch, en.Remote = f.RepoRoot, f.Branch, f.Remote
		en.LastSeenAt = now
		en.State, en.Reason = plans[i].State, plans[i].Reason
		if !plans[i].RetireAt.IsZero() {
			en.RetireAt = plans[i].RetireAt
		}
	}
}

// Sorted returns the entries ordered by path.
func (inv *Inventory) Sorted() []*Entry {
	out := make([]*Entry, 0, len(inv.Entries))
	for _, en := range inv.Entries {
		out = append(out, en)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// writeFileSync writes data to path via a temp file in the same directory,
// fsyncs it and the directory, then renames over path. Records must be on
// disk before a worktree is removed (safety rule 3), so every lifecycle write
// goes through here.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
