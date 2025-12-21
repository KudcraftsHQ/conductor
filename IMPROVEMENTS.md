# Conductor - Improvement Roadmap

## Project Summary

Conductor is a CLI tool for managing git worktrees across multiple projects with port isolation and environment management. Built with Go, Cobra (CLI), and Bubble Tea (TUI).

**Current Version**: 0.1.0
**Lines of Code**: ~4,600 across 25 files
**Test Coverage**: 0% (no tests exist)

---

## High Priority Improvements

### 1. Add Test Suite
**Status**: Missing entirely
**Impact**: Critical for reliability

- [ ] Unit tests for `internal/config/` (config loading, saving, validation)
- [ ] Unit tests for `internal/config/ports.go` (port allocation algorithm)
- [ ] Unit tests for `internal/workspace/manager.go` (worktree lifecycle)
- [ ] Unit tests for `internal/workspace/git.go` (git command wrappers)
- [ ] Unit tests for `internal/runner/` (script execution, env building)
- [ ] Integration tests for CLI commands
- [ ] TUI tests using Bubble Tea test helpers

### 2. Fix Error Handling Issues
**Status**: Multiple unchecked errors
**Impact**: Silent failures, potential crashes

| File | Line | Issue |
|------|------|-------|
| `cmd/conductor/ports.go` | 97 | `fmt.Sscanf` error ignored |
| `cmd/conductor/project.go` | 211 | `os.MkdirAll` error ignored |
| `internal/workspace/setup.go` | 105, 224 | `os.MkdirAll` errors ignored |
| `internal/workspace/setup.go` | 139 | Silent failure on config load |
| `internal/tui/update.go` | 419 | Potential channel deadlock |

- [ ] Add error checking to all `fmt.Sscanf` calls
- [ ] Add error checking to all `os.MkdirAll` calls
- [ ] Log or report setup script failures properly
- [ ] Add timeout handling for channel operations

### 3. Config Robustness
**Status**: No validation or atomic writes
**Impact**: Data corruption risk

- [ ] Implement atomic writes (write to temp file, then rename)
- [ ] Add config backup before modifications
- [ ] Add JSON schema validation for `conductor.json`
- [ ] Add config file versioning for future migrations
- [ ] Set proper file permissions (0600 for config files)

### 4. Complete Filter Feature
**Status**: UI exists but filter logic not implemented
**Impact**: Incomplete feature

Location: `internal/tui/view.go:128-134`

- [ ] Implement filter logic for projects list
- [ ] Implement filter logic for worktrees list
- [ ] Add filter indicator showing active filter
- [ ] Add clear filter functionality

### 5. Setup Timeout Handling
**Status**: No timeout for setup scripts
**Impact**: Hung processes, resource leaks

- [ ] Add configurable timeout for setup scripts
- [ ] Add ability to cancel running setup from TUI
- [ ] Persist setup status to disk (survives app restart)
- [ ] Add cleanup for orphaned setup processes

---

## Medium Priority Improvements

### 6. Documentation
**Status**: No documentation exists
**Impact**: Adoption barrier

- [ ] Create README.md with:
  - Installation instructions
  - Quick start guide
  - Configuration reference
  - Command reference
  - Screenshots of TUI
- [ ] Add inline code comments for complex algorithms
- [ ] Document the port allocation strategy
- [ ] Add CONTRIBUTING.md for contributors

### 7. Git Status in TUI
**Status**: Not implemented
**Impact**: Missing useful information

- [ ] Show current branch for each worktree
- [ ] Show dirty/clean status indicator
- [ ] Show ahead/behind remote counts
- [ ] Add ability to fetch/pull from TUI

### 8. Improved Error Messages
**Status**: Generic errors
**Impact**: Poor debugging experience

- [ ] Add contextual error messages with suggestions
- [ ] Include file paths in file-related errors
- [ ] Add "did you mean?" for typos in commands
- [ ] Show recovery steps for common failures

