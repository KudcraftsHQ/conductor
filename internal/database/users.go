package database

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"net/url"
	"strings"
)

// UserCredentials contains credentials for a created user
type UserCredentials struct {
	Username string
	Password string
	URL      string
}

// SetupUsersResult contains the results of user setup
type SetupUsersResult struct {
	CloneUser    UserCredentials
	DevUser      UserCredentials
	DatabaseName string
}

// SetupUsers creates conductor_clone and conductor_dev users on the target server
// - conductor_clone: SELECT on source DB, CREATEDB privilege (for cloning)
// - conductor_dev: Full access to dev_* databases only (no production access)
func SetupUsers(adminURL string, sourceDBName string) (*SetupUsersResult, error) {
	// Parse the admin URL to extract host info
	parsed, err := url.Parse(adminURL)
	if err != nil {
		return nil, fmt.Errorf("invalid admin URL: %w", err)
	}

	// Connect to the admin database (usually postgres)
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Generate secure passwords
	clonePassword := GenerateSecurePassword(32)
	devPassword := GenerateSecurePassword(32)

	// Create conductor_clone user
	if err := createCloneUser(db, sourceDBName, clonePassword); err != nil {
		return nil, fmt.Errorf("failed to create clone user: %w", err)
	}

	// Create conductor_dev user
	if err := createDevUser(db, devPassword); err != nil {
		return nil, fmt.Errorf("failed to create dev user: %w", err)
	}

	// Build connection URLs for the new users
	cloneURL := buildUserURL(parsed, "conductor_clone", clonePassword, sourceDBName)
	devURL := buildUserURL(parsed, "conductor_dev", devPassword, "")

	// Grant read permissions on the source database
	// Need to connect to the source database for this
	sourceDBURL := buildUserURL(parsed, parsed.User.Username(), getPassword(parsed), sourceDBName)
	if err := GrantClonePermissions(sourceDBURL); err != nil {
		// Don't fail - just warn, user can do it manually if needed
		fmt.Printf("Warning: could not grant permissions automatically: %v\n", err)
		fmt.Println("You may need to grant permissions manually.")
	}

	// Set IS_TEMPLATE = true on the source database
	// This allows conductor_clone to use CREATE DATABASE WITH TEMPLATE
	if err := SetDatabaseAsTemplate(db, sourceDBName); err != nil {
		fmt.Printf("Warning: could not set IS_TEMPLATE: %v\n", err)
		fmt.Println("You may need to run: ALTER DATABASE \"" + sourceDBName + "\" IS_TEMPLATE true;")
	}

	return &SetupUsersResult{
		CloneUser: UserCredentials{
			Username: "conductor_clone",
			Password: clonePassword,
			URL:      cloneURL,
		},
		DevUser: UserCredentials{
			Username: "conductor_dev",
			Password: devPassword,
			URL:      devURL,
		},
		DatabaseName: sourceDBName,
	}, nil
}

// getPassword extracts password from URL userinfo
func getPassword(u *url.URL) string {
	if u.User == nil {
		return ""
	}
	pass, _ := u.User.Password()
	return pass
}

