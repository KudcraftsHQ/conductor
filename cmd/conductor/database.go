package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/database"
	"github.com/hammashamzah/conductor/internal/runner"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/spf13/cobra"
)

var databaseCmd = &cobra.Command{
	Use:     "database",
	Aliases: []string{"db"},
	Short:   "Manage database synchronization",
	Long:    "Configure, sync, and manage local database copies for worktrees",
}

var databaseConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or set database configuration",
	Long:  "View or modify database sync configuration for the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project. Run 'conductor project add .' first")
		}

		// Set fields given as flags
		if cmd.Flags().Changed("empty") || cmd.Flags().Changed("seed-command") {
			if project.Database == nil {
				return fmt.Errorf("database not configured for this project")
			}
			updated := *project.Database
			if cmd.Flags().Changed("empty") {
				updated.Empty = configEmpty
			}
			if cmd.Flags().Changed("seed-command") {
				updated.SeedCommand = configSeedCommand
			}
			if err := s.SetDatabaseConfig(projectName, &updated); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}
			project.Database = &updated
			fmt.Printf("✓ Updated database config for %s\n\n", projectName)
		}

		// Show current config
		fmt.Printf("Database configuration for project: %s\n\n", projectName)

		defaults := s.GetDefaults()
		fmt.Printf("Local PostgreSQL URL: %s\n", maskURL(defaults.LocalPostgresURL))

		if project.Database == nil {
			fmt.Println("\nDatabase sync: Not configured")
			fmt.Println("\nTo configure, use:")
			fmt.Println("  conductor database set-source <connection-string>")
			return nil
		}

		mode := project.Database.Mode
		if mode == "" {
			mode = config.DatabaseModeLocal
		}
		fmt.Printf("\nMode: %s\n", mode)
		if project.Database.Empty {
			fmt.Println("Worktree databases: empty (no copy of source)")
		} else {
			fmt.Println("Worktree databases: cloned from source")
		}
		seed := project.Database.SeedCommand
		if seed == "" {
			seed = "(none)"
		}
		fmt.Printf("Seed command (after reinstantiate): %s\n", seed)
		if project.Database.Source != "" {
			fmt.Printf("Source URL: %s\n", database.MaskConnectionString(project.Database.Source))
		}
		fmt.Printf("Size threshold: %d MB\n", project.Database.SizeThresholdMB)
		fmt.Printf("Excluded tables: %v\n", project.Database.ExcludeTables)
		fmt.Printf("DB name pattern: %s\n", getPattern(project.Database.DBNamePattern))

		return nil
	},
}

var (
	configEmpty       bool
	configSeedCommand string
)

var (
	setSourceThreshold int
	setSourceExclude   []string
	setSourcePattern   string
)

var databaseSetSourceCmd = &cobra.Command{
	Use:   "set-source <connection-string>",
	Short: "Set the source database connection",
	Long:  "Configure the source database (production/staging) to sync from",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceURL := args[0]

		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project. Run 'conductor project add .' first")
		}

		// Validate connection
		fmt.Println("Validating connection...")
		if err := database.ValidateConnection(sourceURL); err != nil {
			return fmt.Errorf("failed to connect to source database: %w", err)
		}

		// Check if read-only
		isReadOnly, err := database.IsReadOnly(sourceURL)
		if err == nil && !isReadOnly {
			fmt.Println("\n⚠️  WARNING: This connection has WRITE access to the source database!")
			fmt.Println("   Consider using a read-only user for safety.")
			fmt.Println()
		}

		// Create or update database config, keeping fields this command does
		// not set (mode, remote URLs, empty, seedCommand...)
		dbConfig := &config.DatabaseConfig{}
		if project.Database != nil {
			*dbConfig = *project.Database
		}
		dbConfig.Source = sourceURL
		dbConfig.SizeThresholdMB = setSourceThreshold
		dbConfig.ExcludeTables = setSourceExclude
		dbConfig.DBNamePattern = setSourcePattern

		// Preserve existing settings if not specified
		if project.Database != nil {
			if setSourceThreshold == 0 {
				dbConfig.SizeThresholdMB = project.Database.SizeThresholdMB
			}
			if len(setSourceExclude) == 0 {
				dbConfig.ExcludeTables = project.Database.ExcludeTables
			}
			if setSourcePattern == "" {
				dbConfig.DBNamePattern = project.Database.DBNamePattern
			}
		}

		// Update via store
		if err := s.SetDatabaseConfig(projectName, dbConfig); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("\n✓ Database source configured for %s\n", projectName)
		fmt.Printf("  Source: %s\n", database.MaskConnectionString(sourceURL))
		fmt.Println("\nNext steps:")
		fmt.Println("  conductor database sync    # Sync from source to local golden copy")

		return nil
	},
}

