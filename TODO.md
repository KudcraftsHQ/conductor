# Store Migration TODO

This document tracks the migration from direct `config.*` mutations to the new Store-based state management (`internal/store/`).

## Overview

**Total mutation sites to migrate:** ~43 ✅ COMPLETED
**Total explicit `config.Save()` calls to remove:** 29 ✅ COMPLETED

**Status:** Migration complete! All store methods are now being used.

Note: The workspace package has 15 "fallback" mutations that only run when `m.store == nil` for backward compatibility. These are intentional and can be removed once all callers are guaranteed to pass a store.

---

## Dependency Graph

```
                    ┌─────────────────────────────────────────────────────────┐
                    │                    FOUNDATION LAYER                      │
                    │    (Must be done first - no dependencies)               │
                    └─────────────────────────────────────────────────────────┘
                                              │
           ┌──────────────────────────────────┼──────────────────────────────────┐
           │                                  │                                  │
           ▼                                  ▼                                  ▼
    ┌─────────────┐                   ┌─────────────┐                    ┌─────────────┐
    │   T1: CLI   │                   │  T5: model  │                    │  T7-T11:    │
    │   commands  │                   │  (TUI init) │                    │  Other CLI  │
    │  (tunnel,   │                   │             │                    │  commands   │
    │   ports,    │                   └──────┬──────┘                    └─────────────┘
    │  project,   │                          │
    │  worktree,  │                          │
    │   update)   │                          │
    └─────────────┘                          │
                                             │
                    ┌────────────────────────┼────────────────────────────┐
                    │              DEPENDENT LAYER                         │
                    │    (Require foundation changes first)               │
                    └────────────────────────┬────────────────────────────┘
                                             │
           ┌─────────────────────────────────┼─────────────────────────────────┐
           │                                 │                                 │
           ▼                                 ▼                                 ▼
    ┌─────────────┐                  ┌─────────────┐                   ┌─────────────┐
    │ T2: update  │                  │ T3: queue   │                   │ T4: setup   │
    │  (TUI msgs) │                  │ (workspace) │                   │ (workspace) │
    │             │                  │             │                   │             │
    │  Deps: T5   │                  │  Deps: T1   │                   │  Deps: T1   │
    └─────────────┘                  └─────────────┘                   └─────────────┘
                                             │
                                             │
                    ┌────────────────────────┼────────────────────────────┐
                    │               FINAL LAYER                            │
                    │    (Require all workspace changes)                  │
                    └────────────────────────┬────────────────────────────┘
                                             │
                                             ▼
                                     ┌─────────────┐
                                     │ T1: manager │
                                     │ (workspace) │
                                     │             │
                                     │ Deps: T3,T4 │
                                     └─────────────┘
```

---

## Parallelization Groups

### Group A - Can run in parallel (no dependencies)
- **T6**: `cmd/conductor/tunnel.go`
- **T7**: `cmd/conductor/ports.go`
- **T8**: `cmd/conductor/project.go`
- **T9**: `cmd/conductor/worktree.go`
- **T10**: `cmd/conductor/update.go`
- **T11**: `internal/tui/updater_helper.go`
- **T5**: `internal/tui/model.go` (partial - tunnel restore only)

### Group B - After TUI model updated (depends on T5)
- **T2**: `internal/tui/update.go`

### Group C - After manager signature updated (depends on T1 signature)
- **T3**: `internal/workspace/queue.go`
- **T4**: `internal/workspace/setup.go`

### Group D - Final (depends on T3, T4)
- **T1**: `internal/workspace/manager.go` (full migration)

---

## High Priority

