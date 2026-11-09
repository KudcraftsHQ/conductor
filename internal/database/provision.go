package database

import (
	"context"
	"fmt"
	"net/url"
)

// ErrEmptyModeNoSync is returned when golden-copy sync is asked of a project
// whose worktree databases start empty: there is no source to copy from.
var ErrEmptyModeNoSync = fmt.Errorf(`project uses empty worktree databases ("empty": true); there is no source to sync`)

// ProvisionRemoteWorktreeDB creates a worktree's database on the remote dev
// server: empty when cfg.Empty is set, otherwise a server-side clone of
// cfg.CloneURL. In empty mode CloneURL and Source are never read.
func ProvisionRemoteWorktreeDB(ctx context.Context, cfg *DatabaseConfig, dbName string, progress ProgressFunc) error {
	if cfg.SSHHost == "" {
		return fmt.Errorf("remote mode requires sshHost to be configured")
	}
	if cfg.DevURL == "" {
		return fmt.Errorf("remote mode requires devUrl to be configured")
	}
	if cfg.Empty {
		return RemoteCreateEmptyDatabase(ctx, cfg.SSHHost, cfg.DevURL, dbName, progress)
	}
	if cfg.CloneURL == "" {
		return fmt.Errorf(`remote mode requires cloneUrl to be configured (or "empty": true)`)
	}
	return RemoteCloneForWorktree(ctx, cfg.SSHHost, cfg.CloneURL, cfg.DevURL, dbName, cfg.ExcludeTables, progress)
}

// ProvisionLocalWorktreeDB creates a worktree's database on the local server:
// empty when cfg.Empty is set, otherwise a clone of the project's golden copy.
func ProvisionLocalWorktreeDB(ctx context.Context, cfg *DatabaseConfig, localURL, projectName, dbName string, progress ProgressFunc) error {
	if cfg != nil && cfg.Empty {
		if progress != nil {
			progress("Creating empty database " + dbName)
		}
		return CreateDatabase(localURL, dbName)
	}
	return CloneFromGoldenDB(ctx, localURL, projectName, dbName, progress)
}

// RemoteCreateEmptyDatabase creates an empty database on the remote dev server
// via SSH, using the same admin connection RemoteCloneForWorktree uses.
func RemoteCreateEmptyDatabase(ctx context.Context, sshHost, devURL, dbName string, progress ProgressFunc) error {
	if sshHost == "" {
		return fmt.Errorf("sshHost is required for remote mode")
	}
	devParsed, err := url.Parse(devURL)
	if err != nil {
		return fmt.Errorf("invalid devUrl: %w", err)
	}
	if progress != nil {
		progress("Creating empty database " + dbName + " via SSH")
	}
	createSQL := fmt.Sprintf(`CREATE DATABASE %q`, dbName)
	if err := execSSHSQL(ctx, sshHost, buildAdminURL(devParsed), createSQL); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}
	return nil
}
