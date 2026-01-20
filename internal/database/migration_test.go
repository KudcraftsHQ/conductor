package database

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWorktreeMigrations(t *testing.T) {
	// Create a temporary directory structure
	tmpDir := t.TempDir()

	// Create prisma/migrations directory
	migrationsDir := filepath.Join(tmpDir, "prisma", "migrations")
	require.NoError(t, os.MkdirAll(migrationsDir, 0755))

	// Create some migration directories with migration.sql files
	migrations := []string{
		"20240101000000_init",
		"20240102000000_add_users",
		"20240103000000_add_posts",
	}

	for _, m := range migrations {
		migDir := filepath.Join(migrationsDir, m)
		require.NoError(t, os.MkdirAll(migDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(migDir, "migration.sql"), []byte("-- migration"), 0644))
	}

	// Create migration_lock.toml (should be ignored)
	require.NoError(t, os.WriteFile(filepath.Join(migrationsDir, "migration_lock.toml"), []byte("provider = \"postgresql\""), 0644))

	// Test
	result, err := GetWorktreeMigrations(tmpDir)
	require.NoError(t, err)

	assert.Equal(t, migrations, result)
}

func TestGetWorktreeMigrations_NoDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// No prisma/migrations directory
	result, err := GetWorktreeMigrations(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestGetWorktreeMigrations_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create empty prisma/migrations directory
	migrationsDir := filepath.Join(tmpDir, "prisma", "migrations")
	require.NoError(t, os.MkdirAll(migrationsDir, 0755))

	result, err := GetWorktreeMigrations(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestComputeMigrationChecksum(t *testing.T) {
	tmpDir := t.TempDir()

	// Create migration directory
	migDir := filepath.Join(tmpDir, "prisma", "migrations", "20240101000000_init")
	require.NoError(t, os.MkdirAll(migDir, 0755))

	// Create migration.sql with known content
	content := "CREATE TABLE users (id SERIAL PRIMARY KEY);"
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "migration.sql"), []byte(content), 0644))

	// Compute checksum
	checksum, err := ComputeMigrationChecksum(tmpDir, "20240101000000_init")
	require.NoError(t, err)

	// Checksum should be consistent
	checksum2, err := ComputeMigrationChecksum(tmpDir, "20240101000000_init")
	require.NoError(t, err)
	assert.Equal(t, checksum, checksum2)

	// Should be a valid hex string (SHA256 = 64 chars)
	assert.Len(t, checksum, 64)
}

