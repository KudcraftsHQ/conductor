# Conductor

A CLI tool for managing git worktrees across multiple projects with automatic port isolation and environment management.

> **Heavily inspired by [Conductor.build](https://conductor.build/)** - the Mac app for running multiple AI coding agents in isolated workspaces. This CLI brings similar git worktree isolation and management to the command line.

![Conductor TUI Screenshot](images/screenshot.png)

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

## Requirements

- **A terminal multiplexer** — either [tmux](https://github.com/tmux/tmux) (default) or [herdr](https://herdr.dev). See [Choosing a multiplexer](#choosing-a-multiplexer).
- **git** - For worktree operations
- **gh** (optional) - GitHub CLI for PR integration
- **cloudflared** (optional) - For Cloudflare tunnel support

## Installation

### From Source (Recommended)

```bash
# Clone the repository
git clone https://github.com/KudcraftsHQ/conductor.git
cd conductor

# Build and install to ~/.local/bin (supports auto-updates)
make install
```

This installs `conductor` to `~/.local/bin`, which enables automatic updates without requiring sudo.

**Make sure `~/.local/bin` is in your PATH:**

```bash
# Add to ~/.zshrc or ~/.bashrc
export PATH="$HOME/.local/bin:$PATH"

# Then reload your shell
source ~/.zshrc  # or source ~/.bashrc
```

### Build Options

```bash
make build          # Build for current platform
make build-all      # Build for Linux, macOS, and Windows
make install        # Install to ~/.local/bin (recommended, supports auto-updates)
make install-global # Install to /usr/local/bin (requires sudo, disables auto-updates)
```

### Auto-Updates

Conductor automatically checks for updates on every launch and downloads them in the background. Updates are seamless and require no user intervention.

```bash
# Manually check for updates
conductor update

# Check without installing
conductor update --check

# Disable auto-updates (add to ~/.conductor/conductor.json)
{
  "updates": {
    "autoCheck": false
  }
}
```

**Migrating from System Installation:**

If you previously installed to `/usr/local/bin`, migrate to enable auto-updates:

```bash
conductor migrate
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
- `c` - Create worktree
- `a` - Archive worktree
- `d` - Delete (with confirmation)
- `o` - Open in terminal
- `t` - Open in terminal
- `C` - Open in Cursor
- `V` - Open in VS Code
- `l` - View logs
  - In logs view: `t` to toggle between setup/archive logs (archived worktrees only)
- `R` - Retry failed setup
- `m` - View merge requests/PRs
- `w` - Create worktree from PR (in PR view)
- `A` - Auto-setup Claude PRs
- `T` - Toggle tunnel for worktree
- `y` - Copy tunnel URL to clipboard
- `D` - View archived worktrees and orphaned branches
- `H` - View status message history
- `p` - View ports
- `r` - Refresh
- `/` - Filter
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

#### Database Lifecycle

Conductor provides database synchronization to give each worktree an isolated database clone. This is useful for projects using PostgreSQL where you want to test migrations or work with production-like data.

```bash
# 1. Set up your source database (production/staging)
conductor database set-source "postgresql://user:pass@host:5432/mydb"

# 2. Sync to create a local "golden" copy
conductor database sync

# 3. Clone the golden copy to a worktree
conductor database clone --worktree tokyo

# 4. Reinitialize after rebasing (drops and re-clones)
conductor database reinit --worktree tokyo

# 5. Check sync status
conductor database status

# 6. List all worktree databases
conductor database list
```

**Architecture:**

```
┌─────────────────┐    sync     ┌─────────────────┐    clone    ┌──────────────────┐
│   Source DB     │ ─────────▶  │   Golden DB     │ ─────────▶  │  Worktree DBs    │
│ (prod/staging)  │             │ (myapp_golden)  │             │ (myapp-3100, etc)│
└─────────────────┘             └─────────────────┘             └──────────────────┘
```

- **Source DB**: Your production or staging PostgreSQL database
- **Golden DB**: A local copy synced from source, stored in your local PostgreSQL as `{project}_golden`
- **Worktree DBs**: Individual database clones for each worktree, named `{project}-{port}`

**Commands:**

| Command | Description |
|---------|-------------|
| `database set-source <url>` | Configure source database connection |
| `database set-local <url>` | Configure local PostgreSQL connection |
| `database sync` | Sync source → golden (incremental, with progress) |
| `database clone` | Clone golden → worktree database |
| `database reinit` | Drop and re-clone worktree database |
| `database drop` | Drop a worktree database |
| `database status` | Show sync status and golden DB info |
| `database list` | List all worktree databases |
| `database analyze` | Analyze source tables for exclusion suggestions |
| `database migration-status` | Check Prisma migration compatibility |
| `database check-freshness` | Check if golden needs resync |

**Table Exclusions:**

Large tables (audit logs, events) can be excluded from data sync while keeping their schema:

```bash
conductor database set-source "postgresql://..." --exclude=audit_logs,events
```

#### Cloudflare Tunnels

Expose your local dev server to the internet via Cloudflare tunnels:

```bash
# Quick tunnel (random URL, no setup required)
conductor tunnel start tokyo

# Named tunnel (custom domain)
conductor tunnel start tokyo --named

# Stop a tunnel
conductor tunnel stop tokyo

# List active tunnels
conductor tunnel list

# View tunnel logs
conductor tunnel logs tokyo

# Setup guide
conductor tunnel setup
```

**Quick Tunnels** require no configuration - just start one and get a random `*.trycloudflare.com` URL.

**Named Tunnels** require one-time setup:

```bash
# 1. Install cloudflared
brew install cloudflared

# 2. Login to Cloudflare (opens browser)
cloudflared tunnel login

# 3. Configure your domain in conductor
# Add to ~/.conductor/conductor.json:
{
  "defaults": {
    "tunnel": {
      "domain": "yourdomain.com"
    }
  }
}
```

Named tunnel URLs follow the pattern: `<worktree>-<port>.<domain>` (e.g., `tokyo-3100.yourdomain.com`)

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
    "ideCommand": "cursor",
    "multiplexer": "auto"
  },
  "portAllocations": {},
  "projects": {}
}
```

### Choosing a multiplexer

Conductor provisions worktrees, ports and databases; the terminal multiplexer is
the layer that hosts the coding-agent and dev-server panes. Two are supported:

| Value | Behaviour |
|-------|-----------|
| `"auto"` (default) | Uses herdr when conductor is running inside a herdr pane, or when herdr is installed and tmux is not. Otherwise uses tmux. |
| `"tmux"` | Always use tmux. Each worktree becomes a tmux window named `project/branch`. |
| `"herdr"` | Always use [herdr](https://herdr.dev). Each worktree becomes a herdr workspace labelled `project/branch`. |

Set it in `~/.conductor/conductor.json` under `defaults.multiplexer`, or override
per-invocation with the `CONDUCTOR_MUX` environment variable:

```bash
CONDUCTOR_MUX=herdr conductor
```

Either way the pane layout is the same: coding agent on the left, dev server on
the right. Under tmux, conductor annotates window names with agent status icons;
under herdr this is skipped because herdr detects and renders agent status itself.

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
| `CONDUCTOR_TUNNEL_ACTIVE` | Tunnel is active | `true` or `false` |
| `CONDUCTOR_TUNNEL_URL` | Tunnel URL | `https://tokyo-3100.example.com` |
| `CONDUCTOR_TUNNEL_PORT` | Tunneled port | `3100` |
| `CONDUCTOR_TUNNEL_MODE` | Tunnel mode | `quick` or `named` |

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

## Chat-Originated Tasks (`conductor orchestrate`)

`conductor orchestrate` is the machine-facing entry point used by the
Herdr/Hermes bridge: a Discord message asks for work on a registered project,
and Conductor answers with a coding agent running in a **brand-new worktree**.
Every subcommand takes `--json` and none of them prompt.

```bash
# Start work. --request-id is the originating message id, and it is required.
conductor orchestrate launch --project myapp \
  --prompt "have a look at why the uploader keeps dying" \
  --request-id 1402998877665544332 --channel-id C --thread-id T --json

# Sample live agent state and record any real progress (all live tasks if no id).
conductor orchestrate observe [task-id] [--json]

# Record what the completion message needs.
conductor orchestrate tests <task-id> --status passed --detail "go test ./... — ok"
conductor orchestrate summary <task-id> "dropped the double fetch"
conductor orchestrate readback <task-id> --slug herdr-<task-id> --url <printed-url>

# Answer the Readback question, then post the completion.
conductor orchestrate gate <task-id> yes|no
conductor orchestrate complete <task-id> [--json]

conductor orchestrate status <task-id>
conductor orchestrate list
```

### What it guarantees

- **A fresh worktree, always.** The agent never runs in the project's root
  checkout; a launch that would land there is refused rather than run.
- **Unattended agent flags.** Claude Code always starts with
  `--dangerously-skip-permissions` and `CLAUDE_CODE_NO_FLICKER=1` — there is no
  human in the pane to answer a permission prompt. The launch fails loudly if
  those flags are ever missing from the argv.
- **Launch returns on dispatch, not completion.** The caller is a Discord
  interaction, so launch has a budget to *confirm the agent started* (default
  20s after readiness) and never a budget to watch it run. A three-hour task
  and a three-second task return in the same time.
- **Real progress only.** Progress events come from Herdr observations —
  an agent status change, or its `state_change_seq` moving. Polling a quiet
  agent a hundred times records nothing, so "no progress for an hour" is a fact
  about the agent rather than an artefact of polling.
- **Idempotency.** The originating message id is the key. Redelivering the same
  Discord message returns the running task; it never creates a second worktree,
  a second agent, or a second copy of the prompt. A launch that crashed before
  producing an agent is retried under the same task id.
- **Recovery.** State lives in `~/.conductor/orchestration/tasks.json`, so a
  restarted process re-attaches to whatever is running. A Herdr outage is a
  disconnect, not a lost agent; only a persistently unreadable pane — or one
  whose terminal id changed, meaning it was recycled — is reported lost, and a
  lost agent is never silently relaunched.

### The Readback gate

Readback is an **optional completion gate, not an automatic blocker**. Whether a
task owes a write-up is modelled explicitly and persisted, so a restart neither
forgets an outstanding question nor asks it twice:

| Gate | Meaning | Set by |
|---|---|---|
| `readback_required` | The request asked for a report, research, an audit or a Readback | classifier at launch, `--readback required`, or the requester |
| `no_readback_needed` | Clearly code-only work; completes without a document | classifier at launch, `--readback not-needed`, or the requester |
| `awaiting_readback_decision` | The request did not say; the requester is asked **once**, when the agent finishes | classifier at launch |

While the gate is open the task is simply **not reported complete**. Asking is
not blocking: the agent keeps running, the worktree is untouched, and nothing
is cleaned up. `conductor orchestrate observe` surfaces the question to put to
the requester exactly once; `conductor orchestrate gate <task> yes|no` records
their answer, which outranks the classifier and is never re-derived.

A task whose gate is `readback_required` completes only once a published URL is
recorded — and the URL is always the one the `readback` CLI printed, never one
constructed from a slug.

### The completion message

```
**Task complete** — `myapp-a1b2c3`
project `myapp` · branch `edinburgh` · worktree `~/.conductor/myapp/edinburgh`
tests: passed — go test ./... — ok
Report: https://notes.kudcrafts.com/d/herdr-myapp-a1b2c3 (slug `herdr-myapp-a1b2c3`)
nil multipart boundary killed the uploader; patched and covered
```

Tests are reported as `unknown` unless something recorded them. Conductor does
not run the agent's tests, and saying "passed" because nothing said otherwise
is the claim this contract exists to prevent.

## IDE Integration

Conductor supports opening worktrees in:

- **Cursor** (`--cursor` or `C` in TUI)
- **VS Code** (`--vscode` or `V` in TUI)
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

- Go 1.24+

### Architecture: Store-Based State Management

Conductor uses a centralized Store pattern for all configuration state management (`internal/store/`). This provides thread-safe access to `~/.conductor/conductor.json` with automatic persistence.

**Key Features:**

- **Thread-safe**: RWMutex-based concurrency - multiple readers, exclusive writers
- **Auto-persistence**: Changes are automatically saved with 100ms debouncing to batch rapid mutations
- **Copy-on-read**: All getters return deep copies to prevent external mutations
- **Retry logic**: Exponential backoff (up to 3 retries) for transient save failures

**Usage Pattern:**

```go
// Load the store (typically once at startup)
store, err := store.Load()
defer store.Close()  // Flushes pending saves

// Read operations (thread-safe, returns copies)
project := store.GetProject("myproject")
worktrees := store.ListWorktrees("myproject")
ports := store.GetWorktreePorts("myproject", "tokyo")

// Write operations (auto-saved with debouncing)
store.AddProject(project)
store.SetWorktreeStatus("myproject", "tokyo", config.StatusDone)
store.AllocatePorts("myproject", "tokyo", []int{3100, 3101})

// Batch mutations (single lock acquisition)
store.BatchMutate(func(cfg *config.Config) error {
    cfg.Projects["myproject"].Worktrees["tokyo"].Status = "done"
    cfg.PortAllocations[3100] = "myproject/tokyo"
    return nil
})

// Force immediate save (bypasses debounce)
store.ForceSave()
```

**Store Methods:**

| Category | Methods |
|----------|---------|
| Projects | `GetProject`, `AddProject`, `RemoveProject`, `ListProjects`, `ProjectExists` |
| Worktrees | `GetWorktree`, `AddWorktree`, `RemoveWorktree`, `SetWorktreeStatus`, `ArchiveWorktree`, `ListWorktrees` |
| Ports | `GetWorktreePorts`, `AllocatePorts`, `FreePorts`, `IsPortAvailable`, `GetAllPortAllocations` |
| Tunnels | `GetTunnelState`, `SetTunnelState`, `ClearTunnelState`, `IsTunnelActive` |
| Settings | `GetDefaults`, `GetTunnelDomain`, `GetOpenWith`, `GetIDECommand` |
| Recovery | `RecoverInterruptedWorktrees`, `CleanupStaleTunnels` |

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