### 9. Custom Port Ranges
**Status**: Hardcoded 3100-3999
**Impact**: Limited flexibility

- [ ] Add per-project port range configuration
- [ ] Add global default port range setting
- [ ] Validate port range doesn't conflict with system ports
- [ ] Add port availability check before allocation

### 10. Performance Optimizations
**Status**: Some O(n) operations
**Impact**: Slow with many projects/worktrees

- [ ] Use map for port lookups instead of linear scan
- [ ] Cache config in memory, avoid repeated disk reads
- [ ] Build path→project index for faster detection
- [ ] Add pagination for large worktree lists
- [ ] Move git operations to background goroutines

---

## Low Priority Improvements

### 11. Fix Deprecation Warnings
**Status**: Using deprecated APIs
**Impact**: Future compatibility

- [ ] Replace `rand.Seed()` in `cities.go:28` with `rand.New(rand.NewSource(...))`

### 12. Shell Completions
**Status**: Basic completion command exists
**Impact**: Developer experience

- [ ] Test and document bash completion
- [ ] Test and document zsh completion
- [ ] Test and document fish completion
- [ ] Add to installation instructions

### 13. Log Management
**Status**: Logs accumulate without cleanup
**Impact**: Disk usage

- [ ] Implement log rotation
- [ ] Add configurable log retention period
- [ ] Add command to clean old logs
- [ ] Compress old log files

### 14. Multi-Project Conflict Detection
**Status**: No collision detection
**Impact**: Potential path conflicts

- [ ] Detect duplicate project names
- [ ] Warn on similar project paths
- [ ] Validate worktree paths don't overlap

### 15. Additional IDE/Terminal Support
**Status**: Limited to specific apps
**Impact**: User preference

- [ ] Add Sublime Text support
- [ ] Add IntelliJ IDEA support
- [ ] Add Alacritty terminal support
- [ ] Add Kitty terminal support
- [ ] Make opener configurable per-project

---

## Security Improvements

### 16. Input Validation
**Status**: Limited validation
**Impact**: Security risk

- [ ] Validate project names (no path traversal)
- [ ] Validate branch names (valid git branch chars)
- [ ] Sanitize script paths
- [ ] Validate port numbers (1024-65535 range)

### 17. Script Execution Safety
**Status**: Scripts run with full privileges
**Impact**: Security risk

- [ ] Add script execution confirmation option
- [ ] Log all script executions
- [ ] Consider sandboxing options
- [ ] Filter sensitive environment variables

---

## Feature Ideas (Future)

### 18. Project Templates
- [ ] Allow creating project templates
- [ ] Share templates between users
- [ ] Template variables (project name, etc.)

### 19. Remote Sync
- [ ] Sync conductor config across machines
- [ ] Conflict resolution for port allocations

### 20. Hooks System
- [ ] Pre/post worktree creation hooks
- [ ] Pre/post archive hooks
- [ ] Custom notification hooks

### 21. Status Dashboard
- [ ] Show all running dev servers
- [ ] Quick access to open in browser
- [ ] Health check indicators

---

## Quick Wins (Can Do Today)

1. Fix `rand.Seed()` deprecation - 5 min
2. Add error checking to `os.MkdirAll` calls - 15 min
3. Add error checking to `fmt.Sscanf` - 5 min
4. Create basic README.md - 30 min
5. Add `.gitignore` if missing - 5 min

---

## Discussion Points

1. **Test framework choice**: Standard `testing` package vs testify vs ginkgo?
2. **Documentation format**: README only vs dedicated docs site?
3. **Port range**: Should default range be configurable globally?
4. **Breaking changes**: OK to change config format for v0.2.0?
5. **CI/CD**: GitHub Actions for testing and releases?

---

## Priority Matrix

| Effort | High Impact | Low Impact |
|--------|-------------|------------|
| **Low** | Fix error handling, Deprecation fixes | Shell completions |
| **High** | Test suite, Documentation | Remote sync, Templates |

---

*Last updated: 2025-12-20*
