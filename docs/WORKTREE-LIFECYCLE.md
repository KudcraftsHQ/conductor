# Worktree lifecycle

Conductor keeps **one inventory of every git worktree on the machine** and cleans
them autonomously but safely: anything it frees can come back by itself.

Spec approved 2026-11-24. Phase 1 (this document's "Built" parts) is the
foundation: discovery, facts, the pure classifier, config, records, and the
read-only CLI. Phase 2 fills in the executors; see [For implementers](#for-implementers).

## Why

- Disk on the dev machine hit 82%. ~400 worktrees existed; most were never known to conductor.
- Three things create worktrees:
  1. conductor: `~/.conductor/<project>/<city>` (registered in `~/.conductor/conductor.json`)
  2. T3 Code: `~/.t3/worktrees/<repo>/t3-…`. T3 runs `conductor adopt` on create
     (`runOnWorktreeCreate`), but only a fraction ever got registered. The
     inventory does not depend on it.
  3. Claude Code subagents (`isolation: "worktree"`): `<repo>/.claude/worktrees/<name>`.
     Plus strays (`~/Projects/.worktrees/*`, sibling `*-worktrees/` dirs).
- Per worktree the real (unshared) cost is ~0.5–1 GB: `node_modules` is mostly
  hardlinks into bun's cache (`~/.bun/install/cache`), plus build caches and local
  dev data.
- The interim script `~/.local/bin/t3-stale-worktrees` (and its systemd units
  `t3-stale-worktrees-report.{service,timer}`, `t3-stale-worktrees-listen.service`)
  is what conductor replaces. Its proven logic is ported here.

## Ports and dev DB are optional

The inventory is **not** conductor.json. It lives in `~/.conductor/lifecycle.json`
and allocates nothing: no ports, no database, no dev server. Those stay
on-demand through the existing adopt/provision path. The T3 watcher never reads
the inventory, so a worktree with no `T3Threads` stays invisible to it, as the
repo's CLAUDE.md requires.

## States

```
 active ──settled ≥ slimAfter──▶ slim ──PR merged/closed + settled ≥ retireAfter──▶ retired
   ▲                              │                                                   │
   └──────thread wakes ───────────┴──────────────── thread wakes ─────────────────────┘
              (reinstall deps)                         (revive whole worktree, same path)
 needs-you: settled ≥ needsYouAfter with unpushed commits or uncommitted changes
```

| State (`wt ls`) | Condition | On disk | Action |
|---|---|---|---|
| `active` | a T3 thread on it is live and unsettled or pinned, OR a process has its cwd inside, OR last activity < slimAfter | everything | none |
| `slim` | idle ≥ slimAfter, nothing running inside | work only | delete regenerable dirs |
| `retired-candidate` | clean, nothing unpushed, no OPEN PR, idle ≥ retireAfter, and the branch's PR is MERGED/CLOSED (or see *Retire without a PR*); or a needs-you worktree past its retire date | nothing | write record + graveyard ref, `git worktree remove` (no --force) |
| `needs-you` | idle ≥ needsYouAfter with uncommitted, untracked or unpushed work | work only (slim) | snapshot to a local graveyard ref, ntfy once with the retire date; retire at the retire date |
| `untouchable` | repo roots (not even listed), conductor root worktrees, `keep` matches, missing dirs, anything whose git state, T3 threads or process table cannot be read | — | never |
| `retired` | only a record remains (`wt ls --all`) | nothing | revive on wake |

**Last activity** is the newest of: any thread's updatedAt / lastVisitedAt /
settledAt / unsettledAt / archivedAt, the last commit, and the newest mtime two
levels deep (skipping `node_modules` and `.git`). A dev server still writing
into the tree therefore keeps it active.

**Retire date for needs-you** is the later of: last activity + abandonAfter,
and the moment Hammas was told + (abandonAfter − needsYouAfter, at least a day),
and any date already announced. A worktree idle for months still gets its full
warning, and an announced date never moves earlier.

**Retire without a PR** (an addition to the spec): a clean worktree on a named
branch with nothing unpushed and no PR, whose HEAD is already on
`origin/<default branch>`, is retired like a merged one. Git loses nothing
either way, and these are mostly Claude Code subagent worktrees that never get
a PR. Detached HEADs (usually tool-owned) are never retired this way.

## Settings

`defaults.lifecycle` in conductor.json, with a per-project override at
`projects.<name>.lifecycle`. Every key is optional and inherits.

| Key | Default | |
|---|---|---|
| `enabled` | `true` | automatic actions on/off |
| `slimAfter` | `24h` | |
| `retireAfter` | `48h` | |
| `needsYouAfter` | `14d` | |
| `abandonAfter` | `30d` | |
| `sweepInterval` | `1h` | |
| `slimAllowlist` | `node_modules, .vite, .turbo, .next, .svelte-kit, .parcel-cache, .remotion` | basenames at any depth (≤4); replaces the default when set |
| `ignoredArchiveCapMB` | `50` | cap for ignored files archived on retire |
| `keep` | none | path globs never touched (`~/` ok, trailing `/**` = below) — e.g. e2e runners' worktrees |

```bash
conductor wt config                                   # effective settings
conductor wt config set retireAfter 72h
conductor wt config set enabled false --project cutroom
conductor wt config set keep '~/Projects/.worktrees/**'
conductor wt config unset retireAfter
```

Durations accept `h`/`m` and a day suffix (`14d`, `1d12h`). Note: a conductor
binary older than this feature drops the `lifecycle` keys when it rewrites
conductor.json, so set them once every conductor on the machine is updated.

**Pause:** `conductor wt pause [--reason …]` writes `~/.conductor/lifecycle.paused`;
`conductor wt resume` removes it. While it exists nothing acts; `ls` and
`gc --dry-run` still report (and say so). Any pause file counts, even an unreadable one.

`--force` on manual commands overrides the timing thresholds, never the safety rules.

## Slim

Delete ONLY gitignored paths from the allowlist. `node_modules` additionally
needs a lockfile (`bun.lock`/`bun.lockb`, package-lock, yarn, pnpm) **tracked by
git**, so the reinstall is exact; build caches (`.vite`, `.next`, …) are
regenerated by the build and need no lockfile. Confirm each path is ignored with
`git check-ignore`. Mark the inventory entry slim with what was removed. Never
delete `.env*`, dev data dirs (e.g. cutroom's `.cutroom-dev`), `.git`, or
anything tracked (`NeverSlim` in settings.go).

## Retire

1. Create `refs/graveyard/<id>` in the main repo pointing at HEAD (keeps commits
   alive even if the branch is deleted locally or on GitHub). With unsaved work:
   commit the full dirty state (tracked changes + untracked non-ignored files)
   into a commit object WITHOUT touching the worktree's branch or index (temp
   index + `git commit-tree`), parent = HEAD, and point the ref at it. Nothing
   is pushed anywhere, ever.
2. Archive gitignored files that are NOT regenerable (not on the slim allowlist:
   `.env*`, local config) when small (cap `ignoredArchiveCapMB`; skip big dev-data
   dirs and record that they were skipped) into
   `~/.conductor/graveyard/<id>/ignored.tar.gz`.
3. Write `~/.conductor/graveyard/<id>/record.json` (`lifecycle.Record`), fsynced.
4. If conductor-registered: release ports/DB/tunnel via the existing
   hibernate / `ReleaseResources` path and keep the entry. Close tmux windows
   inside; re-check no process inside (skip if busy). Then `git worktree remove
   <path>` WITHOUT --force; if git removed the `.git` link but left files written
   during removal, clear the remainder (the interim script's `remove_worktree`).
   `git worktree prune`. Never delete the branch.

As built: a worktree that still has uncommitted or untracked changes at its
retire date is skipped, not removed, because `git worktree remove` refuses it
without `--force`. Its needs-you snapshot keeps the work and it stays slim.
Unpushed commits alone do not block: the graveyard ref at HEAD keeps them.

## Safety rules (never overridden, not even by --force)

1. Nothing that git or the record cannot bring back is ever deleted.
2. Never act while a process has its cwd inside, a T3 thread on it is unsettled
   or pinned, or (for retire) any PR on the branch is OPEN. Unknown counts as
   unsafe: unreadable threads, processes, git state or PR state block the action.
3. `git worktree remove` without --force; the record + graveyard ref are written
   and fsynced BEFORE removal.
4. Repo roots and conductor project-root worktrees are never touched. The pause file stops everything.
5. Every action is logged (JSONL, `~/.local/state/conductor/lifecycle.log`).

## Revive / rehydrate

- **Slim → active:** before `conductor t3 watch` starts a dev server for a
  worktree whose thread unsettled, if the lockfile is present and `node_modules`
  missing, run the project's install (`bun install` for bun). Also
  `conductor wt hydrate [path]` (idempotent, fast no-op if deps exist), wired as
  a Claude Code SessionStart hook (`conductor wt hydrate --hook`): silent and
  fast when there is nothing to do, never fails the session, acts only inside a
  worktree the lifecycle knows, never in a repo root without node_modules unless
  it is a known slimmed worktree.
- **Retired → active:** when a T3 thread whose worktreePath matches a retired
  record unsettles, the watcher revives it: `git worktree add <same path>
  <branch>` (or the graveyard SHA if the branch is gone; for a snapshot, check out
  the branch at HEAD then restore the snapshot's changes as uncommitted changes),
  restore archived ignored files, install deps, re-provision ports/DB only if the
  project wants them (existing adopt path), mark active. Same path → the T3
  thread continues unchanged.
- **Manual:** `conductor wt revive <thread-id|branch|path|record-id> [--no-install]`.

## CLI

| Command | Phase |
|---|---|
| `conductor wt ls [--all] [--state X] [--json] [--size] [--fresh]` | 1 |
| `conductor wt gc --dry-run [--force] [--only slim\|retire] [--json] [path…]` | 1 (plan only) |
| `conductor wt gc …` without `--dry-run` | 2 |
| `conductor wt revive …`, `conductor wt hydrate …` | 2 (registered, return "not implemented yet") |
| `conductor wt pause [--reason]`, `conductor wt resume` | 1 |
| `conductor wt config [set k v \| unset k] [--project p]` | 1 |

`wt ls` columns: STATE, REPO, BRANCH, THREAD (working/settled/archived/none),
PR (open/merged/closed/none, `?` when gh failed), GIT (clean / N dirty / N
untracked / N unpushed), OWN SIZE (`--size` only), IDLE, PATH. OWN SIZE counts
only files with a single link — what removing the worktree would actually free;
bun-cache hardlinks are excluded. A full pass over ~220 worktrees takes ~3 s with
a warm PR cache (gh lists are cached 15 min under `~/.local/state/conductor/cache`).

## Notifications (ntfy)

Read `NTFY_URL`/`NTFY_TOKEN` from `~/.config/ntfy-hammas-dev/env` at runtime
(never commit, never print). One summary per sweep, only when something
happened or needs Hammas: priority low for "slimmed N / retired N, freed X GB",
default for needs-you items (each reported once, with its retire date), high for
failures. No message on a quiet pass. No hostnames, IPs or tokens in the repo;
the URL comes from that env file.

## Scheduling

Hourly sweep (`sweepInterval`) from a systemd user timer: `conductor wt schedule
install` writes `~/.config/systemd/user/conductor-lifecycle.{service,timer}`
(not enabled; `systemctl --user enable --now conductor-lifecycle.timer`). Not a
hook in `conductor t3 watch`: a sweep runs for minutes and would stall the
watcher's 5 s loop. React to watcher
settle/unsettle events for rehydrate/revive. At cutover disable
`t3-stale-worktrees-report.timer` and `t3-stale-worktrees-listen.service` (keep
the script file).

## For implementers

House rules: no unit tests (Hammas's standing rule; overrides the repo
CLAUDE.md). Verify with `go build ./...`, `go vet ./...` and dry runs against
the real machine. Read the repo CLAUDE.md and `docs/T3-WORKTREES.md` first.
Internal tool: open the PR, `gh pr checks`, merge when green (or right away if
there are no checks). In a worktree, `go build` may need `-buildvcs=false`.

### Package layout — `internal/lifecycle`

| File | What | Owner |
|---|---|---|
| `types.go` | `State`, `Action`, `Plan`, `Candidate`, `Facts` (+ `ThreadFact`, `PR`, `Proc`, `TmuxWindow`, `ConductorRef`) and their helpers (`HasUnsavedWork`, `ThreadStatus`, `PRStatus`, `LastActivity`) | phase 1 |
| `env.go` | `Env` — every path (`ConductorDir`, `T3Root`, `T3StateDB`, `StateDir`, `GraveyardDir`, `InventoryPath`, `PauseFile`), `NewEnv`, `ProjectFor` | phase 1 |
| `settings.go` | `Settings`, `ResolveSettings`, `Env.SettingsFor`, `SetSetting`, `NeverSlim`, `Settings.Kept`; pause: `Env.Paused/Pause/Resume` | phase 1 |
| `discover.go` | `Discover(ctx, env) []Candidate` | phase 1 |
| `facts*.go`, `size.go` | `Gather(ctx, env, cands, inv, GatherOptions) []*Facts`; `OwnBytes`, `SharedAwareBytes`; unexported helpers `fxGit` (30 s timeout, `GIT_OPTIONAL_LOCKS=0`), `fxRun`, `inside`, `parallel`, `readProcTable` | phase 1 |
| `classify.go` | **pure** `Classify(f, settings, now, ClassifyOptions{Paused, Force}) Plan`; `GitSummary`, `Human` | phase 1 |
| `pass.go` | `PlanPass(ctx, env, PassOptions) *Pass` — discover + gather + classify, writes nothing | phase 1 |
| `inventory.go` | `Inventory`/`Entry`, `Env.LoadInventory/SaveInventory/UpdateInventory` (locked, atomic), `Inventory.Observe`, `writeFileSync` (fsync + rename) | phase 1 |
| `record.go` | `Record`, `NewRecordID`, `Env.WriteRecord/ReadRecord/ListRecords/RecordsForPath`, `RecordDir`, `IgnoredArchiveName` | phase 1 |
| `graveyard.go` | `GraveyardRef`, `CreateGraveyardRef` (refuses to move an existing ref), `ReadGraveyardRef`, `ListGraveyardRefs` | phase 1 |
| `log.go` | `Event`, `Env.LogEvent` (JSONL, fsync) | phase 1 |
| `exec.go` | `ExecOptions`, `ErrNotImplemented`, `ErrPaused`, the executor contract | phase 1 |
| `slim.go` | `Slim(ctx, env, f, plan, s, ExecOptions) (*SlimResult, error)` | **A** |
| `snapshot.go` | `Snapshot(ctx, env, f, id, ExecOptions) (*SnapshotResult, error)` — needs-you snapshot | **A** |
| `retire.go` | `Retire(ctx, env, f, plan, s, ExecOptions) (*Record, error)` | **A** |
| `notify.go` | `SweepSummary`, `NeedsYou`, `Notify(ctx, env, *SweepSummary) error` | **A** |
| `sweep.go` | `SweepOptions`, `Sweep(ctx, env, SweepOptions) (*SweepSummary, error)` — gc executor + scheduling | **A** |
| `revive.go` | `ReviveOptions`, `ReviveResult`, `FindRecord`, `Revive(ctx, env, target, ReviveOptions)` | **B** |
| `hydrate.go` | `HydrateOptions`, `HydrateResult`, `Hydrate(ctx, env, path, HydrateOptions)` | **B** |
| `hooks.go` | `OnThreadUnsettled(ctx, env, worktreePath)` (watcher hook), `SessionStartHook(ctx, env, cwd)` | **B** |

CLI: `cmd/conductor/lifecycle.go` registers **every** lifecycle subcommand (ls,
gc, revive, hydrate, pause, resume, config) and holds ls/pause/resume/config.
`cmd/conductor/lifecycle_gc.go` (**A**) has `runWtGC`: fill in the
non-`--dry-run` branch with `lifecycle.Sweep`. `cmd/conductor/lifecycle_revive.go`
(**B**) has `runWtRevive` / `runWtHydrate`. Add flags inside the
`registerLifecycleGC` / `registerLifecycleRevive` functions in your own file;
nobody needs to edit `lifecycle.go`.

### Agent A — slim, retire, needs-you, notify, sweep, scheduling

- Every executor follows the contract in `exec.go`: re-check `env.Paused()` at
  act time; re-read what may have changed since the plan (close `f.DevWindows`,
  then re-check processes; HEAD; dirty/untracked/unpushed; thread working) and
  skip on change; `env.LogEvent` for ok, skipped and failed; record the outcome
  on the inventory entry via `env.UpdateInventory`.
- Needs-you: `Snapshot` then set `Entry.SnapshotSHA/SnapshotRef/SnapshotHead/SnapshotAt`,
  `NeedsYouNotifiedAt` and `RetireAt` (take `plan.RetireAt`; classify keeps it
  from moving earlier). Notify once per worktree.
- Retire on a worktree with unsaved work always takes a **fresh** snapshot first
  (the needs-you one may be stale). Write the record before removing. For a
  conductor-registered worktree use the existing `workspace` release path
  (`ReleaseResources`) and keep the entry; never `RemoveTree(..., true)`.
- `Sweep`: `PlanPass` → execute per `plan.Action` (respect `SweepOptions.Only`)
  → `inv.Observe(pass.Facts, pass.Plans, now)` + per-entry fields → `SaveInventory`
  → `Notify` when `o.Notify`. Then scheduling (watch daemon or systemd timer)
  and the interim-script cutover.

### Agent B — revive, hydrate, watcher hooks, SessionStart hook

- `FindRecord`: thread id (record `T3Threads`), branch, path or record id; newest
  record with `RevivedAt` zero.
- `Revive` at the record's exact `Path`; branch if it exists, else the graveyard
  SHA; restore a snapshot as uncommitted changes; extract `IgnoredArchiveName`;
  `Hydrate` unless `--no-install`; re-provision only through the adopt path;
  set `Record.RevivedAt` (rewrite with `WriteRecord`) and `Entry.RevivedAt`.
- `Hydrate`: no-op when `node_modules` exists; install per lockfile (bun first);
  set `Entry.HydratedAt` (this clears `Entry.Slimmed()`).
- Wire `OnThreadUnsettled` into `internal/t3watch` / the watcher **before** it
  starts a dev server, without changing its behaviour for worktrees the
  lifecycle does not know. Keep `internal/t3watch`'s decisions pure.
- Wire `conductor wt hydrate --hook` into `~/.claude/settings.json` SessionStart.

### Where state lives

| What | Where |
|---|---|
| Settings | `~/.conductor/conductor.json` → `defaults.lifecycle`, `projects.<p>.lifecycle` |
| Inventory | `~/.conductor/lifecycle.json` (lock `~/.conductor/lifecycle-inventory.lock`) |
| Pause | `~/.conductor/lifecycle.paused` |
| Records | `~/.conductor/graveyard/<id>/record.json` + `ignored.tar.gz` |
| Graveyard refs | `refs/graveyard/<id>` in the repo root (local only) |
| Action log | `~/.local/state/conductor/lifecycle.log` (JSONL) |
| gh PR cache | `~/.local/state/conductor/cache/prs-<owner>-<name>.json` (15 min) |
| T3 threads (read-only) | `~/.t3/userdata/statev2.sqlite`, `orchestration_v2_projection_threads.payload_json` |

All of `~/.conductor` honours `CONDUCTOR_CONFIG_DIR`, which makes a throwaway
copy (`cp ~/.conductor/conductor.json $TMPDIR/x/`) a safe place to try writes.
