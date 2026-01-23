package database

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// SyncFromSource syncs the source database to a local golden copy
// It creates:
// - golden.sql: Full dump (excluding data for large/excluded tables)
// - schema.sql: Schema-only dump for excluded tables
// - metadata.json: Sync metadata
func SyncFromSource(cfg *DatabaseConfig, projectName string, dbsyncDir string) (*SyncMetadata, error) {
	return SyncFromSourceWithProgressCtx(context.Background(), cfg, projectName, dbsyncDir, nil)
}

// SyncFromSourceWithProgress syncs with progress callback (backwards compatible)
func SyncFromSourceWithProgress(cfg *DatabaseConfig, projectName string, dbsyncDir string, progress ProgressFunc) (*SyncMetadata, error) {
	return SyncFromSourceWithProgressCtx(context.Background(), cfg, projectName, dbsyncDir, progress)
}

// SyncFromSourceWithProgressCtx syncs with progress callback and context for cancellation
// It uses V1 (single pg_dump) for initial sync (faster) and V2 for incremental sync (per-table updates)
func SyncFromSourceWithProgressCtx(ctx context.Context, cfg *DatabaseConfig, projectName string, dbsyncDir string, progress ProgressFunc) (*SyncMetadata, error) {
	// Check if this is initial sync or incremental
	if !GoldenCopyExists(projectName, dbsyncDir) {
		// Initial sync: use V1 (single pg_dump) - much faster
		if progress != nil {
			progress("First sync - using fast single dump...")
		}
		return SyncFromSourceWithProgressCtxV1(ctx, cfg, projectName, dbsyncDir, progress)
	}

	// Incremental sync: use V2 (per-table) for granular updates
	return SyncV2FromSourceCtx(ctx, cfg, projectName, dbsyncDir, progress)
}

// SyncFromSourceWithProgressCtxV1 is the legacy sync that does full dump on schema changes
// Kept for backwards compatibility
func SyncFromSourceWithProgressCtxV1(ctx context.Context, cfg *DatabaseConfig, projectName string, dbsyncDir string, progress ProgressFunc) (*SyncMetadata, error) {
	// Check if we can do incremental sync
	if GoldenCopyExists(projectName, dbsyncDir) {
		if progress != nil {
			progress("Checking for schema changes...")
		}

		// Load existing metadata to get schema hash
		metadata, err := LoadSyncMetadata(projectName, dbsyncDir)
		if err == nil && metadata != nil && metadata.SchemaHash != "" {
			// Check if schema changed
			schemaChanged, err := DetectSchemaChanges(cfg.Source, metadata.SchemaHash)
			if err == nil && !schemaChanged {
				// Schema unchanged - do incremental sync
				if progress != nil {
					progress("Schema unchanged, performing incremental sync...")
				}
				return SyncIncrementalFromSourceCtx(ctx, cfg, projectName, dbsyncDir, progress)
			}
			// Schema changed - fall through to full sync
			if progress != nil {
				progress("Schema changed, performing full sync...")
			}
		}
	}

	// Full sync (first time or schema changed)
	return fullSyncFromSourceCtx(ctx, cfg, projectName, dbsyncDir, progress)
}

