# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.12.25.4] - 2025-12-25

### Fixed
- **Navigation cursor preservation**: Cursor position now preserved when navigating back from child views
  - Pressing Escape in worktrees view returns to the same project in the projects list
  - Pressing Escape in PRs view returns to the same worktree
  - Pressing Escape in logs view returns to the same worktree

## [0.12.25.3] - 2025-12-25

### Fixed
- **Worktree branch creation**: Worktrees now correctly checkout the specified remote branch instead of always using the default branch (main/master)
  - When creating a worktree for an existing remote branch, it now fetches and bases the worktree on `origin/<branch>`
  - Falls back to default branch only when the specified branch doesn't exist on the remote

## [0.12.25.2] - 2025-12-25

### Added
- **Auto-update system**: Conductor now automatically checks for and installs updates
  - Checks for updates on TUI startup and every 6 hours while running
  - New `conductor update` command to manually check and install updates
  - New `conductor update --check` to check without installing
  - New `conductor migrate` command to move installation to user directory for auto-updates
  - Configurable via `~/.conductor/conductor.json` (autoCheck, autoDownload, notifyInTUI)
  - Downloads from GitHub Releases for seamless updates

### Changed
- Installation now defaults to `~/.local/bin` to support auto-updates without sudo
- Fixed GitHub API URL to point to correct repository (KudcraftsHQ/conductor)

## [0.12.25.1] - 2025-12-25

### Added
- **Auto-setup for Claude PRs**: Automatically create worktrees for GitHub PRs with `claude/*` branch prefix
  - New `conductor pr auto-setup [project]` CLI command
  - New `A` keybinding in TUI to trigger auto-setup from worktrees view
  - Fetches open PRs via `gh` CLI and creates worktrees with setup scripts
  - Skips PRs that already have worktrees
  - Periodic background scanning every 30 seconds while TUI is running
- **Retry failed setups**: New `R` keybinding to retry failed worktree setups

### Fixed
- **Worktree status persistence**: `SetupStatus` and `ArchiveStatus` are now persisted to JSON, so failed worktrees remain marked as failed after restarting Conductor

## [0.12.24] - 2024-12-24

### Added
- **GitHub PR Integration**: View pull requests for worktree branches via `gh` CLI
  - New `m` keybinding in TUI to view merge requests/PRs
  - Automatic GitHub owner/repo detection from git remote
  - PR state display (open, closed, merged, draft)
- **Tmux Integration**: TUI now runs within tmux sessions
  - Automatic tmux session management
  - Dependency check for tmux on startup
- **New TUI Keybindings**:
  - `c` - Create worktree
  - `a` - Archive worktree
  - `t` - Open in terminal
  - `m` - View merge requests/PRs
  - `p` - View ports
  - `r` / `Ctrl+R` - Refresh
  - `/` - Filter
  - `C` (uppercase) - Open in Cursor
  - `V` (uppercase) - Open in VS Code
- **Modal Overlay System**: Improved dialogs for create, delete, and help views
- **GitHub Actions**: CI and release workflows

### Changed
- Updated README with accurate keybindings and requirements
- Improved TUI navigation and status display
- Enhanced worktree status tracking

### Fixed
- All lint issues resolved
- Added pre-commit hook for code quality

## [0.1.0] - 2024-12-20

### Added
- Initial release
- Git worktree management with automatic port allocation
- Interactive TUI with Bubble Tea
- CLI commands for project, worktree, and port management
- Setup/run/archive script execution with environment injection
- IDE integration (Cursor, VS Code, Zed)
- Terminal integration (iTerm2, Terminal.app, WezTerm)
- City-based worktree naming (Tokyo, Paris, London, etc.)
- Port range configuration (default: 3100-3999)
- Labeled port support for multi-port setups
