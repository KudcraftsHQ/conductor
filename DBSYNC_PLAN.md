# DB Syncer Tool + Conductor Integration Plan

## Overview

Two-part solution:
1. **`dbsync`** - Standalone CLI tool for syncing production DBs to local "golden" copies
2. **Conductor integration** - Hooks to create worktree DBs from golden copies

---

## Problem Statement

**Current flow (slow):**
```
Production DB → pg_dump (network) → pg_restore → worktree DB
(repeated for every new worktree)
```

**Proposed flow (fast):**
```
Production DB → Syncer → Local "golden" DB (periodic, background)
Local "golden" DB → local copy → worktree DB (fast, local-only)
```

With 10+ projects × 2-15 worktrees each, this eliminates redundant network transfers.

---

## Part 1: `dbsync` CLI Tool

### Purpose
Manage local "golden" copies of production databases that stay fresh via periodic syncing. Worktrees copy from these instead of hitting production every time.

### Core Concepts

```
Production DB ──(sync)──► Golden DB (local cache) ──(clone)──► Worktree DBs
                              │
                         Tracked by dbsync
                         (freshness, rules, etc.)
```

- **Source**: Remote production database (connection string)
- **Golden DB**: Local PostgreSQL database that mirrors production
- **Clone**: Fast local-only copy operation for worktrees

### CLI Commands

```bash
# Configuration
dbsync init                           # Create ~/.dbsync/config.json
dbsync add <name>                     # Add a new source interactively
dbsync list                           # List all configured sources
dbsync remove <name>                  # Remove a source
dbsync config <name> [key] [value]    # View/edit source config

# Syncing
dbsync pull <name>                    # Full sync from production → golden
dbsync pull <name> --incremental      # Incremental sync (if configured)
dbsync pull --all                     # Sync all sources
dbsync status                         # Show sync status for all sources

# Cloning (for worktrees)
dbsync clone <name> <target_db>       # Clone golden → new local DB
dbsync drop <target_db>               # Drop a cloned DB

# Maintenance
dbsync prune                          # Remove orphaned cloned DBs
dbsync daemon start                   # Start background sync daemon
dbsync daemon stop                    # Stop daemon
dbsync daemon status                  # Check daemon status
```

### Configuration Structure

**Global config**: `~/.dbsync/config.json`

```json
{
  "local": {
    "host": "localhost",
    "port": 5432,
    "user": "postgres"
  },
  "sources": {
    "myproject": {
      "productionUrl": "postgres://user:pass@prod-host:5432/myproject_prod",
      "goldenDb": "myproject_golden",
      "syncRules": {
        "schedule": "0 6 * * *",
        "mode": "full",
        "excludeTables": ["sessions", "logs", "audit_trail"],
        "pgDumpArgs": ["--no-owner", "--no-acl"]
      },
      "lastSync": "2026-01-11T06:00:00Z",
      "lastSyncDuration": "2m34s"
    }
  },
  "clones": {
    "myproject_tokyo": {
      "source": "myproject",
      "createdAt": "2026-01-10T14:30:00Z",
      "createdBy": "conductor"
    }
  }
}
```

### Sync Modes

**Full sync** (default):
```bash
# What dbsync does internally:
pg_dump "$PRODUCTION_URL" --format=custom --no-owner --no-acl \
  --exclude-table=sessions --exclude-table=logs \
  > /tmp/myproject.dump

dropdb --if-exists myproject_golden
createdb myproject_golden

pg_restore --dbname=myproject_golden --no-owner --no-acl /tmp/myproject.dump
```

**Incremental sync** (Phase 2):
- Requires `updated_at` column tracking
- Only syncs rows changed since last sync
- More complex, deferred to later phase

### Clone Operation

Fast local-only copy using `createdb --template`:

```bash
# What dbsync clone does internally:
createdb --template=myproject_golden myproject_tokyo
```

This is nearly instant for any database size since PostgreSQL uses copy-on-write.

### Data Flow Diagram

