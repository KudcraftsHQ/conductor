package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// CloneToWorktree clones the golden copy to a new worktree database
// Supports both V1 (single golden.sql) and V2 (separated schema + data files)
func CloneToWorktree(localURL string, dbName string, goldenPath string, schemaPath string) error {
	// Create the database
	if err := CreateDatabase(localURL, dbName); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	// Build connection string for the new database
	targetURL := BuildWorktreeURL(localURL, dbName)

	// Restore the golden dump
	if err := restoreDump(targetURL, goldenPath); err != nil {
		// Try to clean up on failure
		_ = DropDatabase(localURL, dbName)
		return fmt.Errorf("failed to restore golden dump: %w", err)
	}

	// Restore schema-only tables if they exist
	if schemaPath != "" {
		if _, err := os.Stat(schemaPath); err == nil {
			if err := restoreDump(targetURL, schemaPath); err != nil {
				// Non-fatal - schema-only tables are optional
				fmt.Printf("Warning: failed to restore schema-only tables: %v\n", err)
			}
		}
	}

	return nil
}

// CloneToWorktreeV2 clones using the V2 separated sync format (schema.sql + data/*.sql)
func CloneToWorktreeV2(localURL string, dbName string, projectDir string, metadata *SyncMetadata) error {
	// Create the database
	if err := CreateDatabase(localURL, dbName); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	targetURL := BuildWorktreeURL(localURL, dbName)

	// 1. Restore schema first
	schemaPath := filepath.Join(projectDir, "schema.sql")
	if _, err := os.Stat(schemaPath); err == nil {
		if err := restoreDump(targetURL, schemaPath); err != nil {
			_ = DropDatabase(localURL, dbName)
			return fmt.Errorf("failed to restore schema: %w", err)
		}
	}

	// 2. Restore each table's data file
	if metadata != nil && metadata.TableDataFiles != nil {
		for tableName, dataFile := range metadata.TableDataFiles {
			dataPath := filepath.Join(projectDir, dataFile)
			if _, err := os.Stat(dataPath); err == nil {
				if err := restoreDump(targetURL, dataPath); err != nil {
					// Log but continue - some data is better than none
					fmt.Printf("Warning: failed to restore data for %s: %v\n", tableName, err)
				}
			}
		}
	}

	// 3. Restore incremental files in order (if any)
	if metadata != nil && len(metadata.IncrementalFiles) > 0 {
		for _, incFile := range metadata.IncrementalFiles {
			incPath := filepath.Join(projectDir, incFile)
			if _, err := os.Stat(incPath); err == nil {
				if err := restoreDump(targetURL, incPath); err != nil {
					fmt.Printf("Warning: failed to restore incremental %s: %v\n", incFile, err)
				}
			}
		}
	}

	return nil
}

// CloneResult contains the result of a migration-aware clone operation
type CloneResult struct {
	// DatabaseName is the name of the created database
	DatabaseName string `json:"databaseName"`

	// DatabaseURL is the connection string to the new database
	DatabaseURL string `json:"databaseUrl"`

	// MigrationState is the detected migration compatibility
	MigrationState *MigrationState `json:"migrationState,omitempty"`

	// RecommendedAction is a user-friendly suggestion
	RecommendedAction string `json:"recommendedAction"`
}

// CloneToWorktreeWithMigrations clones golden copy and detects migration state
func CloneToWorktreeWithMigrations(localURL, dbName, goldenPath, schemaPath, worktreePath string) (*CloneResult, error) {
	result := &CloneResult{
		DatabaseName: dbName,
		DatabaseURL:  BuildWorktreeURL(localURL, dbName),
	}

	// Clone the golden copy
	if err := CloneToWorktree(localURL, dbName, goldenPath, schemaPath); err != nil {
		return nil, err
	}

	// Check if worktree uses Prisma migrations
	if !HasPrismaMigrations(worktreePath) {
		result.RecommendedAction = "Database cloned. No Prisma migrations detected in worktree."
		return result, nil
	}

	// Detect migration state
	state, err := DetectMigrationState(result.DatabaseURL, worktreePath)
	if err != nil {
		// Non-fatal - clone succeeded, just couldn't detect migration state
		result.RecommendedAction = fmt.Sprintf("Database cloned. Could not detect migration state: %v", err)
		return result, nil
	}

	result.MigrationState = state
	result.RecommendedAction = state.RecommendedAction

	return result, nil
}