var databaseSetLocalCmd = &cobra.Command{
	Use:   "set-local <connection-string>",
	Short: "Set the local PostgreSQL connection",
	Long:  "Configure the local PostgreSQL server where worktree databases will be created",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		localURL := args[0]

		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		// Validate connection
		fmt.Println("Validating connection...")
		if err := database.ValidateConnection(localURL); err != nil {
			return fmt.Errorf("failed to connect to local PostgreSQL: %w", err)
		}

		// Update via store
		if err := s.SetLocalPostgresURL(localURL); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("\n✓ Local PostgreSQL configured: %s\n", maskURL(localURL))

		return nil
	},
}

var databaseStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show sync status and table information",
	Long:  "Display the current sync status, table sizes, and golden copy information",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project. Run 'conductor project add .' first")
		}

		if project.Database == nil {
			return fmt.Errorf("database not configured for this project")
		}

		defaults := s.GetDefaults()
		conductorDir, err := config.ConductorDir()
		if err != nil {
			return err
		}

		mgr := database.NewManager(defaults.LocalPostgresURL, conductorDir)

		fmt.Printf("Database status for %s\n\n", projectName)

		// Check golden copy
		if mgr.HasGoldenCopy(projectName) {
			metadata, err := mgr.GetSyncStatus(projectName)
			if err == nil && metadata != nil {
				fmt.Printf("Last sync: %s\n", metadata.LastSyncAt.Format("2006-01-02 15:04:05"))
				fmt.Printf("Golden copy size: %s\n", database.FormatSize(metadata.GoldenFileSize))
				fmt.Printf("Tables synced: %d\n", len(metadata.TableSizes))
				fmt.Printf("Tables excluded: %d\n", len(metadata.ExcludedTables))

				if len(metadata.ExcludedTables) > 0 {
					fmt.Printf("\nExcluded tables (schema only):\n")
					for _, t := range metadata.ExcludedTables {
						fmt.Printf("  - %s\n", t)
					}
				}
			}
		} else {
			fmt.Println("Golden copy: Not created yet")
			fmt.Println("  Run 'conductor database sync' to create")
		}

		// List worktree databases
		if defaults.LocalPostgresURL != "" {
			dbs, err := mgr.ListWorktreeDatabases(projectName)
			if err == nil && len(dbs) > 0 {
				fmt.Printf("\nWorktree databases:\n")
				for _, db := range dbs {
					fmt.Printf("  - %s\n", db)
				}
			}
		}

		return nil
	},
}

var databaseListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all worktree databases",
	Long:  "List all databases created for worktrees in the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, _, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project. Run 'conductor project add .' first")
		}

		defaults := s.GetDefaults()
		if defaults.LocalPostgresURL == "" {
			return fmt.Errorf("local PostgreSQL not configured")
		}

		conductorDir, err := config.ConductorDir()
		if err != nil {
			return err
		}

		mgr := database.NewManager(defaults.LocalPostgresURL, conductorDir)

		dbs, err := mgr.ListWorktreeDatabases(projectName)
		if err != nil {
			return fmt.Errorf("failed to list databases: %w", err)
		}

		if len(dbs) == 0 {
			fmt.Println("No worktree databases found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "DATABASE\tSTATUS")
		for _, db := range dbs {
			exists, _ := mgr.WorktreeDBExists(db)
			status := "active"
			if !exists {
				status = "missing"
			}
			fmt.Fprintf(w, "%s\t%s\n", db, status)
		}
		w.Flush()

		return nil
	},
}

var databaseDropCmd = &cobra.Command{
	Use:   "drop <database-name>",
	Short: "Drop a worktree database",
	Long:  "Manually drop a worktree database (usually handled automatically by archive)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dbName := args[0]

		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		defaults := s.GetDefaults()
		if defaults.LocalPostgresURL == "" {
			return fmt.Errorf("local PostgreSQL not configured")
		}

		conductorDir, err := config.ConductorDir()
		if err != nil {
			return err
		}

		mgr := database.NewManager(defaults.LocalPostgresURL, conductorDir)

		// Check if exists
		exists, err := mgr.WorktreeDBExists(dbName)
		if err != nil {
			return fmt.Errorf("failed to check database: %w", err)
		}
		if !exists {
			return fmt.Errorf("database '%s' does not exist", dbName)
		}

		fmt.Printf("Dropping database %s...\n", dbName)
		if err := mgr.CleanupWorktree(dbName); err != nil {
			return fmt.Errorf("failed to drop database: %w", err)
		}

		fmt.Printf("✓ Database %s dropped\n", dbName)
		return nil
	},
}

