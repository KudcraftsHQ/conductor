package database

import (
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// Re-export DatabaseConfig from config package for convenience
type DatabaseConfig = config.DatabaseConfig

// SyncMetadata tracks the state of the golden copy
type SyncMetadata struct {
	// LastSyncAt is when the last successful sync completed
	LastSyncAt time.Time `json:"lastSyncAt"`

	// SourceVersion is the PostgreSQL version of the source
	SourceVersion string `json:"sourceVersion"`

	// SourceDatabase is the name of the source database
	SourceDatabase string `json:"sourceDatabase"`

	// TableSizes maps table names to their size in bytes
	TableSizes map[string]int64 `json:"tableSizes"`

	// ExcludedTables lists tables excluded from data sync (schema only)
	ExcludedTables []string `json:"excludedTables"`

	// RowCounts maps table names to approximate row counts
	RowCounts map[string]int64 `json:"rowCounts"`

	// SchemaHash is a hash of the schema for change detection
	SchemaHash string `json:"schemaHash"`

	// GoldenFileSize is the size of golden.sql in bytes
	GoldenFileSize int64 `json:"goldenFileSize"`

	// SyncDurationMs is how long the sync took in milliseconds
	SyncDurationMs int64 `json:"syncDurationMs"`

	// Error contains the last error message if sync failed
	Error string `json:"error,omitempty"`

	// MigrationBaseline tracks Prisma migration state for the golden copy
	MigrationBaseline *MigrationBaseline `json:"migrationBaseline,omitempty"`
}

// MigrationBaseline represents the migration state of a golden copy
type MigrationBaseline struct {
	// LastMigrationName is the name of the most recent migration
	LastMigrationName string `json:"lastMigrationName"`

	// LastMigrationChecksum is the checksum of the last migration
	LastMigrationChecksum string `json:"lastMigrationChecksum"`

	// TotalMigrations is the count of applied migrations
	TotalMigrations int `json:"totalMigrations"`

	// MigrationNames is the ordered list of all migration names
	MigrationNames []string `json:"migrationNames"`

	// CapturedAt is when this baseline was recorded
	CapturedAt time.Time `json:"capturedAt"`
}

// SyncStatus represents the current state of a sync operation
type SyncStatus string

const (
	SyncStatusIdle    SyncStatus = "idle"
	SyncStatusRunning SyncStatus = "running"
	SyncStatusFailed  SyncStatus = "failed"
)

// TableInfo contains information about a database table
type TableInfo struct {
	Name       string `json:"name"`
	Schema     string `json:"schema"`
	RowCount   int64  `json:"rowCount"`
	SizeBytes  int64  `json:"sizeBytes"`
	HasIndexes bool   `json:"hasIndexes"`
	IsAudit    bool   `json:"isAudit"` // Heuristic: likely an audit/log table
}

// SchemaChange represents a detected change in the source schema
type SchemaChange struct {
	Type        string `json:"type"` // "added", "removed", "modified"
	ObjectType  string `json:"objectType"` // "table", "column", "index"
	ObjectName  string `json:"objectName"`
	Description string `json:"description"`
}

// ConnectionInfo holds parsed connection string components
type ConnectionInfo struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
	SSLMode  string
}

// DefaultDBNamePattern is the default pattern for worktree database names
const DefaultDBNamePattern = "{project}-{port}"