```
┌─────────────────┐     pg_dump      ┌──────────────────┐
│  Production DB  │ ───────────────► │  /tmp/dump.sql   │
│  (remote)       │    (network)     │                  │
└─────────────────┘                  └────────┬─────────┘
                                              │
                                     pg_restore
                                              │
                                              ▼
                                     ┌──────────────────┐
                                     │    Golden DB     │
                                     │ (myproject_sync) │
                                     └────────┬─────────┘
                                              │
                              createdb --template (instant, local)
                                              │
                    ┌─────────────────────────┼─────────────────────────┐
                    ▼                         ▼                         ▼
           ┌──────────────┐          ┌──────────────┐          ┌──────────────┐
           │myproject_tokyo│         │myproject_paris│        │myproject_london│
           └──────────────┘          └──────────────┘          └──────────────┘
```

### Daemon Mode

Background process for scheduled syncs:
- Reads cron schedules from config
- Runs syncs automatically
- Logs to `~/.dbsync/logs/`
- Managed via launchd (macOS) or systemd (Linux)

```bash
# Start daemon
dbsync daemon start

# Check status
dbsync daemon status
# Output:
# Daemon running (PID 12345)
# Next syncs:
#   myproject: in 2h 15m (schedule: 0 6 * * *)
#   otherproject: in 5h 45m (schedule: 0 9 * * *)
```

### Project Structure

```
dbsync/
├── cmd/dbsync/
│   └── main.go              # Cobra root command
├── internal/
│   ├── config/              # Config file management
│   │   ├── config.go
│   │   └── config_test.go
│   ├── postgres/            # pg_dump, pg_restore, createdb wrappers
│   │   ├── dump.go
│   │   ├── restore.go
│   │   ├── clone.go
│   │   └── postgres_test.go
│   ├── sync/                # Sync orchestration
│   │   ├── full.go
│   │   ├── incremental.go   # Phase 2
│   │   └── sync_test.go
│   ├── daemon/              # Background scheduler
│   │   ├── daemon.go
│   │   ├── scheduler.go
│   │   └── launchd.go       # macOS integration
│   └── clone/               # Clone management
│       ├── clone.go
│       └── prune.go
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## Part 2: Conductor Integration

### Goal

When creating a worktree, Conductor can optionally:
1. Check if a golden DB exists for this project
2. Clone it to create a worktree-specific DB
3. Inject the DB connection info into environment
4. Clean up DB when worktree is archived

### Configuration Changes

**Project-level `conductor.json`**:

```json
{
  "scripts": {
    "setup": "./scripts/setup.sh",
    "run": "./scripts/run.sh"
  },
  "database": {
    "enabled": true,
    "source": "myproject",
    "nameTemplate": "{{.ProjectName}}_{{.WorktreeName}}",
    "envVar": "DATABASE_URL",
    "onArchive": "drop"
  }
}
```

| Field | Description |
|-------|-------------|
| `enabled` | Enable DB lifecycle management |
| `source` | Name of dbsync source to clone from |
| `nameTemplate` | Go template for worktree DB name |
| `envVar` | Environment variable to inject with connection string |
| `onArchive` | `drop` (delete DB) or `keep` (preserve) |

### New Environment Variables

Conductor injects into setup/run scripts:
```bash
CONDUCTOR_DB_NAME=myproject_tokyo
CONDUCTOR_DB_URL=postgres://postgres@localhost:5432/myproject_tokyo
DATABASE_URL=postgres://postgres@localhost:5432/myproject_tokyo  # (or custom envVar)
```

### Worktree Lifecycle Changes

**On worktree creation** (in `workspace/setup.go`):

```
1. Allocate ports (existing)
2. Create git worktree (existing)
3. [NEW] If database.enabled:
   a. Resolve DB name from template
   b. Shell out: dbsync clone <source> <db_name>
   c. Build DATABASE_URL from local postgres config
   d. Add to environment variables
