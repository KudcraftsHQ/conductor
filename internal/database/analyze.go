package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// GetTableInfo retrieves information about all tables in the database
func GetTableInfo(connStr string) ([]TableInfo, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	query := `
		SELECT
			t.table_schema,
			t.table_name,
			COALESCE(pg_total_relation_size(quote_ident(t.table_schema) || '.' || quote_ident(t.table_name)), 0) as size_bytes,
			COALESCE(s.n_live_tup, 0) as row_count,
			EXISTS(SELECT 1 FROM pg_indexes i WHERE i.schemaname = t.table_schema AND i.tablename = t.table_name) as has_indexes
		FROM information_schema.tables t
		LEFT JOIN pg_stat_user_tables s ON s.schemaname = t.table_schema AND s.relname = t.table_name
		WHERE t.table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND t.table_type = 'BASE TABLE'
		ORDER BY size_bytes DESC
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var tables []TableInfo
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Schema, &t.Name, &t.SizeBytes, &t.RowCount, &t.HasIndexes); err != nil {
			return nil, err
		}

		// Heuristic: detect audit/log tables
		t.IsAudit = isLikelyAuditTable(t.Name, t.RowCount, t.SizeBytes)

		tables = append(tables, t)
	}

	return tables, rows.Err()
}

// GetAccurateRowCounts returns accurate row counts using COUNT(*) for each table
// This is slower than pg_stat estimates but accurate for change detection
func GetAccurateRowCounts(connStr string) (map[string]int64, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Get list of tables
	tablesQuery := `
		SELECT table_schema, table_name
		FROM information_schema.tables
		WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND table_type = 'BASE TABLE'
	`
	rows, err := db.QueryContext(ctx, tablesQuery)
	if err != nil {
		return nil, err
	}

	var tables []struct{ schema, name string }
	for rows.Next() {
		var t struct{ schema, name string }
		if err := rows.Scan(&t.schema, &t.name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tables = append(tables, t)
	}
	_ = rows.Close()

	// Get accurate count for each table
	counts := make(map[string]int64)
	for _, t := range tables {
		fullName := t.schema + "." + t.name
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s",
			quoteIdentifier(t.schema), quoteIdentifier(t.name))

		var count int64
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			// Skip tables we can't count (permissions, etc.)
			continue
		}
		counts[fullName] = count
	}

	return counts, nil
}

// GetTableSizes returns a map of table names to their sizes in bytes
func GetTableSizes(connStr string) (map[string]int64, error) {
	tables, err := GetTableInfo(connStr)
	if err != nil {
		return nil, err
	}

	sizes := make(map[string]int64)
	for _, t := range tables {
		fullName := t.Schema + "." + t.Name
		sizes[fullName] = t.SizeBytes
	}

	return sizes, nil
}

// SuggestExclusions suggests tables to exclude based on size and heuristics
func SuggestExclusions(tables []TableInfo, thresholdMB int) []string {
	var suggestions []string

	thresholdBytes := int64(thresholdMB) * 1024 * 1024

	for _, t := range tables {
		fullName := t.Schema + "." + t.Name

		// Exclude if over size threshold
		if thresholdMB > 0 && t.SizeBytes > thresholdBytes {
			suggestions = append(suggestions, fullName)
			continue
		}

		// Exclude if likely audit/log table with many rows
		if t.IsAudit && t.RowCount > 100000 {
			suggestions = append(suggestions, fullName)
			continue
		}
	}

	return suggestions
}

// isLikelyAuditTable uses heuristics to detect audit/log tables
func isLikelyAuditTable(name string, rowCount int64, sizeBytes int64) bool {
	nameLower := strings.ToLower(name)

	// Common audit/log table name patterns
	auditPatterns := []string{
		"log",
		"logs",
		"audit",
		"audits",
		"event",
		"events",
		"history",
		"histories",
		"activity",
		"activities",
		"tracking",
		"archive",
		"backup",
		"queue",
		"job",
		"jobs",
		"notification",
		"notifications",
		"email",
		"emails",
		"message",
		"messages",
		"session",
		"sessions",
	}

	for _, pattern := range auditPatterns {
		if strings.Contains(nameLower, pattern) {
			return true
		}
	}

	// High row count with small average row size often indicates log tables
	if rowCount > 1000000 && sizeBytes > 0 {
		avgRowSize := sizeBytes / rowCount
		// Very small average row size (< 500 bytes) with many rows = likely logs
		if avgRowSize < 500 {
			return true
		}
	}

	return false
}

// GetDatabaseSize returns the total size of a database in bytes
func GetDatabaseSize(connStr string) (int64, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var size int64
	err = db.QueryRowContext(ctx, "SELECT pg_database_size(current_database())").Scan(&size)
	if err != nil {
		return 0, err
	}

	return size, nil
}

// GetTableCount returns the number of user tables in the database
func GetTableCount(connStr string) (int, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pg_stat_user_tables").Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// FormatSize formats a size in bytes to a human-readable string
func FormatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return formatFloat(float64(bytes)/float64(GB)) + " GB"
	case bytes >= MB:
		return formatFloat(float64(bytes)/float64(MB)) + " MB"
	case bytes >= KB:
		return formatFloat(float64(bytes)/float64(KB)) + " KB"
	default:
		return formatFloat(float64(bytes)) + " B"
	}
}

func formatFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	// Format with 1 decimal place, trim trailing zeros
	s := fmt.Sprintf("%.1f", f)
	return strings.TrimSuffix(s, ".0")
}

// TableIncrementalInfo contains information for incremental sync
type TableIncrementalInfo struct {
	Schema          string
	Name            string
	TimestampColumn string // Best timestamp column for tracking changes (updated_at preferred)
	PrimaryKey      string // Primary key column name
	PrimaryKeyType  string // int, bigint, uuid, etc.
}

// GetTableIncrementalInfo retrieves timestamp and primary key info for incremental sync
func GetTableIncrementalInfo(connStr string) (map[string]*TableIncrementalInfo, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result := make(map[string]*TableIncrementalInfo)

	// Get all tables first
	tablesQuery := `
		SELECT table_schema, table_name
		FROM information_schema.tables
		WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND table_type = 'BASE TABLE'
	`
	rows, err := db.QueryContext(ctx, tablesQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}

	var tables []struct{ schema, name string }
	for rows.Next() {
		var t struct{ schema, name string }
		if err := rows.Scan(&t.schema, &t.name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tables = append(tables, t)
	}
	_ = rows.Close()

	// For each table, find timestamp columns and primary key
	for _, t := range tables {
		fullName := t.schema + "." + t.name
		info := &TableIncrementalInfo{
			Schema: t.schema,
			Name:   t.name,
		}

		// Find timestamp columns (prefer updated_at > modified_at > created_at)
		timestampQuery := `
			SELECT column_name
			FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = $2
			  AND data_type IN ('timestamp with time zone', 'timestamp without time zone')
			  AND column_name IN ('updated_at', 'updatedat', 'modified_at', 'modifiedat',
			                      'created_at', 'createdat', 'inserted_at', 'insertedat')
			ORDER BY CASE column_name
				WHEN 'updated_at' THEN 1
				WHEN 'updatedat' THEN 2
				WHEN 'modified_at' THEN 3
				WHEN 'modifiedat' THEN 4
				WHEN 'created_at' THEN 5
				WHEN 'createdat' THEN 6
				WHEN 'inserted_at' THEN 7
				WHEN 'insertedat' THEN 8
				ELSE 9
			END
			LIMIT 1
		`
		var tsCol sql.NullString
		_ = db.QueryRowContext(ctx, timestampQuery, t.schema, t.name).Scan(&tsCol)
		if tsCol.Valid {
			info.TimestampColumn = tsCol.String
		}

		// Find primary key
		pkQuery := `
			SELECT kcu.column_name, c.data_type
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
				ON tc.constraint_name = kcu.constraint_name
				AND tc.table_schema = kcu.table_schema
			JOIN information_schema.columns c
				ON c.table_schema = kcu.table_schema
				AND c.table_name = kcu.table_name
				AND c.column_name = kcu.column_name
			WHERE tc.constraint_type = 'PRIMARY KEY'
			  AND tc.table_schema = $1
			  AND tc.table_name = $2
			LIMIT 1
		`
		var pkCol, pkType sql.NullString
		_ = db.QueryRowContext(ctx, pkQuery, t.schema, t.name).Scan(&pkCol, &pkType)
		if pkCol.Valid {
			info.PrimaryKey = pkCol.String
			info.PrimaryKeyType = pkType.String
		}

		result[fullName] = info
	}

	return result, nil
}

// GetMaxTimestamp returns the maximum value of a timestamp column in a table
func GetMaxTimestamp(connStr string, schema string, table string, column string) (*time.Time, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	query := fmt.Sprintf("SELECT MAX(%s) FROM %s.%s",
		column,
		quoteIdentifier(schema),
		quoteIdentifier(table))

	var maxTs sql.NullTime
	if err := db.QueryRowContext(ctx, query).Scan(&maxTs); err != nil {
		return nil, err
	}

	if !maxTs.Valid {
		return nil, nil
	}
	return &maxTs.Time, nil
}

// GetMaxPrimaryKey returns the maximum value of an integer primary key
func GetMaxPrimaryKey(connStr string, schema string, table string, column string) (*int64, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	query := fmt.Sprintf("SELECT MAX(%s) FROM %s.%s",
		column,
		quoteIdentifier(schema),
		quoteIdentifier(table))

	var maxPK sql.NullInt64
	if err := db.QueryRowContext(ctx, query).Scan(&maxPK); err != nil {
		return nil, err
	}

	if !maxPK.Valid {
		return nil, nil
	}
	return &maxPK.Int64, nil
}

// GetSchemaSnapshot returns a snapshot of all table schemas for diff detection
func GetSchemaSnapshot(connStr string) (map[string]*TableSchema, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Get all table columns
	query := `
		SELECT
			c.table_schema,
			c.table_name,
			c.column_name,
			c.data_type,
			c.is_nullable = 'YES' as is_nullable,
			COALESCE(c.column_default, '') as column_default
		FROM information_schema.columns c
		JOIN information_schema.tables t
			ON c.table_schema = t.table_schema AND c.table_name = t.table_name
		WHERE c.table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_schema, c.table_name, c.ordinal_position
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]*TableSchema)
	for rows.Next() {
		var schema, table, colName, dataType, colDefault string
		var isNullable bool
		if err := rows.Scan(&schema, &table, &colName, &dataType, &isNullable, &colDefault); err != nil {
			return nil, err
		}

		fullName := schema + "." + table
		if result[fullName] == nil {
			result[fullName] = &TableSchema{
				Name:    table,
				Schema:  schema,
				Columns: []ColumnSchema{},
			}
		}
		result[fullName].Columns = append(result[fullName].Columns, ColumnSchema{
			Name:         colName,
			DataType:     dataType,
			IsNullable:   isNullable,
			DefaultValue: colDefault,
		})
	}

	return result, rows.Err()
}

