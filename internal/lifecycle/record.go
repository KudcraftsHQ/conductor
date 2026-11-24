package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Record is everything needed to bring a retired worktree back, written to
// ~/.conductor/graveyard/<id>/record.json and fsynced BEFORE the worktree is
// removed (safety rule 3). Next to it: ignored.tar.gz (archived ignored files).
type Record struct {
	Version  int    `json:"version"`
	ID       string `json:"id"`
	Path     string `json:"path"` // revive recreates exactly this path
	RepoRoot string `json:"repoRoot"`
	Remote   string `json:"remote,omitempty"` // owner/name
	Branch   string `json:"branch,omitempty"` // "" when HEAD was detached
	HeadSHA  string `json:"headSha"`
	// SnapshotSHA is set when the worktree had unsaved work: a commit (parent
	// HEAD) holding tracked changes + untracked non-ignored files, made
	// without touching the worktree's branch or index.
	SnapshotSHA string `json:"snapshotSha,omitempty"`
	// GraveyardRef is refs/graveyard/<id> in RepoRoot, pointing at
	// SnapshotSHA if set, else HeadSHA. Never pushed.
	GraveyardRef string `json:"graveyardRef"`

	PR *PR `json:"pr,omitempty"` // the PR that justified retiring, if any

	T3Threads []string `json:"t3Threads,omitempty"`
	// Conductor is the conductor.json entry, if registered (its entry is
	// kept, marked archived, so revive can re-provision under the same name).
	Conductor *ConductorRef `json:"conductor,omitempty"`

	Lockfile string `json:"lockfile,omitempty"`

	RetiredAt  time.Time `json:"retiredAt"`
	Reason     string    `json:"reason"`
	FreedBytes int64     `json:"freedBytes,omitempty"`

	// IgnoredArchive is the archive's file name inside the record dir ("" if none).
	IgnoredArchive string `json:"ignoredArchive,omitempty"`
	// ArchivedPaths: gitignored, non-regenerable paths saved into the archive.
	ArchivedPaths []string `json:"archivedPaths,omitempty"`
	// SkippedPaths: gitignored paths NOT saved (regenerable, or over the cap),
	// with why, e.g. ".cutroom-dev (812 MB, over cap)".
	SkippedPaths []string `json:"skippedPaths,omitempty"`

	// RevivedAt is set by revive; the record is kept.
	RevivedAt time.Time `json:"revivedAt,omitempty"`
}

// IgnoredArchiveName is the archive file written beside record.json.
const IgnoredArchiveName = "ignored.tar.gz"

var idUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// NewRecordID makes a unique, ref-safe id: <yyyymmdd-hhmmss>-<repo>-<dir>-<4 hex>.
func NewRecordID(f *Facts, now time.Time) string {
	repo := filepath.Base(f.RepoRoot)
	dir := filepath.Base(f.Path)
	slug := idUnsafe.ReplaceAllString(repo+"-"+dir, "-")
	slug = strings.Trim(strings.ReplaceAll(slug, "..", "."), ".-")
	if len(slug) > 60 {
		slug = slug[:60]
	}
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%s-%s", now.UTC().Format("20060102-150405"), slug, hex.EncodeToString(b[:]))
}

// RecordDir is ~/.conductor/graveyard/<id>.
func (e *Env) RecordDir(id string) string { return filepath.Join(e.GraveyardDir, id) }

// RecordPath is ~/.conductor/graveyard/<id>/record.json.
func (e *Env) RecordPath(id string) string { return filepath.Join(e.RecordDir(id), "record.json") }

// WriteRecord writes (or rewrites) a record durably.
func (e *Env) WriteRecord(r *Record) error {
	if r.ID == "" || idUnsafe.MatchString(r.ID) {
		return fmt.Errorf("invalid record id %q", r.ID)
	}
	r.Version = 1
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileSync(e.RecordPath(r.ID), append(b, '\n'), 0o600)
}

// ReadRecord reads one record by id.
func (e *Env) ReadRecord(id string) (*Record, error) {
	b, err := os.ReadFile(e.RecordPath(id))
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parse record %s: %w", id, err)
	}
	return &r, nil
}

// ListRecords returns every record, newest first. Unreadable ones are skipped.
func (e *Env) ListRecords() ([]*Record, error) {
	ents, err := os.ReadDir(e.GraveyardDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Record
	for _, d := range ents {
		if !d.IsDir() {
			continue
		}
		if r, err := e.ReadRecord(d.Name()); err == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RetiredAt.After(out[j].RetiredAt) })
	return out, nil
}

// RecordsForPath returns the records of a path, newest first (a path can be
// retired, revived and retired again).
func (e *Env) RecordsForPath(path string) ([]*Record, error) {
	all, err := e.ListRecords()
	if err != nil {
		return nil, err
	}
	want := realpath(path)
	var out []*Record
	for _, r := range all {
		if r.Path == path || r.Path == want {
			out = append(out, r)
		}
	}
	return out, nil
}