4. Run setup script with new env vars (existing)
```

**On worktree archive** (in `workspace/manager.go`):

```
1. Run archive script (existing)
2. [NEW] If database.enabled && onArchive == "drop":
   a. Shell out: dbsync drop <db_name>
3. Remove git worktree (existing)
```

### TUI Changes

**Worktree list view** - show DB info:
```
  tokyo     ● Running   Ports: 3100-3102   DB: synced 2h ago
  paris     ● Running   Ports: 3103-3105   DB: synced 2h ago
  london    ◐ Setting up...               DB: cloning...
```

**New keybindings**:
- `Shift+D` - Show database details (name, size, golden freshness)
- `Shift+S` - Trigger sync for project's golden DB

**Status bar integration**:
- Warning if golden DB is stale (> 24h configurable)

### Error Handling

| Scenario | Behavior |
|----------|----------|
| `dbsync` not installed | Error with install instructions |
| Golden DB doesn't exist | Error: "Run `dbsync pull <source>` first" |
| Golden DB stale (> threshold) | Warning, proceed anyway |
| Clone fails | Fail worktree creation, show error |
| Drop fails on archive | Log warning, continue archive |

### Code Changes in Conductor

**New files**:
- `internal/database/database.go` - DB lifecycle helpers
- `internal/database/config.go` - Database config parsing

**Modified files**:
- `internal/config/project.go` - Add `Database` field to project config
- `internal/workspace/setup.go` - Call dbsync clone before setup script
- `internal/workspace/manager.go` - Call dbsync drop on archive
- `internal/runner/env.go` - Add DB env vars
- `internal/tui/view.go` - Show DB status in worktree list
- `internal/tui/update.go` - Handle DB keybindings

---

## Implementation Phases

### Phase 1: Core dbsync CLI (Separate Repo)
- [ ] Project scaffolding (Go, Cobra, Makefile)
- [ ] Config management (`init`, `add`, `list`, `remove`, `config`)
- [ ] Full sync (`pull` command with pg_dump/pg_restore)
- [ ] Clone/drop commands
- [ ] Status command (show freshness, last sync time)
- [ ] Basic tests

### Phase 2: Conductor Integration (Basic)
- [ ] Add `database` field to `conductor.json` schema
- [ ] Parse database config in `internal/config`
- [ ] Shell out to `dbsync clone` during worktree creation
- [ ] Inject DB env vars via `internal/runner/env.go`
- [ ] Shell out to `dbsync drop` on worktree archive
- [ ] TUI: Show DB name in worktree list

### Phase 3: dbsync Daemon + Scheduling
- [ ] Cron expression parser
- [ ] Daemon mode (`start`, `stop`, `status`)
- [ ] launchd plist generation for macOS
- [ ] Logging to `~/.dbsync/logs/`

### Phase 4: Advanced Features
- [ ] Incremental sync mode
- [ ] Table exclusion patterns (regex)
- [ ] Anonymization transforms (Phase 2+)
- [ ] TUI: Trigger sync from conductor
- [ ] Stale DB warnings in TUI
- [ ] `dbsync prune` for orphaned DBs
- [ ] Disk usage reporting

---

## Open Questions

1. **Separate repo or monorepo?**
   - Recommendation: **Separate repo** (`github.com/you/dbsync`)
   - Keeps concerns separate, can be used without Conductor
   - Conductor calls it as external tool

2. **Local postgres auth?**
   - Assume local postgres uses `trust` or `peer` auth for localhost
   - Config stores `local.user` but not password
   - Can add password support later if needed

3. **Multiple databases per project?**
   - Current design: one source per project
   - Could extend to array of sources if needed
   - Defer until we have a real use case

4. **Template source access?**
   - `createdb --template` requires no active connections to source
   - May need to add connection termination before clone
   - Or use `pg_dump | pg_restore` locally (slower but more reliable)

5. **Disk space concerns?**
   - With many clones, disk usage grows
   - Could add `dbsync du` command to show usage
   - Could warn when total clones exceed threshold
