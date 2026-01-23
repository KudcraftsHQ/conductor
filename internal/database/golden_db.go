package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// SyncVersionGoldenDB is the version for database-based golden copy
	SyncVersionGoldenDB = 3

	// GoldenDBSuffix is appended to project name for golden database
	GoldenDBSuffix = "_golden"

	// ConductorSyncTable is the metadata table name in golden DB
	ConductorSyncTable = "_conductor_sync"
)

// GoldenDBName returns the golden database name for a project
func GoldenDBName(projectName string) string {
	return sanitizeDBName(projectName + GoldenDBSuffix)
}

// GoldenDBExists checks if the golden database exists for a project
func GoldenDBExists(localURL string, projectName string) (bool, error) {
	dbName := GoldenDBName(projectName)
	return DatabaseExists(localURL, dbName)
}

// CreateGoldenDB creates the golden database for a project
func CreateGoldenDB(localURL string, projectName string) error {
	dbName := GoldenDBName(projectName)

	// Check if already exists
	exists, err := DatabaseExists(localURL, dbName)
	if err != nil {
		return fmt.Errorf("failed to check golden DB: %w", err)
	}
	if exists {
		return nil // Already exists, nothing to do
	}

	return CreateDatabase(localURL, dbName)
}

// DropGoldenDB drops the golden database for a project
func DropGoldenDB(localURL string, projectName string) error {
	dbName := GoldenDBName(projectName)
	return DropDatabase(localURL, dbName)
}

// GoldenDBURL returns the connection URL for the golden database
func GoldenDBURL(localURL string, projectName string) string {
	dbName := GoldenDBName(projectName)
	return BuildWorktreeURL(localURL, dbName)
}

// SyncToGoldenDB syncs from remote source to local golden database using pipe
// This is much faster than writing to a file first
func SyncToGoldenDB(ctx context.Context, sourceURL string, localURL string, projectName string, cfg *DatabaseConfig, progress ProgressFunc) (*GoldenDBSyncResult, error) {
	startTime := time.Now()

	// Create golden DB if not exists
	if progress != nil {
		progress("Creating golden database...")
	}
	if err := CreateGoldenDB(localURL, projectName); err != nil {
		return nil, fmt.Errorf("failed to create golden DB: %w", err)
	}

	goldenURL := GoldenDBURL(localURL, projectName)

	// Get table info for exclusions
	if progress != nil {
		progress("Analyzing source tables...")
	}
	tables, err := GetTableInfo(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get table info: %w", err)
	}

	excludedTables := determineExclusions(tables, cfg)

	// Get filtered tables from config
	filteredTables := cfg.FilterTables
	if filteredTables == nil {
		filteredTables = make(map[string]string)
	}

	// Get row counts before sync
	rowCounts, err := GetAccurateRowCounts(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get row counts: %w", err)
	}

	// Build pg_dump args
	dumpArgs := []string{
		sourceURL,
		"--no-owner",
		"--no-acl",
		"--clean",
		"--if-exists",
	}

	// Add exclusions (fully excluded tables)
	for _, table := range excludedTables {
		dumpArgs = append(dumpArgs, "--exclude-table-data="+table)
	}

	// Also exclude filtered tables from main dump (we'll copy them separately with WHERE)
	for table := range filteredTables {
		dumpArgs = append(dumpArgs, "--exclude-table-data="+table)
	}

	// Build psql args
	psqlArgs := []string{
		goldenURL,
		"--quiet",
	}

	statusMsg := "Syncing to golden DB"
	if len(excludedTables) > 0 || len(filteredTables) > 0 {
		statusMsg = fmt.Sprintf("Syncing to golden DB (excluding %d, filtering %d tables)...",
			len(excludedTables), len(filteredTables))
	}
	if progress != nil {
		progress(statusMsg)
	}

	// Create pipe: pg_dump | psql
	dumpCmd := exec.CommandContext(ctx, "pg_dump", dumpArgs...)
	psqlCmd := exec.CommandContext(ctx, "psql", psqlArgs...)

	// Connect pg_dump stdout to psql stdin
	pipe, err := dumpCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe: %w", err)
	}
	psqlCmd.Stdin = pipe

	// Capture stderr for errors
	var dumpStderr, psqlStderr strings.Builder
	dumpCmd.Stderr = &dumpStderr
	psqlCmd.Stderr = &psqlStderr

	// Start both commands
	if err := dumpCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start pg_dump: %w", err)
	}
	if err := psqlCmd.Start(); err != nil {
		_ = dumpCmd.Process.Kill()
		return nil, fmt.Errorf("failed to start psql: %w", err)
	}

	// Wait for both to complete
	dumpErr := dumpCmd.Wait()
	psqlErr := psqlCmd.Wait()

	if dumpErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sync cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("pg_dump failed: %w\nstderr: %s", dumpErr, dumpStderr.String())
	}
	if psqlErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sync cancelled: %w", ctx.Err())
		}
		// psql may have warnings that are not fatal
		if !strings.Contains(psqlStderr.String(), "ERROR") {
			// Just warnings, continue
		} else {
			return nil, fmt.Errorf("psql failed: %w\nstderr: %s", psqlErr, psqlStderr.String())
		}
	}

	// Copy filtered tables with WHERE clause
	if len(filteredTables) > 0 {
		if progress != nil {
			progress(fmt.Sprintf("Copying %d filtered tables...", len(filteredTables)))
		}
		for table, whereClause := range filteredTables {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("sync cancelled: %w", ctx.Err())
			}
			if progress != nil {
				progress(fmt.Sprintf("  Filtering %s...", table))
			}
			if err := copyFilteredTable(ctx, sourceURL, goldenURL, table, whereClause); err != nil {
				// Non-fatal, log and continue
				if progress != nil {
					progress(fmt.Sprintf("  Warning: failed to copy %s: %v", table, err))
				}
			}
		}
	}

	syncDuration := time.Since(startTime)

	// Create/update metadata table in golden DB
	if progress != nil {
		progress("Updating sync metadata...")
	}
	if err := updateGoldenDBMetadata(goldenURL, sourceURL, excludedTables, rowCounts, syncDuration); err != nil {
		// Non-fatal, just log
		if progress != nil {
			progress(fmt.Sprintf("Warning: failed to update metadata: %v", err))
		}
	}

	// Build table sizes map
	tableSizes := make(map[string]int64)
	for _, t := range tables {
		fullName := t.Schema + "." + t.Name
		tableSizes[fullName] = t.SizeBytes
	}

	result := &GoldenDBSyncResult{
		GoldenDBName:   GoldenDBName(projectName),
		GoldenDBURL:    goldenURL,
		SyncDurationMs: syncDuration.Milliseconds(),
		TableCount:     len(tables),
		ExcludedTables: excludedTables,
		RowCounts:      rowCounts,
		TableSizes:     tableSizes,
	}

	if progress != nil {
		progress(fmt.Sprintf("Sync completed in %dms", result.SyncDurationMs))
	}

	return result, nil
}

