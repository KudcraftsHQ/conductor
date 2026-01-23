package database

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Manager coordinates database operations (V3 golden database only)
type Manager struct {
	localURL string
	mu       sync.Mutex
	syncing  map[string]bool // Track which projects are currently syncing
}

// NewManager creates a new database manager
func NewManager(localURL string, conductorDir string) *Manager {
	return &Manager{
		localURL: localURL,
		syncing:  make(map[string]bool),
	}
}

// SyncProject syncs a project's source database to a local golden copy
func (m *Manager) SyncProject(projectName string, cfg *DatabaseConfig) (*SyncMetadata, error) {
	return m.SyncProjectWithProgressCtx(context.Background(), projectName, cfg, nil)
}

// SyncProjectWithProgress syncs with progress callback (backwards compatible)
func (m *Manager) SyncProjectWithProgress(projectName string, cfg *DatabaseConfig, progress ProgressFunc) (*SyncMetadata, error) {
	return m.SyncProjectWithProgressCtx(context.Background(), projectName, cfg, progress)
}

// SyncProjectWithProgressCtx syncs with progress callback and context for cancellation
// Uses V3 (golden database) approach - pipes directly to local PostgreSQL for speed
func (m *Manager) SyncProjectWithProgressCtx(ctx context.Context, projectName string, cfg *DatabaseConfig, progress ProgressFunc) (*SyncMetadata, error) {
	m.mu.Lock()
	if m.syncing[projectName] {
		m.mu.Unlock()
		return nil, fmt.Errorf("sync already in progress for project %s", projectName)
	}
	m.syncing[projectName] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.syncing, projectName)
		m.mu.Unlock()
	}()

	// Use V3 (golden database) approach - much faster than file-based
	result, err := SyncToGoldenDB(ctx, cfg.Source, m.localURL, projectName, cfg, progress)
	if err != nil {
		return nil, err
	}

	// Convert to SyncMetadata for backwards compatibility
	return &SyncMetadata{
		LastSyncAt:     time.Now(),
		SourceDatabase: GoldenDBName(projectName),
		TableSizes:     result.TableSizes,
		ExcludedTables: result.ExcludedTables,
		RowCounts:      result.RowCounts,
		SyncDurationMs: result.SyncDurationMs,
		SyncVersion:    SyncVersionGoldenDB,
	}, nil
}

// CheckSyncNeeded checks if a sync is needed for a project (V3 only)
func (m *Manager) CheckSyncNeeded(projectName string, cfg *DatabaseConfig) (*SyncCheckResult, error) {
	return CheckGoldenDBSyncNeeded(m.localURL, projectName, DefaultSyncCooldown)
}

// IsSyncing returns true if a project is currently syncing
func (m *Manager) IsSyncing(projectName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.syncing[projectName]
}

// CloneForWorktree creates a database for a worktree from the golden copy (V3 only)
func (m *Manager) CloneForWorktree(projectName string, dbName string) error {
	return CloneFromGoldenDB(context.Background(), m.localURL, projectName, dbName, nil)
}

// CleanupWorktree drops a worktree database
func (m *Manager) CleanupWorktree(dbName string) error {
	return DropDatabase(m.localURL, dbName)
}

// GetSyncStatus returns the sync metadata for a project (V3 only)
func (m *Manager) GetSyncStatus(projectName string) (*SyncMetadata, error) {
	return LoadGoldenDBMetadata(m.localURL, projectName)
}

// HasGoldenCopy checks if a project has a golden copy (V3 only)
func (m *Manager) HasGoldenCopy(projectName string) bool {
	exists, err := GoldenDBExists(m.localURL, projectName)
	return err == nil && exists
}

// ListWorktreeDatabases lists all databases for a project
func (m *Manager) ListWorktreeDatabases(projectName string) ([]string, error) {
	pattern := projectName + "-%"
	return ListDatabases(m.localURL, pattern)
}

// WorktreeDBExists checks if a worktree database exists
func (m *Manager) WorktreeDBExists(dbName string) (bool, error) {
	return DatabaseExists(m.localURL, dbName)
}

// GetLocalURL returns the local PostgreSQL URL
func (m *Manager) GetLocalURL() string {
	return m.localURL
}

// ValidateLocalConnection validates the local PostgreSQL connection
func (m *Manager) ValidateLocalConnection() error {
	return ValidateConnection(m.localURL)
}

// ValidateSourceConnection validates a source database connection
func (m *Manager) ValidateSourceConnection(sourceURL string) error {
	return ValidateConnection(sourceURL)
}

// IsSourceReadOnly checks if the source database connection is read-only
func (m *Manager) IsSourceReadOnly(sourceURL string) (bool, error) {
	return IsReadOnly(sourceURL)
}

// GetSourceTableInfo gets table information from a source database
func (m *Manager) GetSourceTableInfo(sourceURL string) ([]TableInfo, error) {
	return GetTableInfo(sourceURL)
}

// SuggestTableExclusions suggests tables to exclude from sync
func (m *Manager) SuggestTableExclusions(sourceURL string, thresholdMB int) ([]string, error) {
	tables, err := GetTableInfo(sourceURL)
	if err != nil {
		return nil, err
	}
	return SuggestExclusions(tables, thresholdMB), nil
}

// DeleteGoldenCopy deletes the golden database for a project (V3 only)
func (m *Manager) DeleteGoldenCopy(projectName string) error {
	return DropGoldenDB(m.localURL, projectName)
}

// GetGoldenCopySize returns the size of the golden database in bytes (V3 only)
func (m *Manager) GetGoldenCopySize(projectName string) (int64, error) {
	return GetGoldenDBSize(m.localURL, projectName)
}

// GenerateWorktreeDBName generates a database name for a worktree
func (m *Manager) GenerateWorktreeDBName(projectName string, port int, pattern string) string {
	return GenerateDBName(projectName, port, pattern)
}

// BuildWorktreeDBURL builds a full connection URL for a worktree database
func (m *Manager) BuildWorktreeDBURL(dbName string) string {
	return BuildWorktreeURL(m.localURL, dbName)
}