### T1. `internal/workspace/manager.go` - 19 mutations, 3 saves

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T1.1 | 56 | `wt.SetupStatus = SetupStatusDone` | `SetWorktreeStatus()` | - | [x] |
| T1.2 | 108 | `wt.SetupStatus = SetupStatusCreating` | `SetWorktreeStatus()` | - | [x] |
| T1.3 | 207 | `wt.Tunnel = nil` | `ClearTunnelState()` | - | [x] |
| T1.4 | 226 | `wt.Archived = true` | `ArchiveWorktree()` | - | [x] |
| T1.5 | 227 | `wt.ArchivedAt = time.Now()` | `ArchiveWorktree()` | T1.4 | [x] |
| T1.6 | 228 | `wt.Ports = nil` | `ArchiveWorktree()` | T1.4, T1.5 | [x] |
| T1.7 | 327 | `project.GitHubOwner = owner` | `SetGitHubConfig()` | - | [x] |
| T1.8 | 328 | `project.GitHubRepo = repo` | `SetGitHubConfig()` | T1.7 | [x] |
| T1.9 | 358 | `wt.PRs = prs` | `SetWorktreePRs()` | - | [x] |
| T1.10 | 385 | `wt.PRs = prs` | `SetWorktreePRs()` | - | [x] |
| T1.11 | 448 | `config.Save(m.config)` | Auto-save | - | [x] |
| T1.12 | 539 | `config.Save(m.config)` | Auto-save | - | [x] |
| T1.13 | 593 | `wt.SetupStatus = ...` | `SetWorktreeStatus()` | - | [x] |
| T1.14 | 600 | `wt.SetupStatus = ...` | `SetWorktreeStatus()` | - | [x] |
| T1.15 | 616 | `wt.SetupStatus = ...` | `SetWorktreeStatus()` | - | [x] |
| T1.16 | 620 | `wt.SetupStatus = ...` | `SetWorktreeStatus()` | - | [x] |
| T1.17 | 628 | `wt.SetupStatus = ...` | `SetWorktreeStatus()` | - | [x] |
| T1.18 | 636 | `config.Save(m.config)` | Auto-save | - | [x] |

**Refactoring notes:**
- Manager needs to accept `*store.Store` instead of `*config.Config`
- `ArchiveWorktree()` can replace lines 226-228 (T1.4-T1.6) in one call
- `SetGitHubConfig()` can replace lines 327-328 (T1.7-T1.8) in one call
- **Dependencies**: T3, T4 must be updated first (they reference Manager)

---

### T2. `internal/tui/update.go` - 10 mutations, 11 saves

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T2.1 | 47 | `msg.Worktree.SetupStatus = SetupStatusFailed` | `SetWorktreeStatus()` | T5 | [x] |
| T2.2 | 54 | `msg.Worktree.SetupStatus = SetupStatusRunning` | `SetWorktreeStatus()` | T5 | [x] |
| T2.3 | 85 | `wt.SetupStatus = SetupStatusDone` | `SetWorktreeStatus()` | T5 | [x] |
| T2.4 | 87 | `wt.SetupStatus = SetupStatusFailed` | `SetWorktreeStatus()` | T5 | [x] |
| T2.5 | 108 | `wt.ArchiveStatus = ArchiveStatusNone` | `SetWorktreeArchiveStatus()` | T5 | [x] |
| T2.6 | 387 | `wt.Tunnel = &config.TunnelState{...}` | `SetTunnelState()` | T5 | [x] |
| T2.7 | 407 | `wt.Tunnel = nil` | `ClearTunnelState()` | T5 | [x] |
| T2.8 | 774 | `wt.SetupStatus = SetupStatusCreating` | `SetWorktreeStatus()` | T5 | [x] |
| T2.9 | 791 | `wt.SetupStatus = SetupStatusRunning` | `SetWorktreeStatus()` | T5 | [x] |
| T2.10 | 1044 | `wt.ArchiveStatus = ArchiveStatusRunning` | `SetWorktreeArchiveStatus()` | T5 | [x] |

**`config.Save()` calls to remove:** Lines 196, 237, 275, 373, 393, 408, 1006, 1058, 1081, 1102, 1392 (11 total)

**Refactoring notes:**
- TUI model needs to hold `*store.Store` instead of `*config.Config` (see T5)
- Message handlers update store, which auto-persists
- Consider using `store.GetProject()` for reads (returns copy)
- **Dependencies**: T5 (model.go must have store first)

---

## Medium Priority

