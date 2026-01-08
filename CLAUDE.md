# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
make build          # Build for current platform → build/conductor
make dev            # Build and run TUI
make test           # Run all tests with -v
make lint           # Run golangci-lint (install: brew install golangci-lint)
make fmt            # Format code with go fmt
make install        # Install to $GOPATH/bin
```

Run a single test:
```bash
go test -v ./internal/config -run TestPortAllocation
```

## Architecture Overview

Conductor is a CLI/TUI tool for managing git worktrees with automatic port isolation. It uses **Cobra** for CLI commands and **Bubble Tea** for the terminal UI.

### Package Structure

```
cmd/conductor/     CLI commands (Cobra) - entry point is main.go
internal/
├── config/        Configuration management & port allocation
├── workspace/     Worktree lifecycle (create, archive, git ops)
├── tui/           Terminal UI (Bubble Tea model/update/view)
├── runner/        Script execution with environment injection
├── opener/        IDE & terminal launchers (Cursor, VS Code, iTerm)
├── tmux/          Tmux session management
├── tunnel/        Cloudflare tunnel management (quick & named tunnels)
└── github/        GitHub PR integration via gh CLI
```

### Key Data Flow

1. **Config** (`~/.conductor/conductor.json`) is the source of truth for all projects, worktrees, and port allocations
2. **Worktree lifecycle**: `PrepareWorktree()` → `CreateWorktreeAsync()` (git worktree add) → `RunSetupAsync()` (background script)
3. **Port allocation**: O(n) scan for first consecutive gap of N free ports in range 3100-3999
4. **TUI pattern**: Bubble Tea message-driven updates - background goroutines send messages back to UI

### Worktree States

```
Creating → Running (setup script) → Done/Failed → Archived
```

### Configuration Files

- **Global**: `~/.conductor/conductor.json` - projects, worktrees, port allocations
- **Project-level**: `<project-root>/conductor.json` - scripts (setup/run/archive), port labels
- **Logs**: `~/.conductor/logs/<project>/<worktree>-setup.log`

### Environment Variables Injected

Scripts receive: `CONDUCTOR_PROJECT_NAME`, `CONDUCTOR_WORKTREE_PATH`, `CONDUCTOR_PORT`, `PORT`, `CONDUCTOR_PORTS` (comma-separated), `CONDUCTOR_PORT_0/1/2...`, labeled ports like `CONDUCTOR_PORT_WEB`

## Key Patterns

- **Async operations**: Worktree creation and setup scripts run in background goroutines, sending result messages to TUI
- **City naming**: Worktrees get random city names (Tokyo, Paris, London) via `workspace/cities.go`
- **Port tracking**: Global `PortAllocations` map in config tracks all assigned ports across projects

## Testing

Tests use `testify` for assertions. Test files exist in:
- `internal/config/config_test.go` - config and port allocation
- `internal/config/ports_test.go` - port allocation algorithm
- `internal/runner/env_test.go` - environment variable building
- `internal/workspace/cities_test.go` - city name generation
- `internal/tunnel/config_test.go` - tunnel config and ingress rules
- `internal/tunnel/process_test.go` - PID file operations
- `internal/tunnel/cloudflared_cli_test.go` - cloudflared CLI wrapper
- `internal/tunnel/manager_test.go` - tunnel manager functions

## Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/charmbracelet/bubbles` - UI components (spinner, help, textinput)
- `github.com/charmbracelet/lipgloss` - Terminal styling
- `github.com/spf13/cobra` - CLI framework
- `github.com/stretchr/testify` - Testing assertions

## Versioning

Version format: `YEAR.MONTH.DAY.PATCH`

- **YEAR**: Offset from 2025 (2025=0, 2026=1, 2027=2, etc.)
- **MONTH**: 1-12
- **DAY**: Day of the month
- **PATCH**: Incremental release number for that day (starts at 0)

Examples:
- `0.12.25.0` = First release on Dec 25, 2025
- `0.12.25.3` = Fourth release on Dec 25, 2025
- `1.1.15.0` = First release on Jan 15, 2026

## Releasing New Features

When releasing a new feature or fix:

1. **Update version numbers** in all locations:
   - `Makefile` - Update `VERSION ?= X.X.X.X`

2. **Update CHANGELOG.md**: Add a new version entry following Keep a Changelog format
   - Check `git log` to determine the correct version number (increment PATCH)
   - Document all changes under appropriate headers (Added, Fixed, Changed, etc.)

3. **Update README.md**: If the feature adds new keybindings or user-facing changes
   - Update the Navigation section with new keybindings
   - Update any relevant documentation sections

4. **Commit and push** with a descriptive release message

5. **Create and push the git tag** to trigger the release CI:
   ```bash
   git tag -a vX.X.X.X -m "Release vX.X.X.X - Description"
   git push origin vX.X.X.X
   ```
   The release workflow (`.github/workflows/release.yml`) only triggers on tag pushes matching `v*`.
