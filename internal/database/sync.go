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
	return SyncFromSourceWithProgress(cfg, projectName, dbsyncDir, nil)
}

// SyncFromSourceWithProgress syncs with progress callback
func SyncFromSourceWithProgress(cfg *DatabaseConfig, projectName string, dbsyncDir string, progress ProgressFunc) (*SyncMetadata, error) {
	startTime := time.Now()

	if progress != nil {
		progress("Creating sync directory...")
	}

	// Create project sync directory
	projectDir := filepath.Join(dbsyncDir, projectName)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sync directory: %w", err)
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

	// Build table sizes and row counts maps
	tableSizes := make(map[string]int64)
	rowCounts := make(map[string]int64)
	for _, t := range tables {
		fullName := t.Schema + "." + t.Name
		tableSizes[fullName] = t.SizeBytes
		rowCounts[fullName] = t.RowCount
	}

	if progress != nil {
		progress(fmt.Sprintf("Dumping %d tables...", len(tables)))
	}

	// Run pg_dump for main dump (excluding data for excluded tables)
	goldenPath := filepath.Join(projectDir, "golden.sql")
	if err := runPgDumpWithProgress(cfg.Source, goldenPath, excludedTables, false, progress); err != nil {
		return nil, fmt.Errorf("failed to create golden dump: %w", err)
	}

	// If there are excluded tables, create schema-only dump for them
	schemaPath := filepath.Join(projectDir, "schema.sql")
	if len(excludedTables) > 0 {
		if err := runPgDumpSchemaOnly(cfg.Source, schemaPath, excludedTables); err != nil {
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

// runPgDumpWithProgress executes pg_dump with progress reporting
func runPgDumpWithProgress(connStr string, outputPath string, excludedTables []string, schemaOnly bool, progress ProgressFunc) error {
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

	cmd := exec.Command("pg_dump", args...)

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
		line := scanner.Text()
		if progress != nil {
			// Parse pg_dump verbose output for user-friendly messages
			progress(parsePgDumpProgress(line))
		}
	}

	if err := cmd.Wait(); err != nil {
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

// runPgDumpSchemaOnly creates a schema-only dump for specific tables
func runPgDumpSchemaOnly(connStr string, outputPath string, tables []string) error {
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

	cmd := exec.Command("pg_dump", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
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
		builder.WriteString(fmt.Sprintf("%s.%s.%s:%s:%s\n", schema, table, column, dataType, nullable))
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

	// Check row count changes
	tables, err := GetTableInfo(cfg.Source)
	if err != nil {
		result.Reason = "row count check failed, assuming sync needed"
		return result, nil
	}

	var currentTotal int64
	for _, t := range tables {
		currentTotal += t.RowCount
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
