package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSortTablesByFKDependency(t *testing.T) {
	tests := []struct {
		name     string
		tables   []string
		fks      []ForeignKeyInfo
		expected []string // Expected order (parents before children)
	}{
		{
			name:     "empty tables",
			tables:   []string{},
			fks:      nil,
			expected: []string{},
		},
		{
			name:     "no dependencies",
			tables:   []string{"public.users", "public.products", "public.categories"},
			fks:      nil,
			expected: []string{"public.categories", "public.products", "public.users"}, // Alphabetical when no deps
		},
		{
			name:   "simple dependency: orders -> order_items",
			tables: []string{"public.orders", "public.order_items"},
			fks: []ForeignKeyInfo{
				{
					TableSchema:      "public",
					TableName:        "order_items",
					ColumnName:       "order_id",
					ReferencedSchema: "public",
					ReferencedTable:  "orders",
					ReferencedColumn: "id",
					ConstraintName:   "fk_order_items_orders",
				},
			},
			expected: []string{"public.orders", "public.order_items"}, // orders must come first
		},
		{
			name:   "chain dependency: A -> B -> C",
			tables: []string{"public.a", "public.b", "public.c"},
			fks: []ForeignKeyInfo{
				{
					TableSchema:      "public",
					TableName:        "b",
					ColumnName:       "a_id",
					ReferencedSchema: "public",
					ReferencedTable:  "a",
					ReferencedColumn: "id",
					ConstraintName:   "fk_b_a",
				},
				{
					TableSchema:      "public",
					TableName:        "c",
					ColumnName:       "b_id",
					ReferencedSchema: "public",
					ReferencedTable:  "b",
					ReferencedColumn: "id",
					ConstraintName:   "fk_c_b",
				},
			},
			expected: []string{"public.a", "public.b", "public.c"},
		},
		{
			name:   "diamond dependency: A -> B, A -> C, B -> D, C -> D",
			tables: []string{"public.a", "public.b", "public.c", "public.d"},
			fks: []ForeignKeyInfo{
				{
					TableSchema:      "public",
					TableName:        "b",
					ColumnName:       "a_id",
					ReferencedSchema: "public",
					ReferencedTable:  "a",
					ReferencedColumn: "id",
				},
				{
					TableSchema:      "public",
					TableName:        "c",
					ColumnName:       "a_id",
					ReferencedSchema: "public",
					ReferencedTable:  "a",
					ReferencedColumn: "id",
				},
				{
					TableSchema:      "public",
					TableName:        "d",
					ColumnName:       "b_id",
					ReferencedSchema: "public",
					ReferencedTable:  "b",
					ReferencedColumn: "id",
				},
				{
					TableSchema:      "public",
					TableName:        "d",
					ColumnName:       "c_id",
					ReferencedSchema: "public",
					ReferencedTable:  "c",
					ReferencedColumn: "id",
				},
			},
			expected: []string{"public.a", "public.b", "public.c", "public.d"},
		},
		{
			name:   "self-referencing FK (should be ignored)",
			tables: []string{"public.categories"},
			fks: []ForeignKeyInfo{
				{
					TableSchema:      "public",
					TableName:        "categories",
					ColumnName:       "parent_id",
					ReferencedSchema: "public",
					ReferencedTable:  "categories",
					ReferencedColumn: "id",
				},
			},
			expected: []string{"public.categories"},
		},
		{
			name:   "FK to table not in list (should be ignored)",
			tables: []string{"public.order_items"},
			fks: []ForeignKeyInfo{
				{
					TableSchema:      "public",
					TableName:        "order_items",
					ColumnName:       "order_id",
					ReferencedSchema: "public",
					ReferencedTable:  "orders", // Not in tables list
					ReferencedColumn: "id",
				},
			},
			expected: []string{"public.order_items"},
		},
		{
			name:   "circular dependency (should not infinite loop)",
			tables: []string{"public.a", "public.b"},
			fks: []ForeignKeyInfo{
				{
					TableSchema:      "public",
					TableName:        "a",
					ColumnName:       "b_id",
					ReferencedSchema: "public",
					ReferencedTable:  "b",
					ReferencedColumn: "id",
				},
				{
					TableSchema:      "public",
					TableName:        "b",
					ColumnName:       "a_id",
					ReferencedSchema: "public",
					ReferencedTable:  "a",
					ReferencedColumn: "id",
				},
			},
			// With a cycle, both have in-degree > 0, so they'll be added as "remaining"
			expected: []string{"public.a", "public.b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sortTablesByFKDependency(tt.tables, tt.fks)
			assert.Equal(t, len(tt.expected), len(result), "result length should match expected")

			// For dependency tests, verify parent comes before child
			if tt.name == "simple dependency: orders -> order_items" {
				ordersIdx := indexOf(result, "public.orders")
				orderItemsIdx := indexOf(result, "public.order_items")
				assert.True(t, ordersIdx < orderItemsIdx, "orders should come before order_items")
			}

			if tt.name == "chain dependency: A -> B -> C" {
				aIdx := indexOf(result, "public.a")
				bIdx := indexOf(result, "public.b")
				cIdx := indexOf(result, "public.c")
				assert.True(t, aIdx < bIdx, "a should come before b")
				assert.True(t, bIdx < cIdx, "b should come before c")
			}

			if tt.name == "diamond dependency: A -> B, A -> C, B -> D, C -> D" {
				aIdx := indexOf(result, "public.a")
				bIdx := indexOf(result, "public.b")
				cIdx := indexOf(result, "public.c")
				dIdx := indexOf(result, "public.d")
				assert.True(t, aIdx < bIdx, "a should come before b")
				assert.True(t, aIdx < cIdx, "a should come before c")
				assert.True(t, bIdx < dIdx, "b should come before d")
				assert.True(t, cIdx < dIdx, "c should come before d")
			}
		})
	}
}