// createCloneUser creates the conductor_clone user with read-only access and CREATEDB
func createCloneUser(db *sql.DB, sourceDBName, password string) error {
	// Drop existing user if exists (for idempotency)
	_, _ = db.Exec(`DROP OWNED BY conductor_clone CASCADE`)
	_, _ = db.Exec(`DROP USER IF EXISTS conductor_clone`)

	// Create user with CREATEDB privilege
	_, err := db.Exec(fmt.Sprintf(`CREATE USER conductor_clone WITH PASSWORD '%s' CREATEDB`, password))
	if err != nil {
		return fmt.Errorf("CREATE USER failed: %w", err)
	}

	// Grant connect on source database
	_, err = db.Exec(fmt.Sprintf(`GRANT CONNECT ON DATABASE %q TO conductor_clone`, sourceDBName))
	if err != nil {
		return fmt.Errorf("GRANT CONNECT failed: %w", err)
	}

	// Execute grants - these need to be run on the source database
	// For now, we'll create a helper that the user can run
	grants := []string{
		`GRANT USAGE ON SCHEMA public TO conductor_clone`,
		`GRANT SELECT ON ALL TABLES IN SCHEMA public TO conductor_clone`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO conductor_clone`,
	}

	// Try to execute grants (may fail if we're not connected to source DB)
	for _, grant := range grants {
		if _, err := db.Exec(grant); err != nil {
			// Log but don't fail - user may need to run these manually
			fmt.Printf("  Note: Run this on %s: %s\n", sourceDBName, grant)
		}
	}

	return nil
}

// createDevUser creates the conductor_dev user with access to dev_* databases only
func createDevUser(db *sql.DB, password string) error {
	// Drop existing user if exists (for idempotency)
	_, _ = db.Exec(`DROP OWNED BY conductor_dev CASCADE`)
	_, _ = db.Exec(`DROP USER IF EXISTS conductor_dev`)

	// Create user - no special privileges, will get access to dev DBs as they're created
	_, err := db.Exec(fmt.Sprintf(`CREATE USER conductor_dev WITH PASSWORD '%s'`, password))
	if err != nil {
		return fmt.Errorf("CREATE USER failed: %w", err)
	}

	return nil
}

// buildUserURL constructs a connection URL for a user
func buildUserURL(base *url.URL, username, password, database string) string {
	u := &url.URL{
		Scheme: base.Scheme,
		User:   url.UserPassword(username, password),
		Host:   base.Host,
	}
	if database != "" {
		u.Path = "/" + database
	}
	return u.String()
}

// GenerateSecurePassword generates a cryptographically secure random password
func GenerateSecurePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[num.Int64()]
	}
	return string(result)
}

// SetDatabaseAsTemplate sets IS_TEMPLATE = true on a database
// This allows users with CREATEDB privilege to clone it
func SetDatabaseAsTemplate(db *sql.DB, dbName string) error {
	_, err := db.Exec(fmt.Sprintf(`ALTER DATABASE %q IS_TEMPLATE true`, dbName))
	if err != nil {
		return fmt.Errorf("ALTER DATABASE IS_TEMPLATE failed: %w", err)
	}
	return nil
}

// GrantClonePermissions grants read permissions to conductor_clone on the source database
// This should be run while connected to the source database
func GrantClonePermissions(sourceDBURL string) error {
	db, err := sql.Open("postgres", sourceDBURL)
	if err != nil {
		return fmt.Errorf("failed to connect to source database: %w", err)
	}
	defer func() { _ = db.Close() }()

	grants := []string{
		`GRANT USAGE ON SCHEMA public TO conductor_clone`,
		`GRANT SELECT ON ALL TABLES IN SCHEMA public TO conductor_clone`,
		`GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO conductor_clone`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO conductor_clone`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON SEQUENCES TO conductor_clone`,
	}

	for _, grant := range grants {
		if _, err := db.Exec(grant); err != nil {
			return fmt.Errorf("failed to execute %q: %w", grant, err)
		}
	}

	return nil
}

// GrantDevPermissions grants full permissions to conductor_dev on a dev database
// This should be called after creating each dev database
func GrantDevPermissions(adminURL string, devDBName string) error {
	// First, grant connect on the database
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Grant connect
	_, err = db.Exec(fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE %q TO conductor_dev`, devDBName))
	if err != nil {
		return fmt.Errorf("GRANT on database failed: %w", err)
	}

	return nil
}

// ValidateRemoteConfig validates a remote database configuration
func ValidateRemoteConfig(cloneURL, devURL string) error {
	var errors []string

	if cloneURL == "" {
		errors = append(errors, "cloneUrl is required for remote mode")
	}
	if devURL == "" {
		errors = append(errors, "devUrl is required for remote mode")
	}

	// Validate clone URL is accessible
	if cloneURL != "" {
		if err := ValidateConnection(cloneURL); err != nil {
			errors = append(errors, fmt.Sprintf("cloneUrl connection failed: %v", err))
		}
	}

	// Validate dev URL is accessible (connect to postgres database for base validation)
	if devURL != "" {
		testURL := devURL
		if !strings.Contains(devURL, "/") || strings.HasSuffix(devURL, "/") {
			testURL = devURL + "/postgres"
		}
		if err := ValidateConnection(testURL); err != nil {
			errors = append(errors, fmt.Sprintf("devUrl connection failed: %v", err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("remote config validation failed:\n  - %s", strings.Join(errors, "\n  - "))
	}

	return nil
}
