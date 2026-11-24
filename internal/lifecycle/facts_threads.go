package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // driver "sqlite", read-only use
)

// v2ThreadPayload is the subset of T3's projected thread (payload_json of
// orchestration_v2_projection_threads) the lifecycle reads.
type v2ThreadPayload struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	WorktreePath    *string `json:"worktreePath"`
	CreationSource  string  `json:"creationSource"`
	SettledOverride *string `json:"settledOverride"`
	SettledAt       *string `json:"settledAt"`
	UnsettledAt     *string `json:"unsettledAt"`
	PinnedAt        *string `json:"pinnedAt"`
	ArchivedAt      *string `json:"archivedAt"`
	DeletedAt       *string `json:"deletedAt"`
	UpdatedAt       *string `json:"updatedAt"`
	LastVisitedAt   *string `json:"lastVisitedAt"`
	Lineage         *struct {
		Kind                 string `json:"kind"`
		RelationshipToParent string `json:"relationshipToParent"`
	} `json:"lineage"`
}

// loadThreads reads every T3 thread with a worktreePath from T3's state DB,
// keyed by realpath. Read-only: the DB is opened mode=ro and only selected.
func loadThreads(ctx context.Context, dbPath string) (map[string][]ThreadFact, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("T3 state DB: %w", err)
	}
	dsn := "file:" + (&url.URL{Path: dbPath}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open T3 state DB: %w", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(ctx, fxTimeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, "select payload_json from orchestration_v2_projection_threads")
	if err != nil {
		return nil, fmt.Errorf("read T3 threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]ThreadFact{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("read T3 threads: %w", err)
		}
		var p v2ThreadPayload
		if json.Unmarshal([]byte(raw), &p) != nil || p.WorktreePath == nil {
			continue
		}
		wp := strings.TrimRight(*p.WorktreePath, "/")
		if wp == "" {
			continue
		}
		out[realpath(wp)] = append(out[realpath(wp)], p.toFact())
	}
	return out, rows.Err()
}

func (p v2ThreadPayload) toFact() ThreadFact {
	t := ThreadFact{
		ID:            p.ID,
		Title:         p.Title,
		Pinned:        p.PinnedAt != nil && *p.PinnedAt != "",
		ArchivedAt:    parseTS(p.ArchivedAt),
		DeletedAt:     parseTS(p.DeletedAt),
		SettledAt:     parseTS(p.SettledAt),
		UnsettledAt:   parseTS(p.UnsettledAt),
		UpdatedAt:     parseTS(p.UpdatedAt),
		LastVisitedAt: parseTS(p.LastVisitedAt),
	}
	t.Subagent = p.CreationSource == "provider" ||
		(p.Lineage != nil && (p.Lineage.Kind == "subagent" || p.Lineage.RelationshipToParent == "subagent"))
	override := ""
	if p.SettledOverride != nil {
		override = *p.SettledOverride
	}
	switch override {
	case "settled":
		t.Settled = true
	case "active", "unsettled":
		t.Settled = false
	default:
		t.Settled = !t.SettledAt.IsZero() && !t.SettledAt.Before(t.UnsettledAt)
	}
	return t
}

func parseTS(s *string) time.Time {
	if s == nil || *s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, *s); err == nil {
		return t
	}
	return time.Time{}
}
