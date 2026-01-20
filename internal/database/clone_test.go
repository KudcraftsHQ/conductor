package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateDBName(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		port        int
		pattern     string
		expected    string
	}{
		{
			name:        "default pattern",
			projectName: "myapp",
			port:        3100,
			pattern:     "",
			expected:    "myapp_3100",
		},
		{
			name:        "explicit pattern",
			projectName: "myapp",
			port:        3100,
			pattern:     "{project}-{port}",
			expected:    "myapp_3100",
		},
		{
			name:        "project only pattern",
			projectName: "myapp",
			port:        3100,
			pattern:     "{project}_dev",
			expected:    "myapp_dev",
		},
		{
			name:        "sanitizes special chars",
			projectName: "my-app",
			port:        3100,
			pattern:     "{project}-{port}",
			expected:    "my_app_3100",
		},
		{
			name:        "handles numeric start",
			projectName: "123app",
			port:        3100,
			pattern:     "{project}-{port}",
			expected:    "_123app_3100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateDBName(tt.projectName, tt.port, tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeDBName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "already valid",
			input:    "myapp_db",
			expected: "myapp_db",
		},
		{
			name:     "with dashes",
			input:    "my-app-db",
			expected: "my_app_db",
		},
		{
			name:     "uppercase to lowercase",
			input:    "MyApp_DB",
			expected: "myapp_db",
		},
		{
			name:     "starts with number",
			input:    "123app",
			expected: "_123app",
		},
		{
			name:     "special characters",
			input:    "my@app!db",
			expected: "my_app_db",
		},
		{
			name:     "truncates long names",
			input:    "this_is_a_very_long_database_name_that_exceeds_postgresql_limit_of_63_characters_abcdef",
			expected: "this_is_a_very_long_database_name_that_exceeds_postgresql_limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeDBName(tt.input)
			assert.Equal(t, tt.expected, result)
			assert.LessOrEqual(t, len(result), 63, "should not exceed 63 characters")
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple name",
			input:    "mydb",
			expected: `"mydb"`,
		},
		{
			name:     "with underscore",
			input:    "my_db",
			expected: `"my_db"`,
		},
		{
			name:     "escapes quotes",
			input:    `my"db`,
			expected: `"my""db"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := quoteIdentifier(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