var cloneWorktree string

var databaseCloneCmd = &cobra.Command{
	Use:   "clone",
	Short: "Clone golden copy to a worktree database",
	Long:  "Create a new database for a worktree from the golden copy and run pending migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		if project.Database == nil {
			return fmt.Errorf("database not configured for this project")
		}

		defaults := s.GetDefaults()
		if defaults.LocalPostgresURL == "" {
			return fmt.Errorf("local PostgreSQL not configured. Run 'conductor database set-local <url>' first")
		}

		conductorDir, err := config.ConductorDir()
		if err != nil {
			return err
		}

		mgr := database.NewManager(defaults.LocalPostgresURL, conductorDir)

		// Check if golden copy exists (empty mode needs none)
		if !project.Database.Empty && !mgr.HasGoldenCopy(projectName) {
			return fmt.Errorf("no golden copy found. Run 'conductor database sync' first")
		}

		// Find the worktree
		var worktreeName string
		var worktree *config.Worktree

		if cloneWorktree != "" {
			worktreeName = cloneWorktree
			worktree = project.Worktrees[worktreeName]
			if worktree == nil {
				return fmt.Errorf("worktree '%s' not found", worktreeName)
			}
		} else {
			// Auto-detect from current directory
			for name, wt := range project.Worktrees {
				if wt.Path == cwd {
					worktreeName = name
					worktree = wt
					break
				}
			}
			if worktree == nil {
				return fmt.Errorf("not in a worktree. Use --worktree flag or cd to a worktree directory")
			}
		}

		// Determine database name from first port
		port := 0
		if len(worktree.Ports) > 0 {
			port = worktree.Ports[0]
		}
		dbName := database.GenerateDBName(projectName, port, project.Database.DBNamePattern)

		// Check if database already exists
		exists, err := mgr.WorktreeDBExists(dbName)
		if err != nil {
			return fmt.Errorf("failed to check database: %w", err)
		}
		if exists {
			return fmt.Errorf("database '%s' already exists. Use 'conductor database reinit' to re-clone, or 'conductor database drop %s' first", dbName, dbName)
		}

		fmt.Printf("Cloning database for worktree '%s'...\n", worktreeName)
		fmt.Printf("  Database: %s\n", dbName)
		if project.Database.Empty {
			fmt.Printf("  Source: none (empty database)\n\n")
		} else {
			fmt.Printf("  Source: golden database\n\n")
		}

		// Clone from V3 golden database, or create it empty
		err = database.ProvisionLocalWorktreeDB(cmd.Context(), project.Database, defaults.LocalPostgresURL, projectName, dbName, func(msg string) {
			fmt.Printf("  %s\n", msg)
		})
		if err != nil {
			return fmt.Errorf("clone failed: %w", err)
		}

		dbURL := database.BuildWorktreeURL(defaults.LocalPostgresURL, dbName)

		fmt.Printf("\n✓ Database cloned successfully\n")
		fmt.Printf("  Database: %s\n", dbName)
		fmt.Printf("\nSet DATABASE_URL in your .env:\n")
		fmt.Printf("  %s\n", dbURL)

		return nil
	},
}

var reinstantiateWorktree string

