package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// MigrationCompatibility represents the relationship between DB and worktree migrations
type MigrationCompatibility string

const (
	// MigrationForward means worktree has more migrations than DB (can apply new ones)
	MigrationForward MigrationCompatibility = "forward"

	// MigrationDiverged means migrations have diverged (need re-init)
	MigrationDiverged MigrationCompatibility = "diverged"

	// MigrationBehind means DB has migrations worktree doesn't have
	MigrationBehind MigrationCompatibility = "behind"

	// MigrationSynced means DB and worktree have identical migrations
	MigrationSynced MigrationCompatibility = "synced"

	// MigrationUnknown means we couldn't determine the state
	MigrationUnknown MigrationCompatibility = "unknown"
)

// PrismaMigration represents a migration record from _prisma_migrations table
type PrismaMigration struct {
	ID                string    `json:"id"`
	MigrationName     string    `json:"migrationName"`
	Checksum          string    `json:"checksum"`
	AppliedAt         time.Time `json:"appliedAt"`
	AppliedStepsCount int       `json:"appliedStepsCount"`
	RolledBackAt      *time.Time `json:"rolledBackAt,omitempty"`
}

// MigrationState represents the comparison between DB and worktree migrations
type MigrationState struct {
	// Compatibility is the overall state (forward, diverged, behind, synced)
	Compatibility MigrationCompatibility `json:"compatibility"`

	// AppliedMigrations are migrations in the database
	AppliedMigrations []PrismaMigration `json:"appliedMigrations"`

	// WorktreeMigrations are migrations in the worktree's prisma/migrations/
	WorktreeMigrations []string `json:"worktreeMigrations"`

	// PendingMigrations are in worktree but not applied (forward case)
	PendingMigrations []string `json:"pendingMigrations,omitempty"`

	// ExtraMigrations are applied but not in worktree (behind case)
	ExtraMigrations []string `json:"extraMigrations,omitempty"`

	// DivergentMigrations are mismatched (different checksum or missing)
	DivergentMigrations []string `json:"divergentMigrations,omitempty"`

	// RecommendedAction suggests what the user should do
	RecommendedAction string `json:"recommendedAction"`
}

// Note: MigrationBaseline is defined in types.go

