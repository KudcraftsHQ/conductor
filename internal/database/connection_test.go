package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		connStr  string
		expected *ConnectionInfo
		wantErr  bool
	}{
		{
			name:    "full connection string",
			connStr: "postgresql://user:pass@localhost:5432/mydb",
			expected: &ConnectionInfo{
				Host:     "localhost",
				Port:     "5432",
				User:     "user",
				Password: "pass",
				Database: "mydb",
			},
		},
		{
			name:    "without password",
			connStr: "postgresql://user@localhost:5432/mydb",
			expected: &ConnectionInfo{
				Host:     "localhost",
				Port:     "5432",
				User:     "user",
				Password: "",
				Database: "mydb",
			},
		},
		{
			name:    "default port",
			connStr: "postgresql://user:pass@localhost/mydb",
			expected: &ConnectionInfo{
				Host:     "localhost",
				Port:     "5432",
				User:     "user",
				Password: "pass",
				Database: "mydb",
			},
		},
		{
			name:    "with ssl mode",
			connStr: "postgresql://user:pass@localhost:5432/mydb?sslmode=require",
			expected: &ConnectionInfo{
				Host:     "localhost",
				Port:     "5432",
				User:     "user",
				Password: "pass",
				Database: "mydb",
				SSLMode:  "require",
			},
		},
		{
			name:    "postgres scheme",
			connStr: "postgres://user:pass@localhost:5432/mydb",
			expected: &ConnectionInfo{
				Host:     "localhost",
				Port:     "5432",
				User:     "user",
				Password: "pass",
				Database: "mydb",
			},
		},
		{
			name:    "invalid scheme",
			connStr: "mysql://user:pass@localhost:5432/mydb",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseConnectionString(tt.connStr)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected.Host, info.Host)
			assert.Equal(t, tt.expected.Port, info.Port)
			assert.Equal(t, tt.expected.User, info.User)
			assert.Equal(t, tt.expected.Password, info.Password)
			assert.Equal(t, tt.expected.Database, info.Database)
			assert.Equal(t, tt.expected.SSLMode, info.SSLMode)
		})
	}
}

func TestBuildConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		info     *ConnectionInfo
		expected string
	}{
		{
			name: "full info",
			info: &ConnectionInfo{
				Host:     "localhost",
				Port:     "5432",
				User:     "user",
				Password: "pass",
				Database: "mydb",
			},
			expected: "postgresql://user:pass@localhost:5432/mydb",
		},
		{
			name: "with special characters in password",
			info: &ConnectionInfo{
				Host:     "localhost",
				Port:     "5432",
				User:     "user",
				Password: "p@ss:word",
				Database: "mydb",
			},
			expected: "postgresql://user:p%40ss%3Aword@localhost:5432/mydb",
		},
		{
			name: "with ssl mode",
			info: &ConnectionInfo{
				Host:     "localhost",
				Port:     "5432",
				User:     "user",
				Password: "pass",
				Database: "mydb",
				SSLMode:  "require",
			},
			expected: "postgresql://user:pass@localhost:5432/mydb?sslmode=require",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildConnectionString(tt.info)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildWorktreeURL(t *testing.T) {
	tests := []struct {
		name     string
		localURL string
		dbName   string
		expected string
	}{
		{
			name:     "simple replacement",
			localURL: "postgresql://user:pass@localhost:5432/postgres",
			dbName:   "myapp_3100",
			expected: "postgresql://user:pass@localhost:5432/myapp_3100",
		},
		{
			name:     "with query params",
			localURL: "postgresql://user:pass@localhost:5432/postgres?sslmode=disable",
			dbName:   "myapp_3100",
			expected: "postgresql://user:pass@localhost:5432/myapp_3100?sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildWorktreeURL(tt.localURL, tt.dbName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMaskConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		connStr  string
		expected string
	}{
		{
			name:     "masks password",
			connStr:  "postgresql://user:secretpass@localhost:5432/mydb",
			expected: "postgresql://user:****@localhost:5432/mydb",
		},
		{
			name:     "no password",
			connStr:  "postgresql://user@localhost:5432/mydb",
			expected: "postgresql://user@localhost:5432/mydb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskConnectionString(tt.connStr)
			assert.Equal(t, tt.expected, result)
		})
	}
}