// GoldenDBSyncResult contains the result of a golden DB sync
type GoldenDBSyncResult struct {
	GoldenDBName   string           `json:"goldenDbName"`
	GoldenDBURL    string           `json:"goldenDbUrl"`
	SyncDurationMs int64            `json:"syncDurationMs"`
	TableCount     int              `json:"tableCount"`
	ExcludedTables []string         `json:"excludedTables"`
	RowCounts      map[string]int64 `json:"rowCounts"`
	TableSizes     map[string]int64 `json:"tableSizes"`
}

// copyFilteredTable copies data from source to golden DB with a WHERE filter
// Uses COPY for efficient data transfer: source COPY TO | golden COPY FROM
func copyFilteredTable(ctx context.Context, sourceURL, goldenURL, tableName, whereClause string) error {
	// Build the COPY query with WHERE clause
	// Format: COPY (SELECT * FROM table WHERE condition) TO STDOUT
	copyOutQuery := fmt.Sprintf(`COPY (SELECT * FROM %s WHERE %s) TO STDOUT`, tableName, whereClause)

	// Source: psql -c "COPY ... TO STDOUT"
	sourceCmd := exec.CommandContext(ctx, "psql", sourceURL, "-c", copyOutQuery)

	// Golden: psql -c "COPY table FROM STDIN"
	copyInQuery := fmt.Sprintf(`COPY %s FROM STDIN`, tableName)
	goldenCmd := exec.CommandContext(ctx, "psql", goldenURL, "-c", copyInQuery)

	// Connect source stdout to golden stdin
	pipe, err := sourceCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	goldenCmd.Stdin = pipe

	// Capture stderr
	var sourceStderr, goldenStderr strings.Builder
	sourceCmd.Stderr = &sourceStderr
	goldenCmd.Stderr = &goldenStderr

	// Start both commands
	if err := sourceCmd.Start(); err != nil {
		return fmt.Errorf("failed to start source copy: %w", err)
	}
	if err := goldenCmd.Start(); err != nil {
		_ = sourceCmd.Process.Kill()
		return fmt.Errorf("failed to start golden copy: %w", err)
	}

	// Wait for both
	sourceErr := sourceCmd.Wait()
	goldenErr := goldenCmd.Wait()

	if sourceErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("source copy failed: %w\nstderr: %s", sourceErr, sourceStderr.String())
	}
	if goldenErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("golden copy failed: %w\nstderr: %s", goldenErr, goldenStderr.String())
	}

	return nil
}

