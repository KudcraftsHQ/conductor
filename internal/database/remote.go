package database

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// RemoteCloneForWorktree creates a dev database on the remote server via SSH
// Uses pg_dump | psql executed on the server for fast cloning (no network transfer)
// sshHost: SSH connection string (e.g., "root@192.168.1.1")
// cloneURL: source database connection (used on server, should be localhost)
// devURL: dev database connection base (used on server, should be localhost)
func RemoteCloneForWorktree(ctx context.Context, sshHost, cloneURL, devURL, targetDB string, excludeTables []string, progress ProgressFunc) error {
	if sshHost == "" {
		return fmt.Errorf("sshHost is required for remote mode")
	}

	// Parse devURL to build target connection
	devParsed, err := url.Parse(devURL)
	if err != nil {
		return fmt.Errorf("invalid devUrl: %w", err)
	}

	// Build target URL with the new database name
	targetURL := buildDatabaseURL(devParsed, targetDB)

	// Build admin URL for creating the database
	adminURL := buildAdminURL(devParsed)

	if progress != nil {
		progress("Creating database " + targetDB + " via SSH")
	}

	// Step 1: Create the empty database via SSH
	createSQL := fmt.Sprintf(`CREATE DATABASE %q`, targetDB)
	if err := execSSHSQL(ctx, sshHost, adminURL, createSQL); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	if progress != nil {
		progress("Cloning data via pg_dump | psql (server-side)")
	}

	// Step 2: Clone data via SSH using pg_dump | psql
	if err := cloneViaSSH(ctx, sshHost, cloneURL, targetURL, excludeTables); err != nil {
		// Cleanup on failure
		dropSQL := fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, targetDB)
		_ = execSSHSQL(ctx, sshHost, adminURL, dropSQL)
		return fmt.Errorf("clone failed: %w", err)
	}

	if progress != nil {
		progress("Remote database created successfully")
	}

	return nil
}

// execSSHSQL executes a SQL command on the remote server via SSH
func execSSHSQL(ctx context.Context, sshHost, dbURL, sql string) error {
	// Build psql command to run on remote
	psqlCmd := fmt.Sprintf(`psql "%s" -c %q`, dbURL, sql)

	cmd := exec.CommandContext(ctx, "ssh", sshHost, psqlCmd)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}

	return nil
}

// cloneViaSSH clones a database using pg_dump | psql executed on the remote server
func cloneViaSSH(ctx context.Context, sshHost, sourceURL, targetURL string, excludeTables []string) error {
	// Build pg_dump command with exclude options
	// Use pg_dump 17 explicitly to support PostgreSQL 16+ servers
	dumpCmd := fmt.Sprintf(`/usr/lib/postgresql/17/bin/pg_dump "%s" --no-owner --no-acl`, sourceURL)
	for _, table := range excludeTables {
		dumpCmd += fmt.Sprintf(` --exclude-table-data=%q`, table)
	}

	// Build the full command: pg_dump | psql
	fullCmd := fmt.Sprintf(`%s | psql "%s" -q`, dumpCmd, targetURL)

	cmd := exec.CommandContext(ctx, "ssh", sshHost, fullCmd)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}

	return nil
}

// grantDevFullAccess grants conductor_dev full access to all objects in a database
//
//nolint:unused // wired up in a follow-up
func grantDevFullAccess(dbURL string) error {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	grants := []string{
		`GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO conductor_dev`,
		`GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO conductor_dev`,
		`GRANT USAGE, CREATE ON SCHEMA public TO conductor_dev`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO conductor_dev`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO conductor_dev`,
	}

	for _, grant := range grants {
		if _, err := db.Exec(grant); err != nil {
			// Log but continue - some grants might fail if objects don't exist
			continue
		}
	}

	return nil
}

