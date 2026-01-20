package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// ParseConnectionString parses a PostgreSQL connection URL into components
func ParseConnectionString(connStr string) (*ConnectionInfo, error) {
	u, err := url.Parse(connStr)
	if err != nil {
		return nil, fmt.Errorf("invalid connection string: %w", err)
	}

	if u.Scheme != "postgresql" && u.Scheme != "postgres" {
		return nil, fmt.Errorf("invalid scheme: expected postgresql or postgres, got %s", u.Scheme)
	}

	info := &ConnectionInfo{
		Host: u.Hostname(),
		Port: u.Port(),
	}

	if info.Port == "" {
		info.Port = "5432"
	}

	if u.User != nil {
		info.User = u.User.Username()
		info.Password, _ = u.User.Password()
	}

	// Database name is the path without leading slash
	info.Database = strings.TrimPrefix(u.Path, "/")

	// Parse SSL mode from query params
	if sslMode := u.Query().Get("sslmode"); sslMode != "" {
		info.SSLMode = sslMode
	}

	return info, nil
}

// BuildConnectionString builds a connection string from components
func BuildConnectionString(info *ConnectionInfo) string {
	var userPart string
	if info.User != "" {
		if info.Password != "" {
			userPart = fmt.Sprintf("%s:%s@", url.QueryEscape(info.User), url.QueryEscape(info.Password))
		} else {
			userPart = fmt.Sprintf("%s@", url.QueryEscape(info.User))
		}
	}

	port := info.Port
	if port == "" {
		port = "5432"
	}

	connStr := fmt.Sprintf("postgresql://%s%s:%s/%s", userPart, info.Host, port, info.Database)

	if info.SSLMode != "" {
		connStr += "?sslmode=" + info.SSLMode
	}

	return connStr
}

// BuildWorktreeURL creates a connection URL for a worktree database
func BuildWorktreeURL(localURL, dbName string) string {
	info, err := ParseConnectionString(localURL)
	if err != nil {
		// Fallback: just append database name
		return strings.TrimSuffix(localURL, "/") + "/" + dbName
	}

	info.Database = dbName
	return BuildConnectionString(info)
}

// ValidateConnection tests if a connection string is valid and can connect
func ValidateConnection(connStr string) error {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	return nil
}

// IsReadOnly checks if the connection has write permissions
// Returns true if read-only, false if has write access
func IsReadOnly(connStr string) (bool, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return false, fmt.Errorf("failed to open connection: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to create a temporary table - this will fail if read-only
	_, err = db.ExecContext(ctx, `
		CREATE TEMP TABLE _conductor_write_test (id int);
		DROP TABLE _conductor_write_test;
	`)

	if err != nil {
		// Check if error indicates read-only
		errStr := err.Error()
		if strings.Contains(errStr, "read-only") ||
			strings.Contains(errStr, "permission denied") ||
			strings.Contains(errStr, "cannot execute") {
			return true, nil
		}
		// Other error - might still be read-only, assume it is
		return true, nil
	}

	return false, nil
}

// GetPostgresVersion returns the PostgreSQL server version
func GetPostgresVersion(connStr string) (string, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return "", fmt.Errorf("failed to open connection: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var version string
	err = db.QueryRowContext(ctx, "SELECT version()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("failed to get version: %w", err)
	}

	return version, nil
}

// GetDatabaseName extracts the database name from a connection string
func GetDatabaseName(connStr string) (string, error) {
	info, err := ParseConnectionString(connStr)
	if err != nil {
		return "", err
	}
	return info.Database, nil
}

// CheckPgDumpAvailable verifies pg_dump is installed and returns its version
func CheckPgDumpAvailable() (string, error) {
	cmd := exec.Command("pg_dump", "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pg_dump not found: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// CheckPsqlAvailable verifies psql is installed and returns its version
func CheckPsqlAvailable() (string, error) {
	cmd := exec.Command("psql", "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("psql not found: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// MaskConnectionString returns a connection string with password masked
func MaskConnectionString(connStr string) string {
	info, err := ParseConnectionString(connStr)
	if err != nil {
		return connStr
	}

	// Build manually to avoid URL encoding the asterisks
	var userPart string
	if info.User != "" {
		if info.Password != "" {
			userPart = fmt.Sprintf("%s:****@", info.User)
		} else {
			userPart = fmt.Sprintf("%s@", info.User)
		}
	}

	port := info.Port
	if port == "" {
		port = "5432"
	}

	result := fmt.Sprintf("postgresql://%s%s:%s/%s", userPart, info.Host, port, info.Database)

	if info.SSLMode != "" {
		result += "?sslmode=" + info.SSLMode
	}

	return result
}