// ComputeSchemaDiff compares two schema snapshots and returns the differences
func ComputeSchemaDiff(oldSchema, newSchema map[string]*TableSchema) *SchemaDiff {
	diff := &SchemaDiff{}

	// Find new tables (in new but not in old)
	for tableName := range newSchema {
		if _, exists := oldSchema[tableName]; !exists {
			diff.NewTables = append(diff.NewTables, tableName)
		}
	}

	// Find removed tables (in old but not in new)
	for tableName := range oldSchema {
		if _, exists := newSchema[tableName]; !exists {
			diff.RemovedTables = append(diff.RemovedTables, tableName)
		}
	}

	// Find modified tables (columns changed)
	for tableName, newTable := range newSchema {
		oldTable, exists := oldSchema[tableName]
		if !exists {
			continue // Already counted as new table
		}

		if !columnsEqual(oldTable.Columns, newTable.Columns) {
			diff.ModifiedTables = append(diff.ModifiedTables, tableName)
		}
	}

	diff.HasChanges = len(diff.NewTables) > 0 || len(diff.RemovedTables) > 0 || len(diff.ModifiedTables) > 0
	return diff
}

// columnsEqual compares two column slices for equality
func columnsEqual(a, b []ColumnSchema) bool {
	if len(a) != len(b) {
		return false
	}

	// Build map of columns for comparison
	aMap := make(map[string]ColumnSchema)
	for _, col := range a {
		aMap[col.Name] = col
	}

	for _, col := range b {
		aCol, exists := aMap[col.Name]
		if !exists {
			return false // Column doesn't exist in a
		}
		if aCol.DataType != col.DataType || aCol.IsNullable != col.IsNullable {
			return false // Column type or nullability changed
		}
	}

	return true
}