func TestDetermineCompatibility(t *testing.T) {
	tests := []struct {
		name     string
		state    *MigrationState
		expected MigrationCompatibility
	}{
		{
			name: "synced - no differences",
			state: &MigrationState{
				PendingMigrations:   nil,
				ExtraMigrations:     nil,
				DivergentMigrations: nil,
			},
			expected: MigrationSynced,
		},
		{
			name: "forward - pending only",
			state: &MigrationState{
				PendingMigrations:   []string{"20240104_new"},
				ExtraMigrations:     nil,
				DivergentMigrations: nil,
			},
			expected: MigrationForward,
		},
		{
			name: "behind - extra only",
			state: &MigrationState{
				PendingMigrations:   nil,
				ExtraMigrations:     []string{"20240104_from_main"},
				DivergentMigrations: nil,
			},
			expected: MigrationBehind,
		},
		{
			name: "diverged - checksum mismatch",
			state: &MigrationState{
				PendingMigrations:   nil,
				ExtraMigrations:     nil,
				DivergentMigrations: []string{"20240103_modified"},
			},
			expected: MigrationDiverged,
		},
		{
			name: "diverged - both pending and extra",
			state: &MigrationState{
				PendingMigrations:   []string{"20240104_feature"},
				ExtraMigrations:     []string{"20240104_other"},
				DivergentMigrations: nil,
			},
			expected: MigrationDiverged,
		},
		{
			name: "diverged takes precedence",
			state: &MigrationState{
				PendingMigrations:   []string{"20240105_new"},
				ExtraMigrations:     nil,
				DivergentMigrations: []string{"20240103_changed"},
			},
			expected: MigrationDiverged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineCompatibility(tt.state)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetRecommendedAction(t *testing.T) {
	tests := []struct {
		name        string
		state       *MigrationState
		containsStr string
	}{
		{
			name: "synced",
			state: &MigrationState{
				Compatibility: MigrationSynced,
			},
			containsStr: "up to date",
		},
		{
			name: "forward single",
			state: &MigrationState{
				Compatibility:     MigrationForward,
				PendingMigrations: []string{"one"},
			},
			containsStr: "1 pending migration",
		},
		{
			name: "forward multiple",
			state: &MigrationState{
				Compatibility:     MigrationForward,
				PendingMigrations: []string{"one", "two", "three"},
			},
			containsStr: "3 pending migrations",
		},
		{
			name: "behind",
			state: &MigrationState{
				Compatibility:   MigrationBehind,
				ExtraMigrations: []string{"extra"},
			},
			containsStr: "rebasing",
		},
		{
			name: "diverged",
			state: &MigrationState{
				Compatibility:       MigrationDiverged,
				DivergentMigrations: []string{"changed"},
			},
			containsStr: "reinit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getRecommendedAction(tt.state)
			assert.Contains(t, result, tt.containsStr)
		})
	}
}

func TestCreateMigrationBaseline(t *testing.T) {
	t.Run("empty migrations", func(t *testing.T) {
		baseline := CreateMigrationBaseline([]PrismaMigration{})
		assert.Equal(t, 0, baseline.TotalMigrations)
		assert.Empty(t, baseline.MigrationNames)
		assert.Empty(t, baseline.LastMigrationName)
	})

	t.Run("with migrations", func(t *testing.T) {
		migrations := []PrismaMigration{
			{MigrationName: "20240101_init", Checksum: "abc123"},
			{MigrationName: "20240102_users", Checksum: "def456"},
			{MigrationName: "20240103_posts", Checksum: "ghi789"},
		}

		baseline := CreateMigrationBaseline(migrations)
		assert.Equal(t, 3, baseline.TotalMigrations)
		assert.Equal(t, "20240103_posts", baseline.LastMigrationName)
		assert.Equal(t, "ghi789", baseline.LastMigrationChecksum)
		assert.Equal(t, []string{"20240101_init", "20240102_users", "20240103_posts"}, baseline.MigrationNames)
	})
}

func TestCompareMigrationBaselines(t *testing.T) {
	tests := []struct {
		name     string
		golden   *MigrationBaseline
		current  *MigrationBaseline
		expected MigrationCompatibility
	}{
		{
			name:     "nil golden",
			golden:   nil,
			current:  &MigrationBaseline{TotalMigrations: 1},
			expected: MigrationUnknown,
		},
		{
			name:     "nil current",
			golden:   &MigrationBaseline{TotalMigrations: 1},
			current:  nil,
			expected: MigrationUnknown,
		},
		{
			name: "synced - identical",
			golden: &MigrationBaseline{
				TotalMigrations:       3,
				LastMigrationName:     "20240103_posts",
				LastMigrationChecksum: "abc123",
				MigrationNames:        []string{"20240101_init", "20240102_users", "20240103_posts"},
			},
			current: &MigrationBaseline{
				TotalMigrations:       3,
				LastMigrationName:     "20240103_posts",
				LastMigrationChecksum: "abc123",
				MigrationNames:        []string{"20240101_init", "20240102_users", "20240103_posts"},
			},
			expected: MigrationSynced,
		},
		{
			name: "forward - current has more",
			golden: &MigrationBaseline{
				TotalMigrations:       2,
				LastMigrationName:     "20240102_users",
				LastMigrationChecksum: "def456",
				MigrationNames:        []string{"20240101_init", "20240102_users"},
			},
			current: &MigrationBaseline{
				TotalMigrations:       3,
				LastMigrationName:     "20240103_posts",
				LastMigrationChecksum: "ghi789",
				MigrationNames:        []string{"20240101_init", "20240102_users", "20240103_posts"},
			},
			expected: MigrationForward,
		},
		{
			name: "behind - golden has more",
			golden: &MigrationBaseline{
				TotalMigrations:       3,
				LastMigrationName:     "20240103_posts",
				LastMigrationChecksum: "ghi789",
				MigrationNames:        []string{"20240101_init", "20240102_users", "20240103_posts"},
			},
			current: &MigrationBaseline{
				TotalMigrations:       2,
				LastMigrationName:     "20240102_users",
				LastMigrationChecksum: "def456",
				MigrationNames:        []string{"20240101_init", "20240102_users"},
			},
			expected: MigrationBehind,
		},
		{
			name: "diverged - same count different migrations",
			golden: &MigrationBaseline{
				TotalMigrations:       3,
				LastMigrationName:     "20240103_posts",
				LastMigrationChecksum: "ghi789",
				MigrationNames:        []string{"20240101_init", "20240102_users", "20240103_posts"},
			},
			current: &MigrationBaseline{
				TotalMigrations:       3,
				LastMigrationName:     "20240103_comments",
				LastMigrationChecksum: "xyz999",
				MigrationNames:        []string{"20240101_init", "20240102_users", "20240103_comments"},
			},
			expected: MigrationDiverged,
		},
		{
			name: "diverged - different prefix",
			golden: &MigrationBaseline{
				TotalMigrations: 2,
				MigrationNames:  []string{"20240101_init", "20240102_users"},
			},
			current: &MigrationBaseline{
				TotalMigrations: 3,
				MigrationNames:  []string{"20240101_init", "20240102_other", "20240103_posts"},
			},
			expected: MigrationDiverged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CompareMigrationBaselines(tt.golden, tt.current)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasPrismaMigrations(t *testing.T) {
	t.Run("has prisma schema", func(t *testing.T) {
		tmpDir := t.TempDir()
		prismaDir := filepath.Join(tmpDir, "prisma")
		require.NoError(t, os.MkdirAll(prismaDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(prismaDir, "schema.prisma"), []byte("generator client {}"), 0644))

		assert.True(t, HasPrismaMigrations(tmpDir))
	})

	t.Run("no prisma schema", func(t *testing.T) {
		tmpDir := t.TempDir()
		assert.False(t, HasPrismaMigrations(tmpDir))
	})
}

// Integration test helper - creates a mock worktree structure
func createMockWorktree(t *testing.T, migrations []struct {
	name    string
	content string
}) string {
	tmpDir := t.TempDir()
	migrationsDir := filepath.Join(tmpDir, "prisma", "migrations")
	require.NoError(t, os.MkdirAll(migrationsDir, 0755))

	// Create schema.prisma
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "prisma", "schema.prisma"),
		[]byte("generator client {\n  provider = \"prisma-client-js\"\n}"),
		0644,
	))

	for _, m := range migrations {
		migDir := filepath.Join(migrationsDir, m.name)
		require.NoError(t, os.MkdirAll(migDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(migDir, "migration.sql"), []byte(m.content), 0644))
	}

	return tmpDir
}

func TestDetectMigrationState_MockWorktree(t *testing.T) {
	// This test doesn't require a real database - it tests the worktree side
	worktree := createMockWorktree(t, []struct {
		name    string
		content string
	}{
		{"20240101000000_init", "CREATE TABLE users (id SERIAL);"},
		{"20240102000000_add_email", "ALTER TABLE users ADD COLUMN email TEXT;"},
	})

	migrations, err := GetWorktreeMigrations(worktree)
	require.NoError(t, err)
	assert.Len(t, migrations, 2)
	assert.Equal(t, "20240101000000_init", migrations[0])
	assert.Equal(t, "20240102000000_add_email", migrations[1])

	// Test checksum computation
	checksum1, err := ComputeMigrationChecksum(worktree, "20240101000000_init")
	require.NoError(t, err)
	assert.NotEmpty(t, checksum1)

	checksum2, err := ComputeMigrationChecksum(worktree, "20240102000000_add_email")
	require.NoError(t, err)
	assert.NotEmpty(t, checksum2)

	// Different content should have different checksums
	assert.NotEqual(t, checksum1, checksum2)
}

func TestMigrationCompatibility_String(t *testing.T) {
	// Verify constants are what we expect
	assert.Equal(t, MigrationCompatibility("forward"), MigrationForward)
	assert.Equal(t, MigrationCompatibility("diverged"), MigrationDiverged)
	assert.Equal(t, MigrationCompatibility("behind"), MigrationBehind)
	assert.Equal(t, MigrationCompatibility("synced"), MigrationSynced)
	assert.Equal(t, MigrationCompatibility("unknown"), MigrationUnknown)
}

func TestPrismaMigration_Fields(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)

	m := PrismaMigration{
		ID:                "test-id",
		MigrationName:     "20240101_init",
		Checksum:          "abc123",
		AppliedAt:         now,
		AppliedStepsCount: 1,
		RolledBackAt:      &later,
	}

	assert.Equal(t, "test-id", m.ID)
	assert.Equal(t, "20240101_init", m.MigrationName)
	assert.Equal(t, "abc123", m.Checksum)
	assert.Equal(t, now, m.AppliedAt)
	assert.Equal(t, 1, m.AppliedStepsCount)
	assert.NotNil(t, m.RolledBackAt)
	assert.Equal(t, later, *m.RolledBackAt)
}