### T3. `internal/workspace/queue.go` - 4 mutations, 3 saves

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T3.1 | 102 | `wt.SetupStatus = SetupStatusFailed` | `SetWorktreeStatus()` | T1 (sig) | [x] |
| T3.2 | 103 | `config.Save(job.Config)` | Auto-save | T1 (sig) | [x] |
| T3.3 | 111 | `wt.SetupStatus = SetupStatusRunning` | `SetWorktreeStatus()` | T1 (sig) | [x] |
| T3.4 | 112 | `config.Save(job.Config)` | Auto-save | T1 (sig) | [x] |
| T3.5 | 117 | `wt.SetupStatus = SetupStatusDone` | `SetWorktreeStatus()` | T1 (sig) | [x] |
| T3.6 | 119 | `wt.SetupStatus = SetupStatusFailed` | `SetWorktreeStatus()` | T1 (sig) | [x] |
| T3.7 | 121 | `config.Save(job.Config)` | Auto-save | T1 (sig) | [x] |

**Refactoring notes:**
- WorktreeJob needs `*store.Store` instead of `*config.Config`
- **Dependencies**: T1 signature change (Manager must accept store)

---

### T4. `internal/workspace/setup.go` - 3 mutations, 0 saves

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T4.1 | 100 | `wt.SetupStatus = SetupStatusRunning` | `SetWorktreeStatus()` | T1 (sig) | [x] |
| T4.2 | 131 | `wt.SetupStatus = SetupStatusDone` | `SetWorktreeStatus()` | T1 (sig) | [x] |
| T4.3 | 133 | `wt.SetupStatus = SetupStatusFailed` | `SetWorktreeStatus()` | T1 (sig) | [x] |

**Refactoring notes:**
- SetupManager needs access to store
- **Dependencies**: T1 signature change

---

### T5. `internal/tui/model.go` - 2 mutations, 1 save

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T5.1 | 198 | `wt.Tunnel = state` | `RestoreTunnelStates()` | - | [x] |
| T5.2 | 202 | `wt.Tunnel = nil` | `CleanupStaleTunnels()` | - | [x] |
| T5.3 | 210 | `config.Save(m.config)` | Auto-save | T5.1, T5.2 | [x] |

**Refactoring notes:**
- Model needs `*store.Store` field (add alongside config initially)
- Use `store.RestoreTunnelStates()` for batch restore on startup
- Use `store.CleanupStaleTunnels()` for stale tunnel cleanup
- **Dependencies**: None (foundation task)

---

### T6. `cmd/conductor/tunnel.go` - 3 mutations, 2 saves

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T6.1 | 77 | `wt.Tunnel = state` | `SetTunnelState()` | - | [x] |
| T6.2 | 78 | `config.Save(cfg)` | Auto-save | T6.1 | [x] |
| T6.3 | 127 | `wt.Tunnel = nil` | `ClearTunnelState()` | - | [x] |
| T6.4 | 128 | `config.Save(cfg)` | Auto-save | T6.3 | [x] |

**Refactoring notes:**
- CLI command needs to use `store.Load()` instead of `config.Load()`
- **Dependencies**: None (foundation task)

---

## Low Priority

### T7. `cmd/conductor/ports.go` - 1 mutation, 1 save

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T7.1 | 106 | `wt.Ports = newPorts` | `SetWorktreePorts()` | - | [x] |
| T7.2 | 110 | `config.Save(cfg)` | Auto-save | T7.1 | [x] |

**Dependencies**: None (foundation task)

---

### T8. `cmd/conductor/project.go` - 0 mutations, 2 saves

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T8.1 | 67 | `config.Save(cfg)` | `store.AddProject()` | - | [x] |
| T8.2 | 115 | `config.Save(cfg)` | `store.RemoveProject()` | - | [x] |

**Dependencies**: None (foundation task)

---

### T9. `cmd/conductor/worktree.go` - 0 mutations, 1 save

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T9.1 | 57 | `config.Save(cfg)` | Auto-save | - | [x] |

**Dependencies**: None (foundation task)

---

### T10. `cmd/conductor/update.go` - 0 mutations, 1 save

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T10.1 | 85 | `config.Save(cfg)` | `store.SetUpdateInfo()` | - | [x] |

