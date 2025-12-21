# Conductor

A CLI tool for managing git worktrees across multiple projects with automatic port isolation and environment management.

> **Heavily inspired by [Coolify](https://coolify.io/)** - the open-source & self-hostable Heroku/Netlify alternative. Conductor brings similar developer experience principles to local worktree management.

## Why Conductor?

When working on multiple features or bug fixes simultaneously, git worktrees are incredibly useful. However, managing them manually becomes tedious:

- Each worktree needs its own port allocations to avoid conflicts
- Environment variables need to be set up for each workspace
- Setup scripts need to run for dependencies
- Tracking which ports belong to which worktree is error-prone

Conductor solves these problems by:

- **Automatically allocating ports** for each worktree from a configurable range
- **Injecting environment variables** with port and workspace information
- **Running setup/run/archive scripts** automatically
- **Providing a TUI** for easy management of all your projects and worktrees

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/hammashamzah/conductor.git
cd conductor

# Build and install
make install
```

This installs `conductor` to your `$GOPATH/bin`. Make sure it's in your PATH.

### Build Options

```bash
make build          # Build for current platform
make build-all      # Build for Linux, macOS, and Windows
make install        # Install to $GOPATH/bin
make install-global # Install to /usr/local/bin (requires sudo)
```

## Quick Start

```bash
# Initialize conductor (creates ~/.conductor/)
conductor init

# Add your project
cd /path/to/your/project
conductor project add .

# Create a worktree for a new feature
conductor worktree create feature-auth

# Or launch the interactive TUI
conductor
```

## Usage

### Interactive TUI

Run `conductor` without arguments to launch the interactive terminal UI:

```bash
conductor
```

**Navigation:**
- `↑/↓` or `j/k` - Navigate lists
- `Tab` - Switch between Projects, Worktrees, and Ports views
- `Enter` - Select/Open
- `n` - New worktree
- `d` - Delete (with confirmation)
- `o` - Open in terminal
- `c` - Open in Cursor
- `v` - Open in VS Code
- `l` - View setup logs
- `?` - Help
- `q` - Quit

### CLI Commands

#### Project Management

```bash
# Add current directory as a project
conductor project add .

# Add with custom port count per worktree
conductor project add . --ports 3

# List all projects
conductor project list

# Show project details
conductor project show <project-name>

# Remove a project (doesn't delete files)
conductor project remove <project-name>

# Initialize conductor.json in project
conductor project init
```

#### Worktree Management

```bash
# Create a new worktree (generates random city name)
conductor worktree create

# Create with specific branch name
conductor worktree create feature-auth

# List worktrees for current project
conductor worktree list

# Open worktree in terminal with split panes
conductor worktree open tokyo

# Open in specific IDE
conductor worktree open tokyo --cursor
conductor worktree open tokyo --vscode
conductor worktree open tokyo --zed

# Archive (delete) a worktree
conductor worktree archive tokyo

# Show worktree status
conductor worktree status
```

#### Port Management

```bash
# List all allocated ports
conductor ports list

# Filter by project
conductor ports list --project myproject

# Manually free a port (use with caution)
conductor ports free 3100
```

#### Scripts

```bash
# Run setup script
conductor setup

# Run dev server
conductor run

# Show current worktree status and environment
conductor status
```

## Configuration

### Global Configuration

Conductor stores its configuration in `~/.conductor/conductor.json`:

```json
{
  "version": 1,
  "defaults": {
    "portsPerWorktree": 1,
    "portRangeStart": 3100,
    "portRangeEnd": 3999,
    "openWith": "iterm",
    "ideCommand": "cursor"
  },
  "portAllocations": {},
  "projects": {}
}
```

### Project Configuration

Create a `conductor.json` in your project root:

```json
{
  "scripts": {
    "setup": "npm install && prisma migrate deploy",
    "run": "npm run dev",
    "archive": "docker-compose down"
  },
  "ports": {
    "default": 3,
    "labels": ["web", "api", "db"]
  }
}
```

Or use external scripts in `.conductor-scripts/`:

```
.conductor-scripts/
├── setup.sh
├── run.sh
└── archive.sh
```

External scripts take precedence over inline scripts.

### Environment Variables

Conductor injects these environment variables when running scripts:

| Variable | Description | Example |
|----------|-------------|---------|
| `CONDUCTOR_PROJECT_NAME` | Project name | `myproject` |
| `CONDUCTOR_WORKSPACE_NAME` | Worktree name | `tokyo` |
| `CONDUCTOR_ROOT_PATH` | Project root path | `/path/to/project` |
| `CONDUCTOR_WORKTREE_PATH` | Worktree path | `~/.conductor/myproject/tokyo` |
| `CONDUCTOR_IS_ROOT` | Is root worktree | `true` or `false` |
| `CONDUCTOR_BRANCH` | Git branch | `feature-auth` |
| `CONDUCTOR_PORT` | Primary port | `3100` |
| `PORT` | Alias for primary port | `3100` |
| `CONDUCTOR_PORT_COUNT` | Number of ports | `3` |
| `CONDUCTOR_PORTS` | All ports (comma-separated) | `3100,3101,3102` |
| `CONDUCTOR_PORT_0` | First port | `3100` |
| `CONDUCTOR_PORT_1` | Second port | `3101` |
| `CONDUCTOR_PORT_WEB` | Labeled port (if configured) | `3100` |

## How It Works

### Port Allocation

Conductor allocates consecutive ports from a configurable range (default: 3100-3999):

1. When you create a worktree, Conductor finds the first gap of N consecutive free ports
2. Ports are tracked globally across all projects
3. When a worktree is archived, its ports are freed for reuse

### Worktree Naming

By default, worktrees are named after cities (tokyo, paris, london, etc.) for easy identification. You can also specify a branch name when creating a worktree.

### Directory Structure

```
~/.conductor/
├── conductor.json          # Global configuration
├── myproject/
│   ├── tokyo/              # Worktree directory
│   └── paris/              # Another worktree
└── logs/
    └── myproject/
        ├── tokyo-setup.log
        └── tokyo-archive.log
```

## IDE Integration

Conductor supports opening worktrees in:

- **Cursor** (`--cursor` or `c` in TUI)
- **VS Code** (`--vscode` or `v` in TUI)
- **Zed** (`--zed`)
- **Neovim** (configured via `ideCommand`)

Terminal support:

- **iTerm2** (macOS, with split panes)
- **Terminal.app** (macOS)
- **WezTerm**

## Example Workflow

```bash
# 1. Initialize conductor
conductor init

# 2. Register your project
cd ~/projects/myapp
conductor project add . --ports 2

# 3. Initialize project scripts
conductor project init

# 4. Edit conductor.json with your scripts
# 5. Create a worktree for a feature
conductor worktree create feature-auth
# -> Creates worktree "tokyo" at ~/.conductor/myapp/tokyo
# -> Allocates ports 3100, 3101
# -> Runs setup script

# 6. Open in your IDE
conductor worktree open tokyo --cursor

# 7. Start development
cd ~/.conductor/myapp/tokyo
conductor run
# -> Runs with PORT=3100, CONDUCTOR_PORT_0=3100, CONDUCTOR_PORT_1=3101

# 8. When done, archive the worktree
conductor worktree archive tokyo
# -> Runs archive script
# -> Removes git worktree
# -> Frees ports 3100, 3101
```

## Development

### Requirements

- Go 1.21+

### Building

```bash
make build      # Build binary
make test       # Run tests
make lint       # Run linter (requires golangci-lint)
make fmt        # Format code
```

### Running Tests

```bash
go test ./...

# With coverage
go test -cover ./...
```

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