// createRemoteDatabase creates a database on the remote server
//
//nolint:unused // wired up in a follow-up
func createRemoteDatabase(adminURL, dbName string) error {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Check if database already exists
	var exists bool
	err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check database existence: %w", err)
	}

	if exists {
		return fmt.Errorf("database %s already exists", dbName)
	}

	// Create the database
	// Note: CREATE DATABASE cannot be parameterized, so we use Sprintf with validation
	if !isValidIdentifier(dbName) {
		return fmt.Errorf("invalid database name: %s", dbName)
	}

	_, err = db.Exec(fmt.Sprintf(`CREATE DATABASE %q`, dbName))
	if err != nil {
		return fmt.Errorf("CREATE DATABASE failed: %w", err)
	}

	return nil
}

// createRemoteDatabaseFromTemplate creates a database as a copy of another (server-side clone)
// This is FAST because all data stays on the server - no network transfer
// Requires the template database to have IS_TEMPLATE = true
//
//nolint:unused // wired up in a follow-up
func createRemoteDatabaseFromTemplate(adminURL, dbName, templateDB string) error {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Check if database already exists
	var exists bool
	err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check database existence: %w", err)
	}

	if exists {
		return fmt.Errorf("database %s already exists", dbName)
	}

	// Validate identifiers
	if !isValidIdentifier(dbName) {
		return fmt.Errorf("invalid database name: %s", dbName)
	}
	if !isValidIdentifier(templateDB) {
		return fmt.Errorf("invalid template database name: %s", templateDB)
	}

	// Terminate any existing connections to the template database
	// CREATE DATABASE WITH TEMPLATE requires no other connections
	// Note: We can't set CONNECTION LIMIT without being owner, so we just terminate and clone quickly
	_, _ = db.Exec(`
		SELECT pg_terminate_backend(pg_stat_activity.pid)
		FROM pg_stat_activity
		WHERE pg_stat_activity.datname = $1
		AND pid <> pg_backend_pid()
	`, templateDB)

	// Create the database as a copy of the template
	// This copies all data server-side - very fast!
	_, err = db.Exec(fmt.Sprintf(`CREATE DATABASE %q WITH TEMPLATE %q`, dbName, templateDB))
	if err != nil {
		return fmt.Errorf("CREATE DATABASE WITH TEMPLATE failed: %w", err)
	}

	return nil
}

// cloneViaPipe uses pg_dump piped to psql for fast cloning
//
//nolint:unused // wired up in a follow-up
func cloneViaPipe(ctx context.Context, sourceURL, targetURL string, excludeTables []string, progress ProgressFunc) error {
	// Build pg_dump command
	dumpArgs := []string{
		sourceURL,
		"--no-owner",
		"--no-acl",
	}

	// Add exclude table data options
	for _, table := range excludeTables {
		dumpArgs = append(dumpArgs, "--exclude-table-data="+table)
	}

	dumpCmd := exec.CommandContext(ctx, "pg_dump", dumpArgs...)

	// Build psql command
	psqlCmd := exec.CommandContext(ctx, "psql", targetURL, "-q")

	// Connect dump stdout to psql stdin
	var errBuf bytes.Buffer
	pipe, err := dumpCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}

	psqlCmd.Stdin = pipe
	psqlCmd.Stderr = &errBuf
	dumpCmd.Stderr = &errBuf

	// Start both commands
	if err := dumpCmd.Start(); err != nil {
		return fmt.Errorf("pg_dump start failed: %w", err)
	}

	if err := psqlCmd.Start(); err != nil {
		_ = dumpCmd.Process.Kill()
		return fmt.Errorf("psql start failed: %w", err)
	}

	// Wait for both to complete
	dumpErr := dumpCmd.Wait()
	psqlErr := psqlCmd.Wait()

	if dumpErr != nil {
		return fmt.Errorf("pg_dump failed: %w\n%s", dumpErr, errBuf.String())
	}

	if psqlErr != nil {
		return fmt.Errorf("psql failed: %w\n%s", psqlErr, errBuf.String())
	}

	return nil
}