**Dependencies**: None (foundation task)

---

### T11. `internal/tui/updater_helper.go` - 0 mutations, 2 saves

| ID | Line | Current Pattern | Store Method | Dependencies | Status |
|----|------|-----------------|--------------|--------------|--------|
| T11.1 | 24 | `config.Save(m.config)` | Auto-save | T5 | [x] |
| T11.2 | 53 | `config.Save(m.config)` | Auto-save | T5 | [x] |

**Dependencies**: T5 (model.go must have store first)

---

## Already Clean (No Work Needed)

- [x] `internal/tunnel/` - No direct mutations
- [x] `internal/runner/` - No direct mutations
- [x] `internal/updater/` - No direct mutations
- [x] `internal/store/` - This IS the store implementation

---

## Migration Checklist

### Phase 1: Foundation (Parallelizable - Group A)
- [x] T5: Update `tui.Model` to add `*store.Store` field
- [x] T6: Update `cmd/conductor/tunnel.go` to use store
- [x] T7: Update `cmd/conductor/ports.go` to use store
- [x] T8: Update `cmd/conductor/project.go` to use store
- [x] T9: Update `cmd/conductor/worktree.go` to use store
- [x] T10: Update `cmd/conductor/update.go` to use store

### Phase 2: TUI Layer (Depends on T5)
- [x] T2: Update `internal/tui/update.go` to use store
- [x] T11: Update `internal/tui/updater_helper.go` to use store

### Phase 3: Workspace Layer (Depends on T1 signature)
- [x] T1 (sig): Change `workspace.Manager` to accept `*store.Store`
- [x] T3: Update `internal/workspace/queue.go` to use store
- [x] T4: Update `internal/workspace/setup.go` to use store

### Phase 4: Final Migration
- [x] T1 (full): Complete all mutations in `workspace/manager.go`

### Phase 5: Cleanup
- [ ] Remove `workspace` from allowedPackages in mutation_scanner_test.go (optional - fallbacks remain)
- [ ] Remove `tui` from allowedPackages in mutation_scanner_test.go
- [ ] Remove `conductor` from allowedPackages in mutation_scanner_test.go
- [ ] Enable `TestNoDirectMutationsOutsideStore` test
- [x] Run full test suite with race detector: `go test -race ./...`

---

## Recommended Subagent Assignment

For parallel execution with subagents:

| Agent | Tasks | Est. Complexity |
|-------|-------|-----------------|
| Agent 1 | T6, T7, T8 | Low (CLI commands) |
| Agent 2 | T9, T10, T11 | Low (CLI + helper) |
| Agent 3 | T5 | Medium (TUI foundation) |

After Group A completes:

| Agent | Tasks | Est. Complexity |
|-------|-------|-----------------|
| Agent 4 | T2 | High (TUI update handlers) |
| Agent 5 | T3, T4 | Medium (workspace helpers) |

After Group B & C complete:

| Agent | Tasks | Est. Complexity |
|-------|-------|-----------------|
| Agent 6 | T1 | High (main manager) |

---

## Store API Reference

```go
// Worktree Status
store.SetWorktreeStatus(project, worktree, status)
store.SetWorktreeArchiveStatus(project, worktree, status)
store.ArchiveWorktree(project, worktree)  // Sets Archived, ArchivedAt, clears Ports

// Ports
store.SetWorktreePorts(project, worktree, ports)
store.ClearWorktreePorts(project, worktree)
store.AllocatePorts(project, worktree, count)
store.FreePorts(ports)

// Tunnels
store.SetTunnelState(project, worktree, state)
store.ClearTunnelState(project, worktree)
store.RestoreTunnelStates(states)
store.CleanupStaleTunnels(activeTunnels)

// GitHub
store.SetGitHubConfig(project, owner, repo)
store.SetWorktreePRs(project, worktree, prs)

// Batch (for atomic multi-step operations)
store.BatchMutate(func(cfg *config.Config) error { ... })

// Persistence
store.ForceSave()  // Bypass debounce
store.Close()      // Flush pending saves
```
