# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.4.0] - 2026-01-04

### Changed
- **Simplified Named Tunnel Authentication**: Named tunnels now use `cloudflared tunnel login` instead of manual API token setup
  - No more manual API token, account ID, or zone ID configuration
  - Uses existing cloudflared authentication from `~/.cloudflared/cert.pem`
  - Just run `cloudflared tunnel login` once and configure your domain
  - TUI tunnel modal now shows authentication status and helpful hints
  - Deprecated `cloudflareToken`, `accountId`, `zoneId` config fields (kept for backwards compatibility)

### Added
- **Tunnel Package Tests**: Comprehensive test coverage for the tunnel package
  - Tests for tunnel config (ingress rules, hostname generation)
  - Tests for PID file operations and process management
  - Tests for cloudflared CLI wrapper (CI-safe with skip when not installed)
  - Tests for tunnel manager helper functions

## [1.1.3.0] - 2026-01-03

### Added
- **Cloudflare Tunnel Support**: Expose worktree dev servers to the internet via Cloudflare tunnels
  - **Quick tunnels**: Random `*.trycloudflare.com` URLs with no setup required
  - **Named tunnels**: Custom domains like `tokyo-3100.yourdomain.com` via Cloudflare API
  - New `T` keybind in TUI to toggle tunnel for selected worktree
  - New `y` keybind to copy tunnel URL to clipboard
  - Tunnel URL displayed in worktrees table
  - Tunnel state persists across TUI restarts via PID files
  - Automatic tunnel cleanup when archiving worktrees
  - New CLI commands: `conductor tunnel start|stop|list|status|logs|setup`
  - Environment variables injected into scripts: `CONDUCTOR_TUNNEL_URL`, `CONDUCTOR_TUNNEL_PORT`, `CONDUCTOR_TUNNEL_MODE`

## [0.12.25.7] - 2025-12-25

### Added
- **Create worktree from PR**: Create a worktree directly from any PR in the PR list view
  - New `w` keybinding in PR view to create a worktree for the selected PR's branch
  - New `WORKTREE` column in PR table showing existing worktree names for each PR's branch
  - Automatically navigates back to worktrees view after creation
  - Shows helpful message if worktree already exists for the branch

## [0.12.25.6] - 2025-12-25

### Fixed
- **Nil pointer crash fix**: Prevent panic when worktree is deleted in background but view tries to access it before the list is refreshed

## [0.12.25.5] - 2025-12-25

### Added
- **Git status indicators in worktree table**: Quickly see if a worktree is ready to be archived
  - `[dirty]` tag (yellow) - Shows when worktree has uncommitted changes
  - `[behind N]` tag (blue) - Shows when worktree is N commits behind main/master branch
  - Status is fetched asynchronously when entering a project and on refresh ('r')
  - Clean worktrees with no tags are ready to archive

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