// fullSyncFromSourceCtx performs a full pg_dump sync
func fullSyncFromSourceCtx(ctx context.Context, cfg *DatabaseConfig, projectName string, dbsyncDir string, progress ProgressFunc) (*SyncMetadata, error) {
	startTime := time.Now()

	if progress != nil {
		progress("Creating sync directory...")
	}

	// Create project sync directory
	projectDir := filepath.Join(dbsyncDir, projectName)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sync directory: %w", err)
	}

	// Clear any existing incremental files when doing full sync
	if metadata, _ := LoadSyncMetadata(projectName, dbsyncDir); metadata != nil {
		for _, incFile := range metadata.IncrementalFiles {
			_ = os.Remove(filepath.Join(projectDir, incFile))
		}
	}

	if progress != nil {
		progress("Analyzing source database tables...")
	}

	// Get table info for smart exclusion
	tables, err := GetTableInfo(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get table info: %w", err)
	}

	// Determine which tables to exclude
	excludedTables := determineExclusions(tables, cfg)

	// Get source database info
	dbName, err := GetDatabaseName(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get database name: %w", err)
	}

	pgVersion, err := GetPostgresVersion(cfg.Source)
	if err != nil {
		// Non-fatal, continue anyway
		pgVersion = "unknown"
	}

	// Build table sizes map (from pg_stat - approximate is fine for sizes)
	tableSizes := make(map[string]int64)
	for _, t := range tables {
		fullName := t.Schema + "." + t.Name
		tableSizes[fullName] = t.SizeBytes
	}

	// Get accurate row counts for change detection (not pg_stat estimates)
	rowCounts, err := GetAccurateRowCounts(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get row counts: %w", err)
	}

	if progress != nil {
		progress(fmt.Sprintf("Dumping %d tables...", len(tables)))
	}

	// Check for cancellation before starting dump
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("sync cancelled: %w", err)
	}

	// Run pg_dump for main dump (excluding data for excluded tables)
	goldenPath := filepath.Join(projectDir, "golden.sql")
	if err := runPgDumpWithProgressCtx(ctx, cfg.Source, goldenPath, excludedTables, false, progress); err != nil {
		// Clean up partial file on error/cancellation
		_ = os.Remove(goldenPath)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sync cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("failed to create golden dump: %w", err)
	}

	// Check for cancellation before schema dump
	if err := ctx.Err(); err != nil {
		_ = os.Remove(goldenPath)
		return nil, fmt.Errorf("sync cancelled: %w", err)
	}

	// If there are excluded tables, create schema-only dump for them
	schemaPath := filepath.Join(projectDir, "schema.sql")
	if len(excludedTables) > 0 {
		if err := runPgDumpSchemaOnlyCtx(ctx, cfg.Source, schemaPath, excludedTables); err != nil {
			// Clean up partial files on error/cancellation
			_ = os.Remove(schemaPath)
			if ctx.Err() != nil {
				_ = os.Remove(goldenPath)
				return nil, fmt.Errorf("sync cancelled: %w", ctx.Err())
			}
			return nil, fmt.Errorf("failed to create schema dump: %w", err)
		}
	}

	// Get golden file size
	goldenInfo, err := os.Stat(goldenPath)
	goldenSize := int64(0)
	if err == nil {
		goldenSize = goldenInfo.Size()
	}

	// Compute schema hash for change detection
	schemaHash, err := computeSchemaHash(cfg.Source)
	if err != nil {
		schemaHash = ""
	}

	// Capture migration baseline (for Prisma migration tracking)
	var migrationBaseline *MigrationBaseline
	baseline, err := GetMigrationBaselineFromDB(cfg.Source)
	if err == nil && baseline != nil && baseline.TotalMigrations > 0 {
		migrationBaseline = baseline
	}

	// Initialize table sync state for incremental sync support
	tableSyncState := make(map[string]*TableSyncState)
	tableInfo, _ := GetTableIncrementalInfo(cfg.Source)
	for tableName := range rowCounts {
		state := &TableSyncState{
			LastSyncedAt: time.Now(),
			RowCount:     rowCounts[tableName],
		}
		if info, ok := tableInfo[tableName]; ok {
			state.TimestampColumn = info.TimestampColumn
			state.PrimaryKeyColumn = info.PrimaryKey

			// Get current max values for incremental tracking
			if info.TimestampColumn != "" {
				parts := strings.SplitN(tableName, ".", 2)
				if len(parts) == 2 {
					maxTs, _ := GetMaxTimestamp(cfg.Source, parts[0], parts[1], info.TimestampColumn)
					state.MaxTimestamp = maxTs
				}
			} else if info.PrimaryKey != "" && isIntegerType(info.PrimaryKeyType) {
				parts := strings.SplitN(tableName, ".", 2)
				if len(parts) == 2 {
					maxPK, _ := GetMaxPrimaryKey(cfg.Source, parts[0], parts[1], info.PrimaryKey)
					state.MaxPrimaryKey = maxPK
				}
			}
		}
		tableSyncState[tableName] = state
	}

	// Create metadata
	metadata := &SyncMetadata{
		LastSyncAt:        time.Now(),
		SourceVersion:     pgVersion,
		SourceDatabase:    dbName,
		TableSizes:        tableSizes,
		ExcludedTables:    excludedTables,
		RowCounts:         rowCounts,
		SchemaHash:        schemaHash,
		GoldenFileSize:    goldenSize,
		SyncDurationMs:    time.Since(startTime).Milliseconds(),
		MigrationBaseline: migrationBaseline,
		TableSyncState:    tableSyncState,
		IncrementalFiles:  []string{}, // Clear incremental files on full sync
		IsIncremental:     false,
	}

	// Save metadata
	metadataPath := filepath.Join(projectDir, "metadata.json")
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataPath, metadataBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	return metadata, nil
}

// LoadSyncMetadata loads the sync metadata for a project
func LoadSyncMetadata(projectName string, dbsyncDir string) (*SyncMetadata, error) {
	metadataPath := filepath.Join(dbsyncDir, projectName, "metadata.json")

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No metadata yet
		}
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var metadata SyncMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &metadata, nil
}

// GoldenCopyExists checks if a golden copy exists for a project
func GoldenCopyExists(projectName string, dbsyncDir string) bool {
	goldenPath := filepath.Join(dbsyncDir, projectName, "golden.sql")
	_, err := os.Stat(goldenPath)
	return err == nil
}

// GetGoldenCopyPath returns the path to the golden copy for a project
func GetGoldenCopyPath(projectName string, dbsyncDir string) string {
	return filepath.Join(dbsyncDir, projectName, "golden.sql")
}

// GetSchemaOnlyPath returns the path to the schema-only dump for excluded tables
func GetSchemaOnlyPath(projectName string, dbsyncDir string) string {
	return filepath.Join(dbsyncDir, projectName, "schema.sql")
}

