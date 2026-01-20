package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
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
