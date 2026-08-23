package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateRemoteDBName(t *testing.T) {
	tests := []struct {
		name         string
		projectName  string
		worktreeName string
		expected     string
	}{
		{
			name:         "simple lowercase",
			projectName:  "facemap",
			worktreeName: "tokyo",
			expected:     "dev_tokyo",
		},
		{
			name:         "with hyphen",
			projectName:  "facemap",
			worktreeName: "new-york",
			expected:     "dev_new_york",
		},
		{
			name:         "uppercase converted",
			projectName:  "facemap",
			worktreeName: "Paris",
			expected:     "dev_paris",
		},
		{
			name:         "with underscore",
			projectName:  "facemap",
			worktreeName: "san_francisco",
			expected:     "dev_san_francisco",
		},
		{
			name:         "special chars removed",
			projectName:  "facemap",
			worktreeName: "city@123!",
			expected:     "dev_city123",
		},
		{
			// Every project's root worktree is called "root", and they all
			// share one dev server, so the name has to carry the project.
			name:         "root is qualified by project",
			projectName:  "facemap",
			worktreeName: "root",
			expected:     "dev_facemap_root",
		},
		{
			name:         "root project name is sanitized too",
			projectName:  "abat-payment-backend",
			worktreeName: "root",
			expected:     "dev_abat_payment_backend_root",
		},
		{
			name:         "root without a project name falls back",
			projectName:  "",
			worktreeName: "root",
			expected:     "dev_root",
		},
		{
			// A city called "rooted" is not a root worktree.
			name:         "only an exact root match is qualified",
			projectName:  "facemap",
			worktreeName: "rooted",
			expected:     "dev_rooted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateRemoteDBName(tt.projectName, tt.worktreeName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildRemoteWorktreeURL(t *testing.T) {
	tests := []struct {
		name           string
		devURLExternal string
		devURL         string
		dbName         string
		expected       string
	}{
		{
			name:           "uses external URL when provided",
			devURLExternal: "postgres://user:pass@external:5432",
			devURL:         "postgres://user:pass@internal:5432",
			dbName:         "dev_tokyo",
			expected:       "postgres://user:pass@external:5432/dev_tokyo?sslmode=disable",
		},
		{
			name:           "falls back to devURL when external not provided",
			devURLExternal: "",
			devURL:         "postgres://user:pass@internal:5432",
			dbName:         "dev_tokyo",
			expected:       "postgres://user:pass@internal:5432/dev_tokyo?sslmode=disable",
		},
		{
			name:           "URL with existing sslmode",
			devURLExternal: "postgres://user:pass@host:5432?sslmode=require",
			devURL:         "",
			dbName:         "dev_tokyo",
			expected:       "postgres://user:pass@host:5432/dev_tokyo?sslmode=require",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildRemoteWorktreeURL(tt.devURLExternal, tt.devURL, tt.dbName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsValidIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid lowercase", "mydb", true},
		{"valid with underscore", "my_db", true},
		{"valid with hyphen", "my-db", true},
		{"valid with numbers", "db123", true},
		{"empty string", "", false},
		{"contains space", "my db", false},
		{"contains special char", "my@db", false},
		{"too long", string(make([]byte, 64)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidIdentifier(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