// determineExclusions determines which tables to exclude based on config and heuristics
func determineExclusions(tables []TableInfo, cfg *DatabaseConfig) []string {
	excluded := make(map[string]bool)

	// Add explicitly excluded tables
	for _, t := range cfg.ExcludeTables {
		excluded[t] = true
	}

	// Add tables exceeding size threshold
	if cfg.SizeThresholdMB > 0 {
		thresholdBytes := int64(cfg.SizeThresholdMB) * 1024 * 1024
		for _, t := range tables {
			if t.SizeBytes > thresholdBytes {
				fullName := t.Schema + "." + t.Name
				excluded[fullName] = true
			}
		}
	}

	// Convert map to slice
	result := make([]string, 0, len(excluded))
	for t := range excluded {
		result = append(result, t)
	}

	return result
}

// ProgressFunc is a callback for reporting sync progress
type ProgressFunc func(message string)

// runPgDumpWithProgressCtx executes pg_dump with progress reporting and context for cancellation
func runPgDumpWithProgressCtx(ctx context.Context, connStr string, outputPath string, excludedTables []string, schemaOnly bool, progress ProgressFunc) error {
	args := []string{
		connStr,
		"--file=" + outputPath,
		"--no-owner",
		"--no-acl",
		"--verbose",
	}

	if schemaOnly {
		args = append(args, "--schema-only")
	}

	// Exclude data for specified tables (but keep schema)
	for _, table := range excludedTables {
		args = append(args, "--exclude-table-data="+table)
	}

	cmd := exec.CommandContext(ctx, "pg_dump", args...)

	// Capture stderr for progress (pg_dump verbose output goes to stderr)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start pg_dump: %w", err)
	}

	// Read stderr line by line for progress
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		// Check for cancellation
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			return ctx.Err()
		}
		line := scanner.Text()
		if progress != nil {
			// Parse pg_dump verbose output for user-friendly messages
			progress(parsePgDumpProgress(line))
		}
	}

	if err := cmd.Wait(); err != nil {
		// Check if it was cancelled
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("pg_dump failed: %w", err)
	}

	return nil
}

// parsePgDumpProgress extracts meaningful progress from pg_dump verbose output
func parsePgDumpProgress(line string) string {
	// pg_dump verbose output looks like:
	// pg_dump: dumping contents of table "public.users"
	// pg_dump: saving search_path =
	if strings.Contains(line, "dumping contents of table") {
		// Extract table name
		if idx := strings.Index(line, "\""); idx >= 0 {
			end := strings.LastIndex(line, "\"")
			if end > idx {
				return "Dumping: " + line[idx+1:end]
			}
		}
	}
	return line
}

