package database

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Manager coordinates database operations
type Manager struct {
	localURL  string
	dbsyncDir string
	mu        sync.Mutex
	syncing   map[string]bool // Track which projects are currently syncing
}

// NewManager creates a new database manager
func NewManager(localURL string, conductorDir string) *Manager {
	return &Manager{
		localURL:  localURL,
		dbsyncDir: filepath.Join(conductorDir, "dbsync"),
		syncing:   make(map[string]bool),
	}
}

// SyncProject syncs a project's source database to a local golden copy
func (m *Manager) SyncProject(projectName string, cfg *DatabaseConfig) (*SyncMetadata, error) {
	return m.SyncProjectWithProgress(projectName, cfg, nil)
}

// SyncProjectWithProgress syncs with progress callback
func (m *Manager) SyncProjectWithProgress(projectName string, cfg *DatabaseConfig, progress ProgressFunc) (*SyncMetadata, error) {
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

	return SyncFromSourceWithProgress(cfg, projectName, m.dbsyncDir, progress)
}

// CheckSyncNeeded checks if a sync is needed for a project
func (m *Manager) CheckSyncNeeded(projectName string, cfg *DatabaseConfig) (*SyncCheckResult, error) {
	return CheckSyncNeeded(cfg, projectName, m.dbsyncDir)
}

// IsSyncing returns true if a project is currently syncing
func (m *Manager) IsSyncing(projectName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.syncing[projectName]
}

// CloneForWorktree creates a database for a worktree from the golden copy
func (m *Manager) CloneForWorktree(projectName string, dbName string) error {
	goldenPath := GetGoldenCopyPath(projectName, m.dbsyncDir)
	schemaPath := GetSchemaOnlyPath(projectName, m.dbsyncDir)

	// Check if golden copy exists
	if !GoldenCopyExists(projectName, m.dbsyncDir) {
		return fmt.Errorf("no golden copy found for project %s - run sync first", projectName)
	}

	return CloneToWorktree(m.localURL, dbName, goldenPath, schemaPath)
}

// CleanupWorktree drops a worktree database
func (m *Manager) CleanupWorktree(dbName string) error {
	return DropDatabase(m.localURL, dbName)
}

// GetSyncStatus returns the sync metadata for a project
func (m *Manager) GetSyncStatus(projectName string) (*SyncMetadata, error) {
	return LoadSyncMetadata(projectName, m.dbsyncDir)
}

// HasGoldenCopy checks if a project has a golden copy
func (m *Manager) HasGoldenCopy(projectName string) bool {
	return GoldenCopyExists(projectName, m.dbsyncDir)
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

// DeleteGoldenCopy deletes the golden copy for a project
func (m *Manager) DeleteGoldenCopy(projectName string) error {
	projectDir := filepath.Join(m.dbsyncDir, projectName)
	return os.RemoveAll(projectDir)
}

// GetGoldenCopySize returns the size of the golden copy in bytes
func (m *Manager) GetGoldenCopySize(projectName string) (int64, error) {
	goldenPath := GetGoldenCopyPath(projectName, m.dbsyncDir)
	info, err := os.Stat(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

// GenerateWorktreeDBName generates a database name for a worktree
func (m *Manager) GenerateWorktreeDBName(projectName string, port int, pattern string) string {
	return GenerateDBName(projectName, port, pattern)
}

// BuildWorktreeDBURL builds a full connection URL for a worktree database
func (m *Manager) BuildWorktreeDBURL(dbName string) string {
	return BuildWorktreeURL(m.localURL, dbName)
}