var databaseReinstantiateCmd = &cobra.Command{
	Use:   "reinstantiate",
	Short: "Drop and recreate a worktree database (re-clone, or empty + seedCommand)",
	Long: `Drop the worktree's database and create it again.

Remote mode clones fresh data from the source via SSH. With "empty": true in the
project's database config, the database is recreated empty instead (remote or
local mode) and the source is never read. Either way, the database config's
seedCommand, if set, then runs in the worktree with the setup script's
environment (CONDUCTOR_DB_URL, DATABASE_URL, CONDUCTOR_PORT...). The project's
setup script is not re-run.

This is a destructive operation - all data in the database will be lost.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		if project.Database == nil {
			return fmt.Errorf("database not configured for this project")
		}
		dbConfig := project.Database
		remote := dbConfig.Mode == config.DatabaseModeRemote
		localURL := s.GetDefaults().LocalPostgresURL

		switch {
		case remote && dbConfig.SSHHost == "":
			return fmt.Errorf("SSH host not configured for remote database")
		case !remote && !dbConfig.Empty:
			return fmt.Errorf("local mode reinstantiate needs \"empty\": true; for a golden-copy clone use 'conductor database clone'")
		case !remote && localURL == "":
			return fmt.Errorf("local PostgreSQL not configured. Run 'conductor database set-local <url>' first")
		}

		// Find the worktree
		var worktreeName string
		var worktree *config.Worktree

		if reinstantiateWorktree != "" {
			// Use specified worktree
			worktreeName = reinstantiateWorktree
			worktree = project.Worktrees[worktreeName]
			if worktree == nil {
				return fmt.Errorf("worktree '%s' not found", worktreeName)
			}
		} else {
			// Auto-detect from current directory
			for name, wt := range project.Worktrees {
				if wt.Path == cwd {
					worktreeName = name
					worktree = wt
					break
				}
			}
			if worktree == nil {
				return fmt.Errorf("not in a worktree. Use --worktree flag or cd to a worktree directory")
			}
		}

		if worktree.DatabaseName == "" {
			return fmt.Errorf("worktree '%s' does not have a database configured", worktreeName)
		}

		dbName := worktree.DatabaseName

		fmt.Printf("Reinstantiating database for worktree '%s'...\n", worktreeName)
		fmt.Printf("  Database: %s\n", dbName)
		if remote {
			fmt.Printf("  SSH Host: %s\n", dbConfig.SSHHost)
		}
		if dbConfig.Empty {
			fmt.Println("  Mode: empty (no source data)")
		}
		fmt.Println()

		ctx := cmd.Context()
		progress := func(msg string) { fmt.Printf("  %s\n", msg) }

		// Step 1: Drop the existing database
		fmt.Println("  Dropping existing database...")
		if remote {
			err = database.RemoteDropDatabase(ctx, dbConfig.SSHHost, dbConfig.DevURL, dbName)
		} else {
			err = database.DropDatabase(localURL, dbName)
		}
		if err != nil {
			return fmt.Errorf("failed to drop database: %w", err)
		}

		// Step 2: Recreate it (empty, or cloned fresh from source)
		if remote {
			err = database.ProvisionRemoteWorktreeDB(ctx, dbConfig, dbName, progress)
		} else {
			err = database.ProvisionLocalWorktreeDB(ctx, dbConfig, localURL, projectName, dbName, progress)
		}
		if err != nil {
			return fmt.Errorf("failed to create database: %w", err)
		}

		fmt.Printf("\n✓ Database reinstantiated: %s\n", dbName)

		// Step 3: Seed it
		if dbConfig.SeedCommand != "" {
			fmt.Printf("\nRunning seedCommand: %s\n", dbConfig.SeedCommand)
		}
		ran, err := runner.RunSeedCommand(projectName, project, worktreeName, worktree, os.Stdout, os.Stderr)
		if err != nil {
			return fmt.Errorf("seedCommand failed: %w", err)
		}
		if ran {
			fmt.Println("\n✓ Seeded")
		} else if dbConfig.Empty {
			fmt.Println("\nThe database is empty. Set seedCommand in the database config to migrate and seed it here.")
		}

		return nil
	},
}

var migrationWorktree string

var databaseMigrationStatusCmd = &cobra.Command{
	Use:   "migration-status",
	Short: "Check migration status for a worktree",
	Long:  "Compare the database's applied migrations with the worktree's migration files",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		_, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project")
		}

		defaults := s.GetDefaults()
		if defaults.LocalPostgresURL == "" {
			return fmt.Errorf("local PostgreSQL not configured")
		}

		// Find the worktree
		var worktreeName string
		var worktree *config.Worktree

		if migrationWorktree != "" {
			worktreeName = migrationWorktree
			worktree = project.Worktrees[worktreeName]
			if worktree == nil {
				return fmt.Errorf("worktree '%s' not found", worktreeName)
			}
		} else {
			for name, wt := range project.Worktrees {
				if wt.Path == cwd {
					worktreeName = name
					worktree = wt
					break
				}
			}
			if worktree == nil {
				return fmt.Errorf("not in a worktree. Use --worktree flag or cd to a worktree directory")
			}
		}

		if worktree.DatabaseName == "" {
			return fmt.Errorf("worktree '%s' does not have a database", worktreeName)
		}

		fmt.Printf("Checking migration status for '%s'...\n\n", worktreeName)

		state, err := database.GetWorktreeMigrationStatus(
			defaults.LocalPostgresURL,
			worktree.DatabaseName,
			worktree.Path,
		)
		if err != nil {
			return fmt.Errorf("failed to check migration status: %w", err)
		}

		// Display results
		fmt.Printf("Compatibility: %s\n", state.Compatibility)
		fmt.Printf("Applied migrations: %d\n", len(state.AppliedMigrations))
		fmt.Printf("Worktree migrations: %d\n", len(state.WorktreeMigrations))

		if len(state.PendingMigrations) > 0 {
			fmt.Printf("\nPending migrations (%d):\n", len(state.PendingMigrations))
			for _, m := range state.PendingMigrations {
				fmt.Printf("  + %s\n", m)
			}
		}

		if len(state.ExtraMigrations) > 0 {
			fmt.Printf("\nExtra migrations in DB (%d):\n", len(state.ExtraMigrations))
			for _, m := range state.ExtraMigrations {
				fmt.Printf("  - %s\n", m)
			}
		}

		if len(state.DivergentMigrations) > 0 {
			fmt.Printf("\nDivergent migrations (%d):\n", len(state.DivergentMigrations))
			for _, m := range state.DivergentMigrations {
				fmt.Printf("  ! %s\n", m)
			}
		}

		fmt.Printf("\nRecommended: %s\n", state.RecommendedAction)

		return nil
	},
}

var databaseAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze source database tables",
	Long:  "Show table sizes and suggest exclusions for sync",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		cfg := s.GetConfigSnapshot()

		// Detect current project
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		projectName, project, _, err := cfg.DetectProject(cwd)
		if err != nil {
			return fmt.Errorf("not in a registered project. Run 'conductor project add .' first")
		}

		if project.Database == nil {
			return fmt.Errorf("database not configured. Run 'conductor database set-source <url>' first")
		}

		fmt.Printf("Analyzing source database for %s...\n\n", projectName)

		tables, err := database.GetTableInfo(project.Database.Source)
		if err != nil {
			return fmt.Errorf("failed to analyze: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TABLE\tSIZE\tROWS\tFLAGS")

		for _, t := range tables {
			flags := ""
			if t.IsAudit {
				flags = "audit"
			}
			fullName := t.Schema + "." + t.Name
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", fullName, database.FormatSize(t.SizeBytes), t.RowCount, flags)
		}
		w.Flush()

		// Show suggestions
		threshold := project.Database.SizeThresholdMB
		if threshold == 0 {
			threshold = 100 // Default 100MB
		}
		suggestions := database.SuggestExclusions(tables, threshold)
		if len(suggestions) > 0 {
			fmt.Printf("\nSuggested exclusions (threshold: %d MB):\n", threshold)
			for _, t := range suggestions {
				fmt.Printf("  - %s\n", t)
			}
			fmt.Println("\nTo exclude these tables from data sync (schema only):")
			fmt.Printf("  conductor database set-source --exclude=%s <current-url>\n", suggestions[0])
		}

		return nil
	},
}

var (
	setupUsersAdminURL string
	setupUsersSourceDB string
)

var databaseSetupUsersCmd = &cobra.Command{
	Use:   "setup-users",
	Short: "Setup conductor users on a PostgreSQL server",
	Long: `Creates two users for secure remote database operations:
  - conductor_clone: Read-only access to source DB, can create databases
  - conductor_dev: Full access to dev_* databases only (no production access)

This is a one-time setup command for each PostgreSQL server you want to use
with remote database mode.

After running this command, add the generated cloneUrl and devUrl to your
project's conductor.json database configuration.`,
	Example: `  # Setup users on a remote PostgreSQL server
  conductor database setup-users \
    --admin-url "postgres://postgres:password@host:5432/postgres" \
    --source-db "trading-db"

  # Then add to conductor.json:
  # {
  #   "database": {
  #     "mode": "remote",
  #     "source": "postgres://...@host:port/trading-db",
  #     "cloneUrl": "<generated-clone-url>",
  #     "devUrl": "<generated-dev-url>"
  #   }
  # }`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate connection first
		fmt.Println("Validating admin connection...")
		if err := database.ValidateConnection(setupUsersAdminURL); err != nil {
			return fmt.Errorf("failed to connect with admin URL: %w", err)
		}

		fmt.Printf("Creating users for source database: %s\n\n", setupUsersSourceDB)

		result, err := database.SetupUsers(setupUsersAdminURL, setupUsersSourceDB)
		if err != nil {
			return fmt.Errorf("failed to setup users: %w", err)
		}

		fmt.Println("✓ Created users and granted permissions successfully")
		fmt.Println()
		fmt.Println("Clone user (read-only):")
		fmt.Printf("  Username: %s\n", result.CloneUser.Username)
		fmt.Printf("  URL:      %s\n", database.MaskConnectionString(result.CloneUser.URL))
		fmt.Println()
		fmt.Println("Dev user (dev_* databases only):")
		fmt.Printf("  Username: %s\n", result.DevUser.Username)
		fmt.Printf("  URL:      %s\n", database.MaskConnectionString(result.DevUser.URL))
		fmt.Println()
		fmt.Println("Add to your conductor.json database config:")
		fmt.Println()
		fmt.Println("  \"mode\": \"remote\",")
		fmt.Println("  \"sshHost\": \"user@your-server-ip\",")
		fmt.Printf("  \"cloneUrl\": \"%s\",\n", result.CloneUser.URL)
		fmt.Printf("  \"devUrl\": \"%s\"\n", result.DevUser.URL)
		fmt.Println()
		fmt.Println("Note: cloneUrl and devUrl should use localhost since they run on the server via SSH.")
		fmt.Println("      Replace the host in the URLs above with localhost if needed.")
		fmt.Println()
		fmt.Println("⚠️  Save these URLs securely - the passwords won't be shown again!")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(databaseCmd)

	databaseCmd.AddCommand(databaseConfigCmd)
	databaseCmd.AddCommand(databaseSetSourceCmd)
	databaseCmd.AddCommand(databaseSetLocalCmd)
	databaseCmd.AddCommand(databaseStatusCmd)
	databaseCmd.AddCommand(databaseListCmd)
	databaseCmd.AddCommand(databaseCloneCmd)
	databaseCmd.AddCommand(databaseDropCmd)
	databaseCmd.AddCommand(databaseAnalyzeCmd)
	databaseCmd.AddCommand(databaseReinstantiateCmd)
	databaseCmd.AddCommand(databaseMigrationStatusCmd)
	databaseCmd.AddCommand(databaseSetupUsersCmd)

	// config flags
	databaseConfigCmd.Flags().BoolVar(&configEmpty, "empty", false, "Create worktree databases empty instead of cloning the source")
	databaseConfigCmd.Flags().StringVar(&configSeedCommand, "seed-command", "", "Shell command run in the worktree after 'database reinstantiate' (\"\" to clear)")

	// set-source flags
	databaseSetSourceCmd.Flags().IntVar(&setSourceThreshold, "threshold", 0, "Auto-exclude tables larger than N MB")
	databaseSetSourceCmd.Flags().StringSliceVar(&setSourceExclude, "exclude", nil, "Tables to exclude from data sync")
	databaseSetSourceCmd.Flags().StringVar(&setSourcePattern, "pattern", "", "Database name pattern (default: {project}-{port})")

	// clone flags
	databaseCloneCmd.Flags().StringVar(&cloneWorktree, "worktree", "", "Worktree name (auto-detected if in worktree directory)")

	// reinstantiate flags
	databaseReinstantiateCmd.Flags().StringVar(&reinstantiateWorktree, "worktree", "", "Worktree name (auto-detected if in worktree directory)")

	// migration-status flags
	databaseMigrationStatusCmd.Flags().StringVar(&migrationWorktree, "worktree", "", "Worktree name (auto-detected if in worktree directory)")

	// setup-users flags
	databaseSetupUsersCmd.Flags().StringVar(&setupUsersAdminURL, "admin-url", "", "Admin PostgreSQL URL with superuser privileges (required)")
	databaseSetupUsersCmd.Flags().StringVar(&setupUsersSourceDB, "source-db", "", "Source database name to grant read access for cloning (required)")
	_ = databaseSetupUsersCmd.MarkFlagRequired("admin-url")
	_ = databaseSetupUsersCmd.MarkFlagRequired("source-db")
}

// maskURL masks the password in a URL for display
func maskURL(url string) string {
	if url == "" {
		return "(not set)"
	}
	return database.MaskConnectionString(url)
}

// getPattern returns the pattern or default
func getPattern(pattern string) string {
	if pattern == "" {
		return "{project}-{port} (default)"
	}
	return pattern
}
