package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDBURL = "postgres://postgres:postgres@localhost:54325/test-conductor?sslmode=disable"

func TestIncrementalSync(t *testing.T) {
	// Skip - this test is for V2 file-based sync, but we now use V3 golden database
	t.Skip("Test needs to be updated for V3 golden database format")

	// Skip if test DB not available
	if err := ValidateConnection(testDBURL); err != nil {
		t.Skipf("Test database not available: %v", err)
	}

	// Create temp directory for sync files
	tempDir, err := os.MkdirTemp("", "conductor-sync-test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tempDir) }()

	cfg := &DatabaseConfig{
		Source: testDBURL,
	}
	projectName := "test-project"

	// Test 1: First sync should be full sync (V2 format)
	t.Run("FirstSyncIsFull", func(t *testing.T) {
		metadata, err := SyncFromSource(cfg, projectName, tempDir)
		require.NoError(t, err)
		assert.NotNil(t, metadata)

		// V2: Check schema.sql was created (not golden.sql)
		schemaPath := filepath.Join(tempDir, projectName, "schema.sql")
		_, err = os.Stat(schemaPath)
		assert.NoError(t, err, "schema.sql should exist")

		// V2: Check data directory exists
		dataDir := filepath.Join(tempDir, projectName, "data")
		_, err = os.Stat(dataDir)
		assert.NoError(t, err, "data directory should exist")

		// V2: Check sync version
		assert.Equal(t, SyncVersionSeparated, metadata.SyncVersion, "should be V2 sync")

		// Check metadata has table sync state
		assert.NotNil(t, metadata.TableSyncState)
		assert.Greater(t, len(metadata.TableSyncState), 0)

		// Verify timestamp columns detected for users table
		usersState, ok := metadata.TableSyncState["public.users"]
		assert.True(t, ok, "users table should have sync state")
		if ok {
			assert.Equal(t, "updated_at", usersState.TimestampColumn)
			assert.NotNil(t, usersState.MaxTimestamp)
			t.Logf("Users max timestamp: %v", usersState.MaxTimestamp)
		}

		// Settings table should use PK-based sync
		settingsState, ok := metadata.TableSyncState["public.settings"]
		assert.True(t, ok, "settings table should have sync state")
		if ok {
			assert.Empty(t, settingsState.TimestampColumn)
			assert.Equal(t, "id", settingsState.PrimaryKeyColumn)
			assert.NotNil(t, settingsState.MaxPrimaryKey)
			t.Logf("Settings max PK: %v", *settingsState.MaxPrimaryKey)
		}

		t.Logf("Full sync completed: %d tables, %d ms",
			len(metadata.TableSyncState), metadata.SyncDurationMs)
	})

	// Test 2: Second sync with no changes should still work
	t.Run("NoChangesSync", func(t *testing.T) {
		result, err := CheckSyncNeeded(cfg, projectName, tempDir)
		require.NoError(t, err)
		t.Logf("Sync needed: %v, reason: %s", result.NeedsSync, result.Reason)
	})

	// Test 3: Add new data and verify incremental sync
	t.Run("IncrementalSyncNewRows", func(t *testing.T) {
		// Add new data to the database
		db, err := sql.Open("postgres", testDBURL)
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Wait a moment to ensure timestamps are different
		time.Sleep(100 * time.Millisecond)

		// Use unique values based on timestamp to avoid conflicts between test runs
		ts := time.Now().UnixNano()

		// Insert new user
		_, err = db.Exec(`INSERT INTO users (name, email) VALUES ($1, $2)`,
			fmt.Sprintf("TestUser%d", ts), fmt.Sprintf("testuser%d@example.com", ts))
		require.NoError(t, err)

		// Insert new post
		_, err = db.Exec(`INSERT INTO posts (user_id, title, content) VALUES (1, $1, 'After sync')`,
			fmt.Sprintf("NewPost%d", ts))
		require.NoError(t, err)

		// Insert new setting
		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ($1, 'new_value')`,
			fmt.Sprintf("new_setting_%d", ts))
		require.NoError(t, err)

		// Run VACUUM ANALYZE to force statistics update
		_, err = db.Exec(`VACUUM ANALYZE`)
		require.NoError(t, err)

		// Close connection to ensure stats are flushed
		_ = db.Close()

		// Small delay to allow stats to propagate
		time.Sleep(200 * time.Millisecond)

		// Check sync needed
		result, err := CheckSyncNeeded(cfg, projectName, tempDir)
		require.NoError(t, err)
		assert.True(t, result.NeedsSync, "Sync should be needed after adding data")
		t.Logf("Sync check: needed=%v, reason=%s, rowDiff=%d",
			result.NeedsSync, result.Reason, result.RowCountDiff)

		// Perform sync - should be incremental
		metadata, err := SyncFromSource(cfg, projectName, tempDir)
		require.NoError(t, err)
		assert.True(t, metadata.IsIncremental, "Second sync should be incremental")

		// Check incremental files were created
		assert.Greater(t, len(metadata.IncrementalFiles), 0, "Should have incremental files")

		// Collect all incremental file contents
		var allContent string
		for _, incFile := range metadata.IncrementalFiles {
			incPath := filepath.Join(tempDir, projectName, incFile)
			content, err := os.ReadFile(incPath)
			require.NoError(t, err)
			t.Logf("Incremental file %s:\n%s", incFile, string(content))
			allContent += string(content)
		}

		// Should contain INSERT statements for new rows across all files
		assert.Contains(t, allContent, "INSERT INTO")
		assert.Contains(t, allContent, "users")
		assert.Contains(t, allContent, "settings")

		t.Logf("Incremental sync completed: %d ms, files: %v",
			metadata.SyncDurationMs, metadata.IncrementalFiles)
	})

	// Test 4: Delete golden copy and verify full sync happens
	t.Run("ForceFullSync", func(t *testing.T) {
		err := DeleteGoldenCopyFiles(projectName, tempDir)
		require.NoError(t, err)

		// Verify golden copy deleted
		assert.False(t, GoldenCopyExists(projectName, tempDir))

		// Next sync should be full
		metadata, err := SyncFromSource(cfg, projectName, tempDir)
		require.NoError(t, err)
		assert.False(t, metadata.IsIncremental, "After delete, sync should be full")
		assert.Empty(t, metadata.IncrementalFiles, "Full sync should clear incremental files")

		t.Logf("Full sync after delete: %d ms", metadata.SyncDurationMs)
	})
}

func TestV2SyncWithSchemaChange(t *testing.T) {
	// Skip - this test is for V2 file-based sync, but we now use V3 golden database
	t.Skip("Test needs to be updated for V3 golden database format")

	// Skip if test DB not available
	if err := ValidateConnection(testDBURL); err != nil {
		t.Skipf("Test database not available: %v", err)
	}

	// Create temp directory for sync files
	tempDir, err := os.MkdirTemp("", "conductor-v2sync-test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tempDir) }()

	cfg := &DatabaseConfig{
		Source: testDBURL,
	}
	projectName := "test-v2"

	// Test 1: First V2 sync
	t.Run("FirstV2Sync", func(t *testing.T) {
		metadata, err := SyncV2FromSourceCtx(context.Background(), cfg, projectName, tempDir, func(msg string) {
			t.Logf("Progress: %s", msg)
		})
		require.NoError(t, err)
		assert.NotNil(t, metadata)
		assert.Equal(t, SyncVersionSeparated, metadata.SyncVersion)

		// Check schema.sql was created
		schemaPath := filepath.Join(tempDir, projectName, "schema.sql")
		_, err = os.Stat(schemaPath)
		assert.NoError(t, err, "schema.sql should exist")

		// Check data directory exists
		dataDir := filepath.Join(tempDir, projectName, "data")
		_, err = os.Stat(dataDir)
		assert.NoError(t, err, "data directory should exist")

		// Check per-table data files
		assert.NotEmpty(t, metadata.TableDataFiles, "should have table data files")
		t.Logf("Table data files: %v", metadata.TableDataFiles)

		// Check schema snapshot
		assert.NotEmpty(t, metadata.SchemaSnapshot, "should have schema snapshot")
		t.Logf("Schema snapshot has %d tables", len(metadata.SchemaSnapshot))
	})

	// Test 2: Add a new table (simulating migration)
	t.Run("SchemaChangeAddTable", func(t *testing.T) {
		db, err := sql.Open("postgres", testDBURL)
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Create new table
		tableName := fmt.Sprintf("new_table_%d", time.Now().UnixNano())
		_, err = db.Exec(fmt.Sprintf(`
			CREATE TABLE %s (
				id SERIAL PRIMARY KEY,
				name VARCHAR(255),
				created_at TIMESTAMPTZ DEFAULT NOW()
			)
		`, tableName))
		require.NoError(t, err)

		// Insert some data
		_, err = db.Exec(fmt.Sprintf(`INSERT INTO %s (name) VALUES ('test1'), ('test2')`, tableName))
		require.NoError(t, err)

		// Sync again
		metadata, err := SyncV2FromSourceCtx(context.Background(), cfg, projectName, tempDir, func(msg string) {
			t.Logf("Progress: %s", msg)
		})
		require.NoError(t, err)

		// Should have detected the new table
		fullTableName := "public." + tableName
		_, hasNewTable := metadata.TableDataFiles[fullTableName]
		assert.True(t, hasNewTable, "should have data file for new table")
		t.Logf("New table %s added to sync", fullTableName)

		// Clean up
		_, err = db.Exec(fmt.Sprintf(`DROP TABLE %s`, tableName))
		require.NoError(t, err)
	})

	// Test 3: Sync after table removal
	t.Run("SchemaChangeRemoveTable", func(t *testing.T) {
		// Sync to detect the removed table
		metadata, err := SyncV2FromSourceCtx(context.Background(), cfg, projectName, tempDir, func(msg string) {
			t.Logf("Progress: %s", msg)
		})
		require.NoError(t, err)
		t.Logf("Tables after removal sync: %d", len(metadata.TableDataFiles))
	})

	// Test 4: Add data and verify incremental still works
	t.Run("IncrementalAfterSchemaChange", func(t *testing.T) {
		db, err := sql.Open("postgres", testDBURL)
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Get current row count
		prevMetadata, _ := LoadSyncMetadata(projectName, tempDir)
		prevUserCount := prevMetadata.RowCounts["public.users"]

		// Add new user
		ts := time.Now().UnixNano()
		_, err = db.Exec(`INSERT INTO users (name, email) VALUES ($1, $2)`,
			fmt.Sprintf("V2User%d", ts), fmt.Sprintf("v2user%d@example.com", ts))
		require.NoError(t, err)

		// Sync
		metadata, err := SyncV2FromSourceCtx(context.Background(), cfg, projectName, tempDir, func(msg string) {
			t.Logf("Progress: %s", msg)
		})
		require.NoError(t, err)

		// Row count should have increased
		assert.Greater(t, metadata.RowCounts["public.users"], prevUserCount)
		t.Logf("User count: %d -> %d", prevUserCount, metadata.RowCounts["public.users"])
	})
}

func TestGetTableIncrementalInfo(t *testing.T) {
	// Skip if test DB not available
	if err := ValidateConnection(testDBURL); err != nil {
		t.Skipf("Test database not available: %v", err)
	}

	info, err := GetTableIncrementalInfo(testDBURL)
	require.NoError(t, err)

	// Check users table
	usersInfo, ok := info["public.users"]
	assert.True(t, ok, "should find users table")
	if ok {
		assert.Equal(t, "updated_at", usersInfo.TimestampColumn, "users should use updated_at")
		assert.Equal(t, "id", usersInfo.PrimaryKey)
		t.Logf("users: ts=%s, pk=%s (%s)", usersInfo.TimestampColumn, usersInfo.PrimaryKey, usersInfo.PrimaryKeyType)
	}

	// Check comments table (only has created_at, no updated_at)
	commentsInfo, ok := info["public.comments"]
	assert.True(t, ok, "should find comments table")
	if ok {
		assert.Equal(t, "created_at", commentsInfo.TimestampColumn, "comments should use created_at")
		t.Logf("comments: ts=%s, pk=%s", commentsInfo.TimestampColumn, commentsInfo.PrimaryKey)
	}

	// Check settings table (no timestamps)
	settingsInfo, ok := info["public.settings"]
	assert.True(t, ok, "should find settings table")
	if ok {
		assert.Empty(t, settingsInfo.TimestampColumn, "settings should have no timestamp")
		assert.Equal(t, "id", settingsInfo.PrimaryKey)
		t.Logf("settings: ts=%s, pk=%s (%s)", settingsInfo.TimestampColumn, settingsInfo.PrimaryKey, settingsInfo.PrimaryKeyType)
	}
}
