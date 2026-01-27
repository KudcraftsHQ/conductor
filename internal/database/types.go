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

	// TableSyncState tracks per-table sync state for incremental sync
	TableSyncState map[string]*TableSyncState `json:"tableSyncState,omitempty"`

	// IncrementalFiles lists incremental SQL files to apply after golden.sql
	IncrementalFiles []string `json:"incrementalFiles,omitempty"`

	// IsIncremental indicates if this sync was incremental (vs full)
	IsIncremental bool `json:"isIncremental,omitempty"`

	// TableDataFiles maps table names to their data file paths (for separated sync)
	TableDataFiles map[string]string `json:"tableDataFiles,omitempty"`

	// SchemaSnapshot stores the table schemas for diff detection
	SchemaSnapshot map[string]*TableSchema `json:"schemaSnapshot,omitempty"`

	// SyncVersion indicates the sync format version (2 = separated schema+data)
	SyncVersion int `json:"syncVersion,omitempty"`
}

// TableSyncState tracks the sync state for a single table
type TableSyncState struct {
	// LastSyncedAt is when this table was last synced
	LastSyncedAt time.Time `json:"lastSyncedAt"`

	// TimestampColumn is the column used for incremental sync (created_at, updated_at, etc.)
	TimestampColumn string `json:"timestampColumn,omitempty"`

	// MaxTimestamp is the max value of the timestamp column at last sync
	MaxTimestamp *time.Time `json:"maxTimestamp,omitempty"`

	// PrimaryKeyColumn is the primary key column name
	PrimaryKeyColumn string `json:"primaryKeyColumn,omitempty"`

	// MaxPrimaryKey is the max primary key value at last sync (for tables without timestamps)
	MaxPrimaryKey *int64 `json:"maxPrimaryKey,omitempty"`

	// RowCount is the row count at last sync
	RowCount int64 `json:"rowCount"`
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

// SchemaDiff represents the differences between two schema versions
type SchemaDiff struct {
	// NewTables lists tables that exist in new schema but not in old
	NewTables []string `json:"newTables,omitempty"`

	// RemovedTables lists tables that existed in old schema but not in new
	RemovedTables []string `json:"removedTables,omitempty"`

	// ModifiedTables lists tables whose columns have changed
	ModifiedTables []string `json:"modifiedTables,omitempty"`

	// HasChanges returns true if there are any schema differences
	HasChanges bool `json:"hasChanges"`
}

// TableSchema represents the schema structure of a table for comparison
type TableSchema struct {
	Name    string         `json:"name"`
	Schema  string         `json:"schema"`
	Columns []ColumnSchema `json:"columns"`
}

// ColumnSchema represents a column's schema for comparison
type ColumnSchema struct {
	Name         string `json:"name"`
	DataType     string `json:"dataType"`
	IsNullable   bool   `json:"isNullable"`
	DefaultValue string `json:"defaultValue,omitempty"`
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

// ForeignKeyInfo represents a foreign key relationship between tables
type ForeignKeyInfo struct {
	TableSchema      string `json:"tableSchema"`      // Schema of the table with FK
	TableName        string `json:"tableName"`        // Table that has the FK constraint
	ColumnName       string `json:"columnName"`       // Column in this table
	ReferencedSchema string `json:"referencedSchema"` // Schema of referenced table
	ReferencedTable  string `json:"referencedTable"`  // Parent table being referenced
	ReferencedColumn string `json:"referencedColumn"` // Column in parent table
	ConstraintName   string `json:"constraintName"`   // Name of the FK constraint
}

// IndexInfo represents an index on a table
type IndexInfo struct {
	Schema    string   `json:"schema"`
	Table     string   `json:"table"`
	IndexName string   `json:"indexName"`
	Columns   []string `json:"columns"`
	IsUnique  bool     `json:"isUnique"`
	IsPrimary bool     `json:"isPrimary"`
}
