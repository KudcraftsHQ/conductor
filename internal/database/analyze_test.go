package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLikelyAuditTable(t *testing.T) {
	tests := []struct {
		name      string
		tableName string
		rowCount  int64
		sizeBytes int64
		expected  bool
	}{
		{
			name:      "audit table by name",
			tableName: "user_audit",
			rowCount:  1000,
			sizeBytes: 100000,
			expected:  true,
		},
		{
			name:      "log table by name",
			tableName: "application_logs",
			rowCount:  1000,
			sizeBytes: 100000,
			expected:  true,
		},
		{
			name:      "events table by name",
			tableName: "user_events",
			rowCount:  1000,
			sizeBytes: 100000,
			expected:  true,
		},
		{
			name:      "history table by name",
			tableName: "order_history",
			rowCount:  1000,
			sizeBytes: 100000,
			expected:  true,
		},
		{
			name:      "regular table",
			tableName: "users",
			rowCount:  1000,
			sizeBytes: 100000,
			expected:  false,
		},
		{
			name:      "high row count small size",
			tableName: "metrics",
			rowCount:  2000000,
			sizeBytes: 500000000, // 500MB / 2M rows = 250 bytes/row
			expected:  true,
		},
		{
			name:      "high row count normal size",
			tableName: "products",
			rowCount:  1000000,
			sizeBytes: 1000000000, // 1GB / 1M rows = 1000 bytes/row
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLikelyAuditTable(tt.tableName, tt.rowCount, tt.sizeBytes)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSuggestExclusions(t *testing.T) {
	tables := []TableInfo{
		{Schema: "public", Name: "users", SizeBytes: 10 * 1024 * 1024, RowCount: 1000},           // 10MB
		{Schema: "public", Name: "audit_logs", SizeBytes: 50 * 1024 * 1024, RowCount: 500000},   // 50MB, audit
		{Schema: "public", Name: "large_table", SizeBytes: 200 * 1024 * 1024, RowCount: 100000}, // 200MB
		{Schema: "public", Name: "events", SizeBytes: 30 * 1024 * 1024, RowCount: 200000},       // 30MB, events
	}

	tests := []struct {
		name        string
		thresholdMB int
		expected    []string
	}{
		{
			name:        "100MB threshold",
			thresholdMB: 100,
			expected:    []string{"public.large_table", "public.audit_logs", "public.events"},
		},
		{
			name:        "25MB threshold",
			thresholdMB: 25,
			expected:    []string{"public.large_table", "public.audit_logs", "public.events"},
		},
		{
			name:        "no threshold, just heuristics",
			thresholdMB: 0,
			expected:    []string{"public.audit_logs", "public.events"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set IsAudit based on name heuristics
			for i := range tables {
				tables[i].IsAudit = isLikelyAuditTable(tables[i].Name, tables[i].RowCount, tables[i].SizeBytes)
			}

			result := SuggestExclusions(tables, tt.thresholdMB)
			// Check that expected tables are in result (order may vary)
			for _, expected := range tt.expected {
				assert.Contains(t, result, expected)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{
			name:     "bytes",
			bytes:    500,
			expected: "500 B",
		},
		{
			name:     "kilobytes",
			bytes:    1024,
			expected: "1 KB",
		},
		{
			name:     "megabytes",
			bytes:    1024 * 1024,
			expected: "1 MB",
		},
		{
			name:     "gigabytes",
			bytes:    1024 * 1024 * 1024,
			expected: "1 GB",
		},
		{
			name:     "fractional megabytes",
			bytes:    1536 * 1024, // 1.5 MB
			expected: "1.5 MB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatSize(tt.bytes)
			assert.Equal(t, tt.expected, result)
		})
	}
}