// updateGoldenDBMetadata creates/updates the metadata table in the golden database
func updateGoldenDBMetadata(goldenURL string, sourceURL string, excludedTables []string, rowCounts map[string]int64, duration time.Duration) error {
	db, err := sql.Open("postgres", goldenURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	// Create metadata table if not exists
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS _conductor_sync (
			id SERIAL PRIMARY KEY,
			synced_at TIMESTAMPTZ DEFAULT NOW(),
			source_url TEXT,
			excluded_tables TEXT[],
			row_counts JSONB,
			sync_duration_ms INT,
			is_incremental BOOLEAN DEFAULT FALSE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create metadata table: %w", err)
	}

	// Convert row counts to JSON
	rowCountsJSON := "{}"
	if len(rowCounts) > 0 {
		pairs := make([]string, 0, len(rowCounts))
		for table, count := range rowCounts {
			pairs = append(pairs, fmt.Sprintf(`"%s": %d`, table, count))
		}
		rowCountsJSON = "{" + strings.Join(pairs, ", ") + "}"
	}

	// Mask password in source URL for storage
	maskedURL := maskPassword(sourceURL)

	// Convert excluded tables to PostgreSQL array format
	excludedArray := "{}"
	if len(excludedTables) > 0 {
		quoted := make([]string, len(excludedTables))
		for i, t := range excludedTables {
			quoted[i] = fmt.Sprintf(`"%s"`, t)
		}
		excludedArray = "{" + strings.Join(quoted, ",") + "}"
	}

	// Insert sync record
	_, err = db.Exec(`
		INSERT INTO _conductor_sync (source_url, excluded_tables, row_counts, sync_duration_ms, is_incremental)
		VALUES ($1, $2::text[], $3::jsonb, $4, false)
	`, maskedURL, excludedArray, rowCountsJSON, duration.Milliseconds())

	return err
}

// maskPassword masks the password in a connection URL
func maskPassword(url string) string {
	// Simple masking: replace password between : and @ with ****
	// Format: postgres://user:password@host:port/db
	if idx := strings.Index(url, "://"); idx != -1 {
		rest := url[idx+3:]
		if atIdx := strings.Index(rest, "@"); atIdx != -1 {
			if colonIdx := strings.Index(rest[:atIdx], ":"); colonIdx != -1 {
				return url[:idx+3+colonIdx+1] + "****" + rest[atIdx:]
			}
		}
	}
	return url
}

// CloneFromGoldenDB clones the golden database to a worktree database using pipe
func CloneFromGoldenDB(ctx context.Context, localURL string, projectName string, worktreeDBName string, progress ProgressFunc) error {
	// Check golden DB exists
	exists, err := GoldenDBExists(localURL, projectName)
	if err != nil {
		return fmt.Errorf("failed to check golden DB: %w", err)
	}
	if !exists {
		return fmt.Errorf("golden database does not exist for project %s - run sync first", projectName)
	}

	// Create worktree DB
	if progress != nil {
		progress("Creating worktree database...")
	}
	if err := CreateDatabase(localURL, worktreeDBName); err != nil {
		return fmt.Errorf("failed to create worktree DB: %w", err)
	}

	goldenURL := GoldenDBURL(localURL, projectName)
	worktreeURL := BuildWorktreeURL(localURL, worktreeDBName)

	// Build pg_dump args (from golden DB)
	dumpArgs := []string{
		goldenURL,
		"--no-owner",
		"--no-acl",
		// Don't include _conductor_sync table in clone
		"--exclude-table=" + ConductorSyncTable,
	}

	// Build psql args
	psqlArgs := []string{
		worktreeURL,
		"--quiet",
	}

	if progress != nil {
		progress("Cloning from golden DB...")
	}

	// Create pipe: pg_dump | psql
	dumpCmd := exec.CommandContext(ctx, "pg_dump", dumpArgs...)
	psqlCmd := exec.CommandContext(ctx, "psql", psqlArgs...)

	// Connect pg_dump stdout to psql stdin
	pipe, err := dumpCmd.StdoutPipe()
	if err != nil {
		_ = DropDatabase(localURL, worktreeDBName)
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	psqlCmd.Stdin = pipe

	// Capture stderr
	var dumpStderr, psqlStderr strings.Builder
	dumpCmd.Stderr = &dumpStderr
	psqlCmd.Stderr = &psqlStderr

	// Start both commands
	if err := dumpCmd.Start(); err != nil {
		_ = DropDatabase(localURL, worktreeDBName)
		return fmt.Errorf("failed to start pg_dump: %w", err)
	}
	if err := psqlCmd.Start(); err != nil {
		_ = dumpCmd.Process.Kill()
		_ = DropDatabase(localURL, worktreeDBName)
		return fmt.Errorf("failed to start psql: %w", err)
	}

	// Wait for both to complete
	dumpErr := dumpCmd.Wait()
	psqlErr := psqlCmd.Wait()

	if dumpErr != nil {
		_ = DropDatabase(localURL, worktreeDBName)
		if ctx.Err() != nil {
			return fmt.Errorf("clone cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("pg_dump failed: %w\nstderr: %s", dumpErr, dumpStderr.String())
	}
	if psqlErr != nil {
		// psql may have warnings that are not fatal
		if strings.Contains(psqlStderr.String(), "ERROR") {
			_ = DropDatabase(localURL, worktreeDBName)
			return fmt.Errorf("psql failed: %w\nstderr: %s", psqlErr, psqlStderr.String())
		}
	}

	if progress != nil {
		progress("Clone completed")
	}

	return nil
}

// ReinitializeDatabaseV3 drops an existing worktree database and re-clones from the golden database
// Returns error if golden database doesn't exist
func ReinitializeDatabaseV3(ctx context.Context, localURL string, projectName string, worktreeDBName string, worktreePath string, progress ProgressFunc) (*ReinitV3Result, error) {
	// Check golden DB exists
	exists, err := GoldenDBExists(localURL, projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to check golden DB: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("golden database does not exist for project %s - run sync first", projectName)
	}

	// Drop existing database if it exists
	dbExists, err := DatabaseExists(localURL, worktreeDBName)
	if err != nil {
		return nil, fmt.Errorf("failed to check database existence: %w", err)
	}

	if dbExists {
		if progress != nil {
			progress("Dropping existing database...")
		}
		if err := DropDatabase(localURL, worktreeDBName); err != nil {
			return nil, fmt.Errorf("failed to drop existing database: %w", err)
		}
	}

	// Clone from golden database
	if err := CloneFromGoldenDB(ctx, localURL, projectName, worktreeDBName, progress); err != nil {
		return nil, err
	}

	result := &ReinitV3Result{
		DatabaseName: worktreeDBName,
	}

	// Check migration status if worktree uses Prisma
	if HasPrismaMigrations(worktreePath) {
		dbURL := BuildWorktreeURL(localURL, worktreeDBName)
		state, err := DetectMigrationState(dbURL, worktreePath)
		if err == nil {
			result.MigrationState = state
		}
	}

	return result, nil
}

// ReinitV3Result contains the result of a V3 database reinitialization
type ReinitV3Result struct {
	DatabaseName   string
	MigrationState *MigrationState
}

// GetGoldenDBSyncInfo retrieves the latest sync info from the golden database
func GetGoldenDBSyncInfo(localURL string, projectName string) (*GoldenDBSyncInfo, error) {
	exists, err := GoldenDBExists(localURL, projectName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	goldenURL := GoldenDBURL(localURL, projectName)
	db, err := sql.Open("postgres", goldenURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	// Check if metadata table exists
	var tableExists bool
	err = db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = '_conductor_sync'
		)
	`).Scan(&tableExists)
	if err != nil || !tableExists {
		return &GoldenDBSyncInfo{
			GoldenDBName: GoldenDBName(projectName),
			Exists:       true,
		}, nil
	}

	// Get latest sync record
	info := &GoldenDBSyncInfo{
		GoldenDBName: GoldenDBName(projectName),
		Exists:       true,
	}

	err = db.QueryRow(`
		SELECT synced_at, source_url, sync_duration_ms, is_incremental
		FROM _conductor_sync
		ORDER BY synced_at DESC
		LIMIT 1
	`).Scan(&info.LastSyncAt, &info.SourceURL, &info.SyncDurationMs, &info.IsIncremental)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	// Get table count
	err = db.QueryRow(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
	`).Scan(&info.TableCount)
	if err != nil {
		return nil, err
	}

	return info, nil
}

// GoldenDBSyncInfo contains information about the golden database
type GoldenDBSyncInfo struct {
	GoldenDBName   string    `json:"goldenDbName"`
	Exists         bool      `json:"exists"`
	LastSyncAt     time.Time `json:"lastSyncAt,omitempty"`
	SourceURL      string    `json:"sourceUrl,omitempty"`
	SyncDurationMs int64     `json:"syncDurationMs,omitempty"`
	IsIncremental  bool      `json:"isIncremental,omitempty"`
	TableCount     int       `json:"tableCount,omitempty"`
}

// DefaultSyncCooldown is the default time to wait between syncs (24 hours)
const DefaultSyncCooldown = 24 * time.Hour

// CheckGoldenDBSyncNeeded checks if a sync is needed for the V3 golden database
// Returns nil if no sync is needed (synced recently), or a reason if sync is needed
func CheckGoldenDBSyncNeeded(localURL string, projectName string, cooldown time.Duration) (*SyncCheckResult, error) {
	result := &SyncCheckResult{NeedsSync: true, Reason: "initial sync"}

	// Check if golden DB exists
	info, err := GetGoldenDBSyncInfo(localURL, projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to check golden DB: %w", err)
	}

	// No golden DB - need sync
	if info == nil || !info.Exists {
		return result, nil
	}

	// No sync record - need sync
	if info.LastSyncAt.IsZero() {
		result.Reason = "no previous sync record"
		return result, nil
	}

	// Check if synced recently
	timeSinceSync := time.Since(info.LastSyncAt)
	if timeSinceSync < cooldown {
		result.NeedsSync = false
		result.Reason = fmt.Sprintf("synced %s ago (cooldown: %s)", formatDuration(timeSinceSync), formatDuration(cooldown))
		return result, nil
	}

	// Synced more than cooldown ago - need sync
	result.Reason = fmt.Sprintf("last sync was %s ago", formatDuration(timeSinceSync))
	return result, nil
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd", days)
}

// LoadGoldenDBMetadata loads sync metadata from the golden database (V3)
func LoadGoldenDBMetadata(localURL string, projectName string) (*SyncMetadata, error) {
	exists, err := GoldenDBExists(localURL, projectName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	goldenURL := GoldenDBURL(localURL, projectName)
	db, err := sql.Open("postgres", goldenURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	var syncedAt time.Time
	var sourceURL string
	var excludedTablesJSON, rowCountsJSON []byte
	var syncDurationMs int64
	var isIncremental bool

	err = db.QueryRow(`
		SELECT synced_at, source_url, excluded_tables, row_counts, sync_duration_ms, is_incremental
		FROM _conductor_sync
		ORDER BY id DESC LIMIT 1
	`).Scan(&syncedAt, &sourceURL, &excludedTablesJSON, &rowCountsJSON, &syncDurationMs, &isIncremental)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	var excludedTables []string
	var rowCounts map[string]int64
	_ = json.Unmarshal(excludedTablesJSON, &excludedTables)
	_ = json.Unmarshal(rowCountsJSON, &rowCounts)

	return &SyncMetadata{
		LastSyncAt:     syncedAt,
		ExcludedTables: excludedTables,
		RowCounts:      rowCounts,
		SyncDurationMs: syncDurationMs,
		IsIncremental:  isIncremental,
		SyncVersion:    SyncVersionGoldenDB,
	}, nil
}

// GetGoldenDBSize returns the size of the golden database in bytes
func GetGoldenDBSize(localURL string, projectName string) (int64, error) {
	exists, err := GoldenDBExists(localURL, projectName)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}

	goldenURL := GoldenDBURL(localURL, projectName)
	db, err := sql.Open("postgres", goldenURL)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()

	dbName := GoldenDBName(projectName)
	var size int64
	err = db.QueryRow(`SELECT pg_database_size($1)`, dbName).Scan(&size)
	if err != nil {
		return 0, err
	}
	return size, nil
}