// ReinitializeDatabase drops and re-clones a worktree database
func ReinitializeDatabase(localURL, dbName, goldenPath, schemaPath, worktreePath string) (*CloneResult, error) {
	// Drop existing database if it exists
	exists, err := DatabaseExists(localURL, dbName)
	if err != nil {
		return nil, fmt.Errorf("failed to check database existence: %w", err)
	}

	if exists {
		if err := DropDatabase(localURL, dbName); err != nil {
			return nil, fmt.Errorf("failed to drop existing database: %w", err)
		}
	}

	// Clone fresh from golden copy
	return CloneToWorktreeWithMigrations(localURL, dbName, goldenPath, schemaPath, worktreePath)
}

// GetWorktreeMigrationStatus checks migration state for an existing worktree database
func GetWorktreeMigrationStatus(localURL, dbName, worktreePath string) (*MigrationState, error) {
	dbURL := BuildWorktreeURL(localURL, dbName)

	// Check if database exists
	exists, err := DatabaseExists(localURL, dbName)
	if err != nil {
		return nil, fmt.Errorf("failed to check database: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("database %s does not exist", dbName)
	}

	// Check if worktree uses Prisma
	if !HasPrismaMigrations(worktreePath) {
		return &MigrationState{
			Compatibility:     MigrationUnknown,
			RecommendedAction: "No Prisma migrations detected in worktree",
		}, nil
	}

	return DetectMigrationState(dbURL, worktreePath)
}

// CreateDatabase creates a new database
func CreateDatabase(localURL string, dbName string) error {
	// Connect to postgres database (or template1) to create new database
	info, err := ParseConnectionString(localURL)
	if err != nil {
		return err
	}

	// Connect to postgres database
	info.Database = "postgres"
	adminURL := BuildConnectionString(info)

	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Sanitize database name
	safeName := sanitizeDBName(dbName)

	// Check if database already exists
	var exists bool
	err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", safeName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check database existence: %w", err)
	}

	if exists {
		return fmt.Errorf("database %s already exists", safeName)
	}

	// Create database (can't use prepared statements for CREATE DATABASE)
	_, err = db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", quoteIdentifier(safeName)))
	if err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	return nil
}

// DropDatabase drops a database
func DropDatabase(localURL string, dbName string) error {
	// Connect to postgres database to drop
	info, err := ParseConnectionString(localURL)
	if err != nil {
		return err
	}

	info.Database = "postgres"
	adminURL := BuildConnectionString(info)

	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	safeName := sanitizeDBName(dbName)

	// Terminate existing connections to the database
	_, _ = db.ExecContext(ctx, fmt.Sprintf(`
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = '%s' AND pid <> pg_backend_pid()
	`, safeName))

	// Drop database
	_, err = db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", quoteIdentifier(safeName)))
	if err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}

	return nil
}

// DatabaseExists checks if a database exists
func DatabaseExists(localURL string, dbName string) (bool, error) {
	info, err := ParseConnectionString(localURL)
	if err != nil {
		return false, err
	}

	info.Database = "postgres"
	adminURL := BuildConnectionString(info)

	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return false, fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	safeName := sanitizeDBName(dbName)

	var exists bool
	err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", safeName).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check database existence: %w", err)
	}

	return exists, nil
}

// ListDatabases lists all databases matching a pattern
func ListDatabases(localURL string, pattern string) ([]string, error) {
	info, err := ParseConnectionString(localURL)
	if err != nil {
		return nil, err
	}

	info.Database = "postgres"
	adminURL := BuildConnectionString(info)

	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query := "SELECT datname FROM pg_database WHERE datname LIKE $1 ORDER BY datname"
	rows, err := db.QueryContext(ctx, query, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		databases = append(databases, name)
	}

	return databases, nil
}

// GenerateDBName generates a database name from project and port
func GenerateDBName(projectName string, port int, pattern string) string {
	if pattern == "" {
		pattern = DefaultDBNamePattern
	}

	name := pattern
	name = strings.ReplaceAll(name, "{project}", projectName)
	name = strings.ReplaceAll(name, "{port}", fmt.Sprintf("%d", port))

	return sanitizeDBName(name)
}

// restoreDump restores a SQL dump to a database
func restoreDump(targetURL string, dumpPath string) error {
	// Use psql to restore the dump
	cmd := exec.Command("psql", targetURL, "-f", dumpPath)
	cmd.Env = append(os.Environ(), "PGOPTIONS=-c client_min_messages=warning")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql restore failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// sanitizeDBName sanitizes a database name to be valid PostgreSQL identifier
func sanitizeDBName(name string) string {
	// Replace invalid characters with underscores
	re := regexp.MustCompile(`[^a-zA-Z0-9_]`)
	name = re.ReplaceAllString(name, "_")

	// Ensure it starts with a letter or underscore
	if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}

	// Truncate to 63 characters (PostgreSQL limit)
	if len(name) > 63 {
		name = name[:63]
	}

	// Lowercase
	name = strings.ToLower(name)

	return name
}

// quoteIdentifier quotes a PostgreSQL identifier
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