// GetTableList returns a list of all table names in the database
func GetTableList(connStr string) ([]string, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	query := `
		SELECT table_schema || '.' || table_name
		FROM information_schema.tables
		WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND table_type = 'BASE TABLE'
		ORDER BY table_schema, table_name
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}

	return tables, rows.Err()
}

// GetForeignKeys returns all FK relationships for given tables
func GetForeignKeys(connStr string, tables []string) ([]ForeignKeyInfo, error) {
	if len(tables) == 0 {
		return nil, nil
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query FK relationships from information_schema
	query := `
		SELECT
			tc.table_schema,
			tc.table_name,
			kcu.column_name,
			ccu.table_schema AS referenced_schema,
			ccu.table_name AS referenced_table,
			ccu.column_name AS referenced_column,
			tc.constraint_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.table_schema
		JOIN information_schema.constraint_column_usage ccu
			ON tc.constraint_name = ccu.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema || '.' || tc.table_name = ANY($1)
	`

	rows, err := db.QueryContext(ctx, query, pq.Array(tables))
	if err != nil {
		return nil, fmt.Errorf("failed to query foreign keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var fks []ForeignKeyInfo
	for rows.Next() {
		var fk ForeignKeyInfo
		if err := rows.Scan(
			&fk.TableSchema,
			&fk.TableName,
			&fk.ColumnName,
			&fk.ReferencedSchema,
			&fk.ReferencedTable,
			&fk.ReferencedColumn,
			&fk.ConstraintName,
		); err != nil {
			return nil, fmt.Errorf("failed to scan FK row: %w", err)
		}
		fks = append(fks, fk)
	}

	return fks, rows.Err()
}

// GetTableIndexes returns index info for specified tables
func GetTableIndexes(connStr string, tables []string) (map[string][]IndexInfo, error) {
	if len(tables) == 0 {
		return make(map[string][]IndexInfo), nil
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query indexes from pg_indexes
	query := `
		SELECT
			schemaname,
			tablename,
			indexname,
			indexdef
		FROM pg_indexes
		WHERE schemaname || '.' || tablename = ANY($1)
	`

	rows, err := db.QueryContext(ctx, query, pq.Array(tables))
	if err != nil {
		return nil, fmt.Errorf("failed to query indexes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string][]IndexInfo)
	for rows.Next() {
		var schema, table, indexName, indexDef string
		if err := rows.Scan(&schema, &table, &indexName, &indexDef); err != nil {
			return nil, fmt.Errorf("failed to scan index row: %w", err)
		}

		fullName := schema + "." + table
		idx := IndexInfo{
			Schema:    schema,
			Table:     table,
			IndexName: indexName,
			Columns:   parseIndexColumns(indexDef),
			IsUnique:  strings.Contains(indexDef, "UNIQUE"),
			IsPrimary: strings.Contains(indexName, "_pkey"),
		}
		result[fullName] = append(result[fullName], idx)
	}

	return result, rows.Err()
}

// parseIndexColumns extracts column names from an index definition
// Example: "CREATE INDEX idx_name ON public.table USING btree (col1, col2)"
func parseIndexColumns(indexDef string) []string {
	// Find the content within the last parentheses
	lastOpen := strings.LastIndex(indexDef, "(")
	lastClose := strings.LastIndex(indexDef, ")")
	if lastOpen == -1 || lastClose == -1 || lastClose <= lastOpen {
		return nil
	}

	columnsPart := indexDef[lastOpen+1 : lastClose]
	// Split by comma and clean up
	parts := strings.Split(columnsPart, ",")
	var columns []string
	for _, part := range parts {
		col := strings.TrimSpace(part)
		// Remove any ASC/DESC or other modifiers
		if spaceIdx := strings.Index(col, " "); spaceIdx != -1 {
			col = col[:spaceIdx]
		}
		// Remove quotes if present
		col = strings.Trim(col, `"`)
		if col != "" {
			columns = append(columns, col)
		}
	}
	return columns
}