// runPgDumpSchemaOnlyCtx creates a schema-only dump for specific tables with context for cancellation
func runPgDumpSchemaOnlyCtx(ctx context.Context, connStr string, outputPath string, tables []string) error {
	args := []string{
		connStr,
		"--file=" + outputPath,
		"--schema-only",
		"--no-owner",
		"--no-acl",
	}

	// Only include specified tables
	for _, table := range tables {
		args = append(args, "--table="+table)
	}

	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("pg_dump (schema-only) failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// computeSchemaHash computes a hash of the database schema for change detection
func computeSchemaHash(connStr string) (string, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query schema information
	query := `
		SELECT
			table_schema,
			table_name,
			column_name,
			data_type,
			is_nullable
		FROM information_schema.columns
		WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		ORDER BY table_schema, table_name, ordinal_position
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	var builder strings.Builder
	for rows.Next() {
		var schema, table, column, dataType, nullable string
		if err := rows.Scan(&schema, &table, &column, &dataType, &nullable); err != nil {
			return "", err
		}
		fmt.Fprintf(&builder, "%s.%s.%s:%s:%s\n", schema, table, column, dataType, nullable)
	}

	hash := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(hash[:]), nil
}

// DetectSchemaChanges compares current schema with stored hash
func DetectSchemaChanges(connStr string, storedHash string) (bool, error) {
	if storedHash == "" {
		return false, nil // No previous hash to compare
	}

	currentHash, err := computeSchemaHash(connStr)
	if err != nil {
		return false, err
	}

	return currentHash != storedHash, nil
}

// SyncCheckResult contains the result of checking if sync is needed
type SyncCheckResult struct {
	NeedsSync     bool
	Reason        string
	SchemaChanged bool
	RowCountDiff  int64 // Difference in total row counts
}

// CheckSyncNeeded checks if a sync is needed by comparing schema and row counts
func CheckSyncNeeded(cfg *DatabaseConfig, projectName string, dbsyncDir string) (*SyncCheckResult, error) {
	result := &SyncCheckResult{NeedsSync: true, Reason: "initial sync"}

	// Load existing metadata
	metadata, err := LoadSyncMetadata(projectName, dbsyncDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load metadata: %w", err)
	}

	// No previous sync - need full sync
	if metadata == nil {
		return result, nil
	}

	// Check schema changes
	schemaChanged, err := DetectSchemaChanges(cfg.Source, metadata.SchemaHash)
	if err != nil {
		// Non-fatal, assume we need sync
		result.Reason = "schema check failed, assuming sync needed"
		return result, nil
	}

	if schemaChanged {
		result.SchemaChanged = true
		result.Reason = "schema changed"
		return result, nil
	}

	// Check row count changes using accurate counts (not pg_stat estimates)
	currentCounts, err := GetAccurateRowCounts(cfg.Source)
	if err != nil {
		result.Reason = "row count check failed, assuming sync needed"
		return result, nil
	}

	var currentTotal int64
	for _, count := range currentCounts {
		currentTotal += count
	}

	var storedTotal int64
	for _, count := range metadata.RowCounts {
		storedTotal += count
	}

	diff := currentTotal - storedTotal
	if diff != 0 {
		result.RowCountDiff = diff
		result.Reason = fmt.Sprintf("row count changed (%+d rows)", diff)
		return result, nil
	}

	// No changes detected
	result.NeedsSync = false
	result.Reason = "no changes detected"
	return result, nil
}

// SyncIncrementalFromSource performs an incremental sync, only dumping new/changed rows
func SyncIncrementalFromSource(cfg *DatabaseConfig, projectName string, dbsyncDir string) (*SyncMetadata, error) {
	return SyncIncrementalFromSourceWithProgress(cfg, projectName, dbsyncDir, nil)
}

// SyncIncrementalFromSourceWithProgress performs incremental sync with progress callback
func SyncIncrementalFromSourceWithProgress(cfg *DatabaseConfig, projectName string, dbsyncDir string, progress ProgressFunc) (*SyncMetadata, error) {
	return SyncIncrementalFromSourceCtx(context.Background(), cfg, projectName, dbsyncDir, progress)
}

// SyncIncrementalFromSourceCtx performs incremental sync with context and progress
func SyncIncrementalFromSourceCtx(ctx context.Context, cfg *DatabaseConfig, projectName string, dbsyncDir string, progress ProgressFunc) (*SyncMetadata, error) {
	startTime := time.Now()
	projectDir := filepath.Join(dbsyncDir, projectName)

	if progress != nil {
		progress("Loading previous sync state...")
	}

	// Load existing metadata
	metadata, err := LoadSyncMetadata(projectName, dbsyncDir)
	if err != nil || metadata == nil {
		// No previous sync - can't do incremental
		return nil, fmt.Errorf("no previous sync found, cannot do incremental sync")
	}

	// Initialize TableSyncState if nil
	if metadata.TableSyncState == nil {
		metadata.TableSyncState = make(map[string]*TableSyncState)
	}

	if progress != nil {
		progress("Analyzing tables for incremental sync...")
	}

	// Get incremental info for all tables
	tableInfo, err := GetTableIncrementalInfo(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get table info: %w", err)
	}

	// Get current row counts
	tables, err := GetTableInfo(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get table info: %w", err)
	}

	// Build map of current row counts
	currentRowCounts := make(map[string]int64)
	for _, t := range tables {
		fullName := t.Schema + "." + t.Name
		currentRowCounts[fullName] = t.RowCount
	}

	// Find tables with changes
	var tablesWithChanges []string
	for tableName, currentCount := range currentRowCounts {
		previousCount, exists := metadata.RowCounts[tableName]
		if !exists || currentCount > previousCount {
			tablesWithChanges = append(tablesWithChanges, tableName)
		}
	}

	if len(tablesWithChanges) == 0 {
		if progress != nil {
			progress("No changes detected")
		}
		return metadata, nil
	}

	if progress != nil {
		progress(fmt.Sprintf("Found %d tables with changes", len(tablesWithChanges)))
	}

	// Generate incremental file name
	incrementalFileName := fmt.Sprintf("incremental-%s.sql", time.Now().Format("20060102-150405"))
	incrementalPath := filepath.Join(projectDir, incrementalFileName)

	// Open incremental file for writing
	f, err := os.Create(incrementalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create incremental file: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Write header
	_, _ = f.WriteString("-- Incremental sync generated at " + time.Now().Format(time.RFC3339) + "\n")
	_, _ = f.WriteString("-- Tables: " + strings.Join(tablesWithChanges, ", ") + "\n\n")

	totalRowsDumped := int64(0)

	// Dump each table incrementally
	for i, tableName := range tablesWithChanges {
		if ctx.Err() != nil {
			_ = os.Remove(incrementalPath)
			return nil, fmt.Errorf("sync cancelled: %w", ctx.Err())
		}

		info := tableInfo[tableName]
		if info == nil {
			continue
		}

		if progress != nil {
			progress(fmt.Sprintf("Dumping %s (%d/%d)...", tableName, i+1, len(tablesWithChanges)))
		}

		// Get previous sync state for this table
		prevState := metadata.TableSyncState[tableName]

		var rowsDumped int64
		var newMaxTs *time.Time
		var newMaxPK *int64

		if info.TimestampColumn != "" {
			// Use timestamp-based incremental
			var sinceTs *time.Time
			if prevState != nil && prevState.MaxTimestamp != nil {
				sinceTs = prevState.MaxTimestamp
			}
			rowsDumped, newMaxTs, err = dumpTableIncrementalByTimestamp(ctx, cfg.Source, info, sinceTs, f)
		} else if info.PrimaryKey != "" && isIntegerType(info.PrimaryKeyType) {
			// Use PK-based incremental for tables without timestamps
			var sincePK *int64
			if prevState != nil && prevState.MaxPrimaryKey != nil {
				sincePK = prevState.MaxPrimaryKey
			}
			rowsDumped, newMaxPK, err = dumpTableIncrementalByPK(ctx, cfg.Source, info, sincePK, f)
		} else {
			// No incremental possible - skip (will be caught in next full sync)
			if progress != nil {
				progress(fmt.Sprintf("  Skipping %s (no timestamp or integer PK)", tableName))
			}
			continue
		}

		if err != nil {
			// Log error but continue with other tables
			_, _ = fmt.Fprintf(f, "-- ERROR dumping %s: %v\n", tableName, err)
			continue
		}

		totalRowsDumped += rowsDumped

		// Update table sync state
		metadata.TableSyncState[tableName] = &TableSyncState{
			LastSyncedAt:     time.Now(),
			TimestampColumn:  info.TimestampColumn,
			MaxTimestamp:     newMaxTs,
			PrimaryKeyColumn: info.PrimaryKey,
			MaxPrimaryKey:    newMaxPK,
			RowCount:         currentRowCounts[tableName],
		}
	}

	// Update metadata
	metadata.LastSyncAt = time.Now()
	metadata.RowCounts = currentRowCounts
	metadata.SyncDurationMs = time.Since(startTime).Milliseconds()
	metadata.IsIncremental = true

	// Add incremental file to list
	if metadata.IncrementalFiles == nil {
		metadata.IncrementalFiles = []string{}
	}
	metadata.IncrementalFiles = append(metadata.IncrementalFiles, incrementalFileName)

	// Update table sizes
	metadata.TableSizes = make(map[string]int64)
	for _, t := range tables {
		fullName := t.Schema + "." + t.Name
		metadata.TableSizes[fullName] = t.SizeBytes
	}

	// Save metadata
	metadataPath := filepath.Join(projectDir, "metadata.json")
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := os.WriteFile(metadataPath, metadataBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	if progress != nil {
		progress(fmt.Sprintf("Incremental sync complete: %d rows in %d tables", totalRowsDumped, len(tablesWithChanges)))
	}

	return metadata, nil
}

// dumpTableIncrementalByTimestamp dumps rows newer than the given timestamp
func dumpTableIncrementalByTimestamp(ctx context.Context, connStr string, info *TableIncrementalInfo, since *time.Time, w *os.File) (int64, *time.Time, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = db.Close() }()

	fullTable := fmt.Sprintf("%s.%s", quoteIdentifier(info.Schema), quoteIdentifier(info.Name))
	tsCol := quoteIdentifier(info.TimestampColumn)

	// Build query
	var query string
	var args []interface{}
	if since != nil {
		query = fmt.Sprintf("SELECT * FROM %s WHERE %s > $1 ORDER BY %s", fullTable, tsCol, tsCol)
		args = []interface{}{*since}
	} else {
		query = fmt.Sprintf("SELECT * FROM %s ORDER BY %s", fullTable, tsCol)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return 0, nil, err
	}

	// Write table header
	_, _ = fmt.Fprintf(w, "\n-- Table: %s.%s (incremental by %s)\n", info.Schema, info.Name, info.TimestampColumn)

	var rowCount int64
	var maxTs *time.Time

	for rows.Next() {
		if ctx.Err() != nil {
			return rowCount, maxTs, ctx.Err()
		}

		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return rowCount, maxTs, err
		}

		// Track max timestamp
		for i, col := range columns {
			if col == info.TimestampColumn {
				if ts, ok := values[i].(time.Time); ok {
					if maxTs == nil || ts.After(*maxTs) {
						maxTs = &ts
					}
				}
			}
		}

		// Generate INSERT statement
		insert := generateInsertStatement(info.Schema, info.Name, columns, values)
		_, _ = w.WriteString(insert + "\n")
		rowCount++
	}

	return rowCount, maxTs, rows.Err()
}

// dumpTableIncrementalByPK dumps rows with PK greater than the given value
func dumpTableIncrementalByPK(ctx context.Context, connStr string, info *TableIncrementalInfo, sincePK *int64, w *os.File) (int64, *int64, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = db.Close() }()

	fullTable := fmt.Sprintf("%s.%s", quoteIdentifier(info.Schema), quoteIdentifier(info.Name))
	pkCol := quoteIdentifier(info.PrimaryKey)

	// Build query
	var query string
	var args []interface{}
	if sincePK != nil {
		query = fmt.Sprintf("SELECT * FROM %s WHERE %s > $1 ORDER BY %s", fullTable, pkCol, pkCol)
		args = []interface{}{*sincePK}
	} else {
		query = fmt.Sprintf("SELECT * FROM %s ORDER BY %s", fullTable, pkCol)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return 0, nil, err
	}

	// Write table header
	_, _ = fmt.Fprintf(w, "\n-- Table: %s.%s (incremental by %s)\n", info.Schema, info.Name, info.PrimaryKey)

	var rowCount int64
	var maxPK *int64

	for rows.Next() {
		if ctx.Err() != nil {
			return rowCount, maxPK, ctx.Err()
		}

		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return rowCount, maxPK, err
		}

		// Track max PK
		for i, col := range columns {
			if col == info.PrimaryKey {
				if pk, ok := values[i].(int64); ok {
					if maxPK == nil || pk > *maxPK {
						maxPK = &pk
					}
				}
			}
		}

		// Generate INSERT statement
		insert := generateInsertStatement(info.Schema, info.Name, columns, values)
		_, _ = w.WriteString(insert + "\n")
		rowCount++
	}

	return rowCount, maxPK, rows.Err()
}

// generateInsertStatement generates an INSERT statement for a row
func generateInsertStatement(schema, table string, columns []string, values []interface{}) string {
	var colNames []string
	var valStrings []string

	for i, col := range columns {
		colNames = append(colNames, quoteIdentifier(col))
		valStrings = append(valStrings, formatValue(values[i]))
	}

	return fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s);",
		quoteIdentifier(schema),
		quoteIdentifier(table),
		strings.Join(colNames, ", "),
		strings.Join(valStrings, ", "))
}

// formatValue formats a Go value as a SQL literal
func formatValue(v interface{}) string {
	if v == nil {
		return "NULL"
	}

	switch val := v.(type) {
	case bool:
		if val {
			return "TRUE"
		}
		return "FALSE"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", val)
	case float32, float64:
		return fmt.Sprintf("%v", val)
	case string:
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	case []byte:
		return "'" + strings.ReplaceAll(string(val), "'", "''") + "'"
	case time.Time:
		return "'" + val.Format("2006-01-02 15:04:05.999999-07") + "'"
	default:
		// For other types, convert to string
		s := fmt.Sprintf("%v", val)
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
}


// isIntegerType checks if a PostgreSQL type is an integer type
func isIntegerType(t string) bool {
	t = strings.ToLower(t)
	return t == "integer" || t == "bigint" || t == "smallint" || t == "serial" || t == "bigserial"
}

// DeleteGoldenCopyFiles deletes all sync files to force a full sync
func DeleteGoldenCopyFiles(projectName string, dbsyncDir string) error {
	projectDir := filepath.Join(dbsyncDir, projectName)

	// Check if directory exists
	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		return nil // Nothing to delete
	}

	// Remove entire project sync directory
	if err := os.RemoveAll(projectDir); err != nil {
		return fmt.Errorf("failed to delete sync directory: %w", err)
	}

	return nil
}

// =============================================================================
// V2 Sync: Separated Schema + Per-Table Data Files
// =============================================================================

const SyncVersionSeparated = 2

// SyncV2FromSourceCtx performs a v2 sync with separated schema and per-table data files
// This allows incremental sync even after schema changes (migrations)
func SyncV2FromSourceCtx(ctx context.Context, cfg *DatabaseConfig, projectName string, dbsyncDir string, progress ProgressFunc) (*SyncMetadata, error) {
	startTime := time.Now()
	projectDir := filepath.Join(dbsyncDir, projectName)
	dataDir := filepath.Join(projectDir, "data")

	// Create directories
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	// Load existing metadata
	metadata, _ := LoadSyncMetadata(projectName, dbsyncDir)

	// Get current schema snapshot
	if progress != nil {
		progress("Analyzing schema...")
	}
	currentSchema, err := GetSchemaSnapshot(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema snapshot: %w", err)
	}

	// Determine what needs to be synced
	var schemaDiff *SchemaDiff
	isFirstSync := metadata == nil || metadata.SyncVersion != SyncVersionSeparated

	if !isFirstSync && metadata.SchemaSnapshot != nil {
		schemaDiff = ComputeSchemaDiff(metadata.SchemaSnapshot, currentSchema)
		if progress != nil {
			if schemaDiff.HasChanges {
				progress(fmt.Sprintf("Schema changed: +%d tables, -%d tables, ~%d modified",
					len(schemaDiff.NewTables), len(schemaDiff.RemovedTables), len(schemaDiff.ModifiedTables)))
			} else {
				progress("Schema unchanged")
			}
		}
	}

	// Always dump schema.sql (it's small and fast)
	if progress != nil {
		progress("Dumping schema...")
	}
	schemaPath := filepath.Join(projectDir, "schema.sql")
	if err := runPgDumpSchemaOnlyCtx(ctx, cfg.Source, schemaPath, nil); err != nil {
		return nil, fmt.Errorf("failed to dump schema: %w", err)
	}

	// Determine which tables need data sync
	tablesToSync := determineTablesToSync(isFirstSync, schemaDiff, currentSchema, metadata)

	// Get table info for exclusions
	tables, err := GetTableInfo(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get table info: %w", err)
	}
	excludedTables := determineExclusions(tables, cfg)
	excludedMap := make(map[string]bool)
	for _, t := range excludedTables {
		excludedMap[t] = true
	}

	// Initialize metadata fields
	tableDataFiles := make(map[string]string)
	if metadata != nil && metadata.TableDataFiles != nil {
		// Preserve existing data file references
		tableDataFiles = metadata.TableDataFiles
	}

	rowCounts, err := GetAccurateRowCounts(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get row counts: %w", err)
	}

	tableSyncState := make(map[string]*TableSyncState)
	if metadata != nil && metadata.TableSyncState != nil {
		tableSyncState = metadata.TableSyncState
	}

	tableInfo, _ := GetTableIncrementalInfo(cfg.Source)

	// Track incremental files created during this sync
	var incrementalFiles []string

	// Filter out excluded tables from tablesToSync
	var filteredTables []string
	for _, tableName := range tablesToSync {
		if !excludedMap[tableName] {
			filteredTables = append(filteredTables, tableName)
		}
	}

	if progress != nil && len(excludedTables) > 0 {
		progress(fmt.Sprintf("Excluding %d tables from data sync (schema only)", len(excludedTables)))
	}

	// Sync each table that needs it
	for i, tableName := range filteredTables {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sync cancelled: %w", ctx.Err())
		}

		if progress != nil {
			progress(fmt.Sprintf("Syncing %s (%d/%d)...", tableName, i+1, len(filteredTables)))
		}

		// Check if this is incremental or full table sync
		prevState := tableSyncState[tableName]
		info := tableInfo[tableName]

		needsFullTableSync := isFirstSync ||
			(schemaDiff != nil && contains(schemaDiff.NewTables, tableName)) ||
			(schemaDiff != nil && contains(schemaDiff.ModifiedTables, tableName)) ||
			prevState == nil

		if needsFullTableSync {
			// Full table data dump
			dataPath := filepath.Join(dataDir, sanitizeFileName(tableName)+".sql")
			if err := dumpTableData(ctx, cfg.Source, tableName, dataPath); err != nil {
				return nil, fmt.Errorf("failed to dump table %s: %w", tableName, err)
			}
			tableDataFiles[tableName] = "data/" + sanitizeFileName(tableName) + ".sql"

			// Update sync state
			state := &TableSyncState{
				LastSyncedAt: time.Now(),
				RowCount:     rowCounts[tableName],
			}
			if info != nil {
				state.TimestampColumn = info.TimestampColumn
				state.PrimaryKeyColumn = info.PrimaryKey
				// Get max values
				parts := strings.SplitN(tableName, ".", 2)
				if len(parts) == 2 {
					if info.TimestampColumn != "" {
						maxTs, _ := GetMaxTimestamp(cfg.Source, parts[0], parts[1], info.TimestampColumn)
						state.MaxTimestamp = maxTs
					} else if info.PrimaryKey != "" && isIntegerType(info.PrimaryKeyType) {
						maxPK, _ := GetMaxPrimaryKey(cfg.Source, parts[0], parts[1], info.PrimaryKey)
						state.MaxPrimaryKey = maxPK
					}
				}
			}
			tableSyncState[tableName] = state
		} else if prevState != nil && rowCounts[tableName] > prevState.RowCount {
			// Incremental sync - only new rows
			incFileName := fmt.Sprintf("incremental-%s-%s.sql", sanitizeFileName(tableName), time.Now().Format("20060102-150405"))
			incPath := filepath.Join(projectDir, incFileName)

			var newMaxTs *time.Time
			var newMaxPK *int64
			var rowsDumped int64

			f, err := os.Create(incPath)
			if err != nil {
				return nil, fmt.Errorf("failed to create incremental file: %w", err)
			}

			if info != nil && info.TimestampColumn != "" && prevState.MaxTimestamp != nil {
				rowsDumped, newMaxTs, err = dumpTableIncrementalByTimestamp(ctx, cfg.Source, info, prevState.MaxTimestamp, f)
			} else if info != nil && info.PrimaryKey != "" && prevState.MaxPrimaryKey != nil {
				rowsDumped, newMaxPK, err = dumpTableIncrementalByPK(ctx, cfg.Source, info, prevState.MaxPrimaryKey, f)
			}
			f.Close()

			if err != nil {
				_ = os.Remove(incPath)
				continue // Skip this table on error
			}

			if rowsDumped == 0 {
				_ = os.Remove(incPath) // No new rows, remove empty file
			} else {
				// Track incremental file for restore
				incrementalFiles = append(incrementalFiles, incFileName)
			}

			// Update sync state
			prevState.LastSyncedAt = time.Now()
			prevState.RowCount = rowCounts[tableName]
			if newMaxTs != nil {
				prevState.MaxTimestamp = newMaxTs
			}
			if newMaxPK != nil {
				prevState.MaxPrimaryKey = newMaxPK
			}
		}
	}

	// Remove data files for deleted tables
	if schemaDiff != nil {
		for _, tableName := range schemaDiff.RemovedTables {
			if dataFile, exists := tableDataFiles[tableName]; exists {
				_ = os.Remove(filepath.Join(projectDir, dataFile))
				delete(tableDataFiles, tableName)
				delete(tableSyncState, tableName)
			}
		}
	}

	// Get schema hash for backwards compatibility
	schemaHash, _ := computeSchemaHash(cfg.Source)

	// Build table sizes
	tableSizes := make(map[string]int64)
	for _, t := range tables {
		fullName := t.Schema + "." + t.Name
		tableSizes[fullName] = t.SizeBytes
	}

	// Create/update metadata
	newMetadata := &SyncMetadata{
		LastSyncAt:       time.Now(),
		SourceVersion:    "",
		SourceDatabase:   "",
		TableSizes:       tableSizes,
		ExcludedTables:   excludedTables,
		RowCounts:        rowCounts,
		SchemaHash:       schemaHash,
		SyncDurationMs:   time.Since(startTime).Milliseconds(),
		TableSyncState:   tableSyncState,
		TableDataFiles:   tableDataFiles,
		SchemaSnapshot:   currentSchema,
		SyncVersion:      SyncVersionSeparated,
		IsIncremental:    !isFirstSync && (schemaDiff == nil || !schemaDiff.HasChanges || len(schemaDiff.NewTables) == 0),
		IncrementalFiles: incrementalFiles,
	}

	// Get source info
	if dbName, err := GetDatabaseName(cfg.Source); err == nil {
		newMetadata.SourceDatabase = dbName
	}
	if pgVersion, err := GetPostgresVersion(cfg.Source); err == nil {
		newMetadata.SourceVersion = pgVersion
	}

	// Save metadata
	metadataPath := filepath.Join(projectDir, "metadata.json")
	metadataBytes, err := json.MarshalIndent(newMetadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := os.WriteFile(metadataPath, metadataBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	if progress != nil {
		if isFirstSync {
			progress(fmt.Sprintf("Full sync completed: %d tables (%d excluded)", len(filteredTables), len(excludedTables)))
		} else if schemaDiff != nil && schemaDiff.HasChanges {
			progress(fmt.Sprintf("Schema sync completed: +%d new tables", len(schemaDiff.NewTables)))
		} else {
			progress("Incremental sync completed")
		}
	}

	return newMetadata, nil
}

// determineTablesToSync determines which tables need to be synced based on current state
func determineTablesToSync(isFirstSync bool, schemaDiff *SchemaDiff, currentSchema map[string]*TableSchema, metadata *SyncMetadata) []string {
	if isFirstSync {
		// First sync: all tables
		tables := make([]string, 0, len(currentSchema))
		for tableName := range currentSchema {
			tables = append(tables, tableName)
		}
		return tables
	}

	if schemaDiff == nil || !schemaDiff.HasChanges {
		// No schema changes: check for data changes only
		if metadata == nil || metadata.RowCounts == nil {
			// No previous state, sync all
			tables := make([]string, 0, len(currentSchema))
			for tableName := range currentSchema {
				tables = append(tables, tableName)
			}
			return tables
		}
		// Return all existing tables (will be checked for row count changes)
		tables := make([]string, 0, len(currentSchema))
		for tableName := range currentSchema {
			tables = append(tables, tableName)
		}
		return tables
	}

	// Schema changed: sync new tables + tables with data changes
	tables := make([]string, 0)

	// New tables need full sync
	tables = append(tables, schemaDiff.NewTables...)

	// Modified tables need full sync (schema changed)
	tables = append(tables, schemaDiff.ModifiedTables...)

	// Existing unchanged tables - check for data changes
	for tableName := range currentSchema {
		if !contains(schemaDiff.NewTables, tableName) &&
			!contains(schemaDiff.ModifiedTables, tableName) &&
			!contains(schemaDiff.RemovedTables, tableName) {
			tables = append(tables, tableName)
		}
	}

	return tables
}

// dumpTableData dumps all data from a single table to a file
func dumpTableData(ctx context.Context, connStr string, tableName string, outputPath string) error {
	parts := strings.SplitN(tableName, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid table name: %s", tableName)
	}

	args := []string{
		connStr,
		"--file=" + outputPath,
		"--no-owner",
		"--no-acl",
		"--data-only",
		"--table=" + tableName,
	}

	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump failed: %w, output: %s", err, string(output))
	}

	return nil
}

// sanitizeFileName converts a table name to a safe file name
func sanitizeFileName(name string) string {
	// Replace dots and other special chars with underscores
	safe := strings.ReplaceAll(name, ".", "_")
	safe = strings.ReplaceAll(safe, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	return safe
}

// contains checks if a string slice contains a value
func contains(slice []string, value string) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}