func TestValidateFilterIndexes(t *testing.T) {
	tests := []struct {
		name         string
		filterTables map[string]string
		indexes      map[string][]IndexInfo
		wantWarnings int
	}{
		{
			name:         "empty filters",
			filterTables: map[string]string{},
			indexes:      map[string][]IndexInfo{},
			wantWarnings: 0,
		},
		{
			name: "filter on indexed column",
			filterTables: map[string]string{
				"public.orders": "created_at > NOW() - INTERVAL '7 days'",
			},
			indexes: map[string][]IndexInfo{
				"public.orders": {
					{
						Schema:    "public",
						Table:     "orders",
						IndexName: "idx_orders_created_at",
						Columns:   []string{"created_at"},
						IsUnique:  false,
						IsPrimary: false,
					},
				},
			},
			wantWarnings: 0,
		},
		{
			name: "filter on non-indexed column",
			filterTables: map[string]string{
				"public.orders": "status = 'active'",
			},
			indexes: map[string][]IndexInfo{
				"public.orders": {
					{
						Schema:    "public",
						Table:     "orders",
						IndexName: "idx_orders_created_at",
						Columns:   []string{"created_at"},
						IsUnique:  false,
						IsPrimary: false,
					},
				},
			},
			wantWarnings: 1,
		},
		{
			name: "multiple filters, some indexed",
			filterTables: map[string]string{
				"public.orders":      "created_at > NOW() - INTERVAL '7 days'",
				"public.order_items": "status = 'pending'",
			},
			indexes: map[string][]IndexInfo{
				"public.orders": {
					{
						Schema:    "public",
						Table:     "orders",
						IndexName: "idx_orders_created_at",
						Columns:   []string{"created_at"},
					},
				},
				"public.order_items": {}, // No indexes
			},
			wantWarnings: 1, // status not indexed
		},
		{
			name: "filter with IN clause on indexed column",
			filterTables: map[string]string{
				"public.order_items": "order_id IN (1, 2, 3)",
			},
			indexes: map[string][]IndexInfo{
				"public.order_items": {
					{
						Schema:    "public",
						Table:     "order_items",
						IndexName: "idx_order_items_order_id",
						Columns:   []string{"order_id"},
					},
				},
			},
			wantWarnings: 0, // order_id is indexed
		},
		{
			name: "filter on primary key",
			filterTables: map[string]string{
				"public.users": "id > 1000",
			},
			indexes: map[string][]IndexInfo{
				"public.users": {
					{
						Schema:    "public",
						Table:     "users",
						IndexName: "users_pkey",
						Columns:   []string{"id"},
						IsPrimary: true,
					},
				},
			},
			wantWarnings: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := validateFilterIndexes(tt.filterTables, tt.indexes)
			assert.Equal(t, tt.wantWarnings, len(warnings), "unexpected number of warnings: %v", warnings)
		})
	}
}

func TestExtractColumnsFromWhere(t *testing.T) {
	tests := []struct {
		name        string
		whereClause string
		expected    []string
	}{
		{
			name:        "simple comparison",
			whereClause: "created_at > NOW()",
			expected:    []string{"created_at"},
		},
		{
			name:        "equality",
			whereClause: "status = 'active'",
			expected:    []string{"status"},
		},
		{
			name:        "IN clause",
			whereClause: "order_id IN (SELECT id FROM orders)",
			expected:    []string{"order_id"}, // Only extracts the main filter column, not subquery columns
		},
		{
			name:        "multiple conditions",
			whereClause: "created_at > '2024-01-01' AND status = 'active'",
			expected:    []string{"created_at", "status"},
		},
		{
			name:        "BETWEEN",
			whereClause: "amount BETWEEN 100 AND 500",
			expected:    []string{"amount"},
		},
		{
			name:        "complex condition",
			whereClause: "created_at > NOW() - INTERVAL '7 days' AND user_id = 123",
			expected:    []string{"created_at", "user_id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractColumnsFromWhere(tt.whereClause)
			for _, exp := range tt.expected {
				assert.Contains(t, result, exp, "expected column %s not found in %v", exp, result)
			}
		})
	}
}

func TestParseIndexColumns(t *testing.T) {
	tests := []struct {
		name     string
		indexDef string
		expected []string
	}{
		{
			name:     "single column",
			indexDef: "CREATE INDEX idx_orders_created_at ON public.orders USING btree (created_at)",
			expected: []string{"created_at"},
		},
		{
			name:     "multiple columns",
			indexDef: "CREATE INDEX idx_orders_user_created ON public.orders USING btree (user_id, created_at)",
			expected: []string{"user_id", "created_at"},
		},
		{
			name:     "with ASC/DESC",
			indexDef: "CREATE INDEX idx_orders_created_at ON public.orders USING btree (created_at DESC)",
			expected: []string{"created_at"},
		},
		{
			name:     "quoted column",
			indexDef: `CREATE INDEX idx_test ON public.test USING btree ("Column")`,
			expected: []string{"Column"},
		},
		{
			name:     "unique index",
			indexDef: "CREATE UNIQUE INDEX users_email_key ON public.users USING btree (email)",
			expected: []string{"email"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseIndexColumns(tt.indexDef)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function to find index of element in slice
func indexOf(slice []string, item string) int {
	for i, s := range slice {
		if s == item {
			return i
		}
	}
	return -1
}