// GetAppliedMigrations queries the _prisma_migrations table from a database
func GetAppliedMigrations(dbURL string) ([]PrismaMigration, error) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Check if _prisma_migrations table exists
	var exists bool
	err = db.QueryRow(`
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = '_prisma_migrations'
		)
	`).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check for _prisma_migrations table: %w", err)
	}

	if !exists {
		// No migrations table - database hasn't been initialized with Prisma
		return []PrismaMigration{}, nil
	}

	// Query all migrations, ordered by applied_at
	rows, err := db.Query(`
		SELECT id, migration_name, checksum, started_at, applied_steps_count, rolled_back_at
		FROM _prisma_migrations
		WHERE rolled_back_at IS NULL
		ORDER BY started_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var migrations []PrismaMigration
	for rows.Next() {
		var m PrismaMigration
		var rolledBackAt sql.NullTime
		err := rows.Scan(&m.ID, &m.MigrationName, &m.Checksum, &m.AppliedAt, &m.AppliedStepsCount, &rolledBackAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan migration row: %w", err)
		}
		if rolledBackAt.Valid {
			m.RolledBackAt = &rolledBackAt.Time
		}
		migrations = append(migrations, m)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating migration rows: %w", err)
	}

	return migrations, nil
}

// GetWorktreeMigrations lists migration directories from prisma/migrations/
func GetWorktreeMigrations(worktreePath string) ([]string, error) {
	migrationsDir := filepath.Join(worktreePath, "prisma", "migrations")

	// Check if migrations directory exists
	info, err := os.Stat(migrationsDir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat migrations directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("prisma/migrations is not a directory")
	}

	// Read directory entries
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrations []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip migration_lock.toml and other non-migration entries
		if name == "migration_lock.toml" || strings.HasPrefix(name, ".") {
			continue
		}
		// Check if it contains a migration.sql file
		migrationSQL := filepath.Join(migrationsDir, name, "migration.sql")
		if _, err := os.Stat(migrationSQL); err == nil {
			migrations = append(migrations, name)
		}
	}

	// Sort migrations by name (they typically start with timestamp)
	sort.Strings(migrations)

	return migrations, nil
}

// ComputeMigrationChecksum calculates the checksum of a migration.sql file
// This matches Prisma's checksum algorithm (SHA256 of file contents)
func ComputeMigrationChecksum(worktreePath, migrationName string) (string, error) {
	migrationFile := filepath.Join(worktreePath, "prisma", "migrations", migrationName, "migration.sql")

	content, err := os.ReadFile(migrationFile)
	if err != nil {
		return "", fmt.Errorf("failed to read migration file: %w", err)
	}

	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:]), nil
}

// DetectMigrationState compares database migrations with worktree migrations
func DetectMigrationState(dbURL, worktreePath string) (*MigrationState, error) {
	// Get applied migrations from database
	applied, err := GetAppliedMigrations(dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Get worktree migrations from filesystem
	worktree, err := GetWorktreeMigrations(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree migrations: %w", err)
	}

	state := &MigrationState{
		AppliedMigrations:  applied,
		WorktreeMigrations: worktree,
	}

	// Build maps for comparison
	appliedMap := make(map[string]PrismaMigration)
	for _, m := range applied {
		appliedMap[m.MigrationName] = m
	}

	worktreeSet := make(map[string]bool)
	for _, name := range worktree {
		worktreeSet[name] = true
	}

	// Find pending migrations (in worktree but not applied)
	for _, name := range worktree {
		if _, exists := appliedMap[name]; !exists {
			state.PendingMigrations = append(state.PendingMigrations, name)
		}
	}

	// Find extra migrations (applied but not in worktree)
	for _, m := range applied {
		if !worktreeSet[m.MigrationName] {
			state.ExtraMigrations = append(state.ExtraMigrations, m.MigrationName)
		}
	}

	// Check for divergent migrations (same name but different checksum)
	for _, m := range applied {
		if worktreeSet[m.MigrationName] {
			worktreeChecksum, err := ComputeMigrationChecksum(worktreePath, m.MigrationName)
			if err != nil {
				// Can't compute checksum, mark as potentially divergent
				state.DivergentMigrations = append(state.DivergentMigrations, m.MigrationName)
				continue
			}
			if worktreeChecksum != m.Checksum {
				state.DivergentMigrations = append(state.DivergentMigrations, m.MigrationName)
			}
		}
	}

	// Determine compatibility
	state.Compatibility = determineCompatibility(state)
	state.RecommendedAction = getRecommendedAction(state)

	return state, nil
}

// determineCompatibility classifies the migration state
func determineCompatibility(state *MigrationState) MigrationCompatibility {
	hasPending := len(state.PendingMigrations) > 0
	hasExtra := len(state.ExtraMigrations) > 0
	hasDivergent := len(state.DivergentMigrations) > 0

	// Divergent takes precedence - checksums don't match
	if hasDivergent {
		return MigrationDiverged
	}

	// Extra migrations in DB but not in worktree
	if hasExtra && !hasPending {
		return MigrationBehind
	}

	// Both extra and pending - this is also diverged
	if hasExtra && hasPending {
		return MigrationDiverged
	}

	// Only pending migrations - forward compatible
	if hasPending {
		return MigrationForward
	}

	// No differences
	return MigrationSynced
}

// getRecommendedAction returns a user-friendly action recommendation
func getRecommendedAction(state *MigrationState) string {
	switch state.Compatibility {
	case MigrationSynced:
		return "Database is up to date with worktree migrations"

	case MigrationForward:
		count := len(state.PendingMigrations)
		if count == 1 {
			return "Run 'prisma migrate deploy' to apply 1 pending migration"
		}
		return fmt.Sprintf("Run 'prisma migrate deploy' to apply %d pending migrations", count)

	case MigrationBehind:
		count := len(state.ExtraMigrations)
		if count == 1 {
			return "Database has 1 migration not in worktree. Consider rebasing your branch or re-initializing the database"
		}
		return fmt.Sprintf("Database has %d migrations not in worktree. Consider rebasing your branch or re-initializing the database", count)

	case MigrationDiverged:
		if len(state.DivergentMigrations) > 0 {
			return "Migrations have diverged (checksum mismatch). Re-initialize database with 'conductor database reinit'"
		}
		return "Migrations have diverged. Re-initialize database with 'conductor database reinit'"

	default:
		return "Unable to determine migration state"
	}
}

// CreateMigrationBaseline creates a baseline from applied migrations
func CreateMigrationBaseline(migrations []PrismaMigration) *MigrationBaseline {
	if len(migrations) == 0 {
		return &MigrationBaseline{
			TotalMigrations: 0,
			MigrationNames:  []string{},
			CapturedAt:      time.Now(),
		}
	}

	names := make([]string, len(migrations))
	for i, m := range migrations {
		names[i] = m.MigrationName
	}

	last := migrations[len(migrations)-1]
	return &MigrationBaseline{
		LastMigrationName:     last.MigrationName,
		LastMigrationChecksum: last.Checksum,
		TotalMigrations:       len(migrations),
		MigrationNames:        names,
		CapturedAt:            time.Now(),
	}
}

// CompareMigrationBaselines compares two baselines to detect changes
func CompareMigrationBaselines(golden, current *MigrationBaseline) MigrationCompatibility {
	if golden == nil || current == nil {
		return MigrationUnknown
	}

	// Same number and same last migration - synced
	if golden.TotalMigrations == current.TotalMigrations &&
		golden.LastMigrationName == current.LastMigrationName &&
		golden.LastMigrationChecksum == current.LastMigrationChecksum {
		return MigrationSynced
	}

	// Current has more migrations - check if golden is a prefix
	if current.TotalMigrations > golden.TotalMigrations {
		// Verify golden migrations are a prefix of current
		for i, name := range golden.MigrationNames {
			if i >= len(current.MigrationNames) || current.MigrationNames[i] != name {
				return MigrationDiverged
			}
		}
		return MigrationForward
	}

	// Golden has more migrations - current is behind
	if golden.TotalMigrations > current.TotalMigrations {
		// Verify current migrations are a prefix of golden
		for i, name := range current.MigrationNames {
			if i >= len(golden.MigrationNames) || golden.MigrationNames[i] != name {
				return MigrationDiverged
			}
		}
		return MigrationBehind
	}

	// Same count but different - diverged
	return MigrationDiverged
}

// HasPrismaMigrations checks if a worktree uses Prisma migrations
func HasPrismaMigrations(worktreePath string) bool {
	schemaPath := filepath.Join(worktreePath, "prisma", "schema.prisma")
	_, err := os.Stat(schemaPath)
	return err == nil
}

// GetMigrationBaselineFromDB creates a baseline directly from a database
func GetMigrationBaselineFromDB(dbURL string) (*MigrationBaseline, error) {
	migrations, err := GetAppliedMigrations(dbURL)
	if err != nil {
		return nil, err
	}
	return CreateMigrationBaseline(migrations), nil
}