// RemoteDropDatabase drops a database on the remote server via SSH
func RemoteDropDatabase(ctx context.Context, sshHost, devURL, dbName string) error {
	if sshHost == "" {
		return fmt.Errorf("sshHost is required for remote mode")
	}

	devParsed, err := url.Parse(devURL)
	if err != nil {
		return fmt.Errorf("invalid devUrl: %w", err)
	}

	adminURL := buildAdminURL(devParsed)

	// Terminate connections first (ignore errors - there may be no connections)
	terminateSQL := fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`, dbName)
	_ = execSSHSQL(ctx, sshHost, adminURL, terminateSQL)

	// Drop database (must be separate command - DROP DATABASE cannot run in transaction)
	dropSQL := fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, dbName)
	return execSSHSQL(ctx, sshHost, adminURL, dropSQL)
}

// RemoteDBExists checks if a database exists on the remote server via SSH
func RemoteDBExists(ctx context.Context, sshHost, devURL, dbName string) (bool, error) {
	if sshHost == "" {
		return false, fmt.Errorf("sshHost is required for remote mode")
	}

	devParsed, err := url.Parse(devURL)
	if err != nil {
		return false, fmt.Errorf("invalid devUrl: %w", err)
	}

	adminURL := buildAdminURL(devParsed)

	// Query to check if database exists
	sql := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = '%s')`, dbName)
	psqlCmd := fmt.Sprintf(`psql "%s" -t -c %q`, adminURL, sql)

	cmd := exec.CommandContext(ctx, "ssh", sshHost, psqlCmd)

	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to check database: %w", err)
	}

	// Output will be " t" or " f"
	return strings.TrimSpace(string(output)) == "t", nil
}

// buildAdminURL builds an admin URL (connecting to postgres database) from a parsed URL
func buildAdminURL(parsed *url.URL) string {
	u := &url.URL{
		Scheme:   parsed.Scheme,
		User:     parsed.User,
		Host:     parsed.Host,
		Path:     "/postgres",
		RawQuery: parsed.RawQuery,
	}
	// Ensure sslmode is set if not present
	if u.RawQuery == "" {
		u.RawQuery = "sslmode=disable"
	}
	return u.String()
}

// buildDatabaseURL builds a URL with a specific database
func buildDatabaseURL(parsed *url.URL, database string) string {
	u := &url.URL{
		Scheme:   parsed.Scheme,
		User:     parsed.User,
		Host:     parsed.Host,
		Path:     "/" + database,
		RawQuery: parsed.RawQuery,
	}
	// Ensure sslmode is set if not present
	if u.RawQuery == "" {
		u.RawQuery = "sslmode=disable"
	}
	return u.String()
}

// isValidIdentifier checks if a string is a valid PostgreSQL identifier
func isValidIdentifier(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	// Only allow alphanumeric, underscore, and hyphen
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// GenerateRemoteDBName generates a database name for remote mode
// Format: dev_{worktree}
func GenerateRemoteDBName(worktreeName string) string {
	// Sanitize worktree name for use in database name
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		if r == '-' {
			return '_'
		}
		return -1
	}, worktreeName)

	return "dev_" + strings.ToLower(safe)
}

// BuildRemoteWorktreeURL builds the full connection URL for a remote worktree database
// Uses devURLExternal if provided (for external access), otherwise falls back to devURL
func BuildRemoteWorktreeURL(devURLExternal, devURL, dbName string) string {
	// Prefer external URL for worktree connections
	baseURL := devURLExternal
	if baseURL == "" {
		baseURL = devURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		// Fallback to simple concatenation
		baseURL = strings.TrimSuffix(baseURL, "/")
		return baseURL + "/" + dbName
	}
	return buildDatabaseURL(parsed, dbName)
}

// GetRemoteAdminURL returns the admin URL (postgres database) from devURL
func GetRemoteAdminURL(devURL string) (string, error) {
	parsed, err := url.Parse(devURL)
	if err != nil {
		return "", fmt.Errorf("invalid devUrl: %w", err)
	}
	return buildAdminURL(parsed), nil
}
