# T3 Code worktrees

T3 Code owns the worktree lifecycle. Conductor owns everything around it.

You make a worktree by starting a thread on a new branch in T3. You dispose of
one by deleting the last thread bound to it. Conductor follows along: allocating
ports, cloning the dev database, running setup, starting the dev server, and
releasing all of that again when the threads go.

This document is the contract. **Agents working in a conductor worktree should
read it before starting a dev server, reaching for a port, or touching a
database.**

## The three states

| T3 | Conductor | On disk |
|---|---|---|
| at least one live thread | **active** — ports, database, tunnel, dev server | worktree + branch |
| every thread archived | **hibernated** — resources released | worktree + branch, untouched |
| every thread deleted | **gone** — entry dropped | worktree removed, **branch kept** |

Archiving is reversible and deleting is not, which matches what T3 itself does:
its server stops the session and closes terminals on archive, and touches no
files.

Two consequences worth knowing:

- **Hibernation never touches your working tree.** Uncommitted work survives it.
  What you lose is the environment: new ports and a fresh dev database on wake.
- **Teardown never deletes the branch.** T3's own worktree removal leaves it
  alone, and a thread deleted by mistake must not be able to destroy commits.
  `conductor worktree archive` — the manual, conductor-initiated path — still
  deletes the branch, because that one is a deliberate act.

## Setting up a project

Each repository carries a `t3.json` at its root:

```json
{
  "$schema": "https://t3.codes/schema/t3.json",
  "scripts": [
    {
      "name": "conductor provision",
      "command": "conductor adopt --path \"$T3CODE_WORKTREE_PATH\" --repo \"$T3CODE_PROJECT_ROOT\"",
      "runOnWorktreeCreate": true
    },
    { "name": "dev logs", "command": "conductor t3 logs -f" }
  ]
}
```

T3 runs the first script in the new worktree the moment it creates one, with
`T3CODE_WORKTREE_PATH` and `T3CODE_PROJECT_ROOT` in the environment. That is the
only creation hook T3 has — **there is no teardown counterpart**, which is why
the watcher exists.

The repository root must be registered as a T3 project (`t3 project add <path>`).
Do not register a *worktree* as a project: a thread in a project rooted at the
worktree is a plain local checkout to T3, its `worktreePath` is null, and
conductor cannot see it at all.

## Running it

```bash
conductor t3 watch                 # the reconciler; run it once per machine
conductor t3 watch once --dry-run  # what would change, changing nothing
conductor adopt --bind-only        # bind an already-provisioned worktree
```

The watcher polls T3's snapshot every few seconds. It is the only cleanup path
there is, so if it is not running, hibernation and teardown do not happen.

### Guards

These exist because the failure modes are expensive:

- **Batch deletions are refused.** Deleting a T3 *project* expands into a
  `thread.delete` for every thread in it, which with one project per repository
  is every worktree at once. More than `--max-teardowns` (default 3) in a single
  pass and the watcher does nothing and says so.
- **Hibernation is debounced** by 10 minutes, so archiving a thread by mistake
  costs a click to undo rather than a database reclone.
- **Worktrees with no bound threads are invisible.** Anything created under tmux
  or herdr predates this and is never touched.
- **Cold start reconciles from scratch.** Nothing replays what happened while the
  watcher was down.

## The dev server, and why it is in tmux

One dev server per worktree, in a tmux window named `project/branch`, started by
`conductor run`. Every live thread on that worktree shares it.

It is not in a T3 terminal, and that is deliberate: **a T3 terminal belongs to
one thread.** Sessions are keyed `(threadId, terminalId)` and the UI only renders
the active thread's, so a second thread running the dev script would collide on
the port rather than share the server. A tmux window is shared by construction,
and survives T3 restarts and updates besides.

What each thread gets instead is a read-only view: conductor opens a terminal
running `conductor t3 logs -f` on every thread bound to the worktree.

**So: do not start your own dev server.** One is already running. Read it, and
control it, with:

```bash
conductor t3 logs -f          # follow, inferred from the working directory
conductor t3 logs -n 1000     # more history

conductor t3 dev status       # address, window, whether the port is listening
conductor t3 dev restart      # interrupt and bring it back — recreates the
                              # window if it is gone
conductor t3 dev stop         # leave it at the restart prompt
```

`restart` is the answer to a wedged server, a stale build or a missing window.
It knows the three cases apart: no window is recreated, a window already sitting
at the restart prompt only needs Enter, and a running server is interrupted
first and then answered once the loop has actually reached the prompt — sending
Enter early feeds the keystroke to the dev server instead.

The port is discovered by T3's own port scanner (`lsof`), so the preview panel
finds it without configuration. It appears in the generic "Local" list rather
than as the thread's own server, because the listening process is tmux's child.
`previewUrl` in `t3.json` is a static string with no templating and can never
carry a per-worktree port — do not set it.

## Wait for provisioning before you work

**T3 starts the thread's first turn immediately after launching the setup
script, without waiting for it.** A dev database is a full clone and takes
minutes. Anything that queries, migrates, seeds or tests has to wait first, or
it works against a database that does not exist yet.

```bash
conductor wait                # setup finished and the database answers
conductor wait --for all      # and the dev server is listening
conductor wait --json --timeout 5m
```

| Exit | Meaning |
|---|---|
| 0 | ready |
| 1 | setup failed, or was abandoned by a process that died — prints the tail of the setup log |
| 2 | timed out |
| 3 | not a conductor worktree |

The dev server is deliberately **not** part of the default: a task whose job is
to fix a server that will not boot must not deadlock waiting for it to boot.

### How readiness is decided

Not by a marker file. `.conductor-provisioning` used to serve this purpose and
was removed, because absence meant two different things — "ready" and "nobody
ever set this up" were the same state, so the check failed open on every path
that did not happen to write it.

Readiness is derived from the worktree's `setupStatus` in `conductor.json`,
which every provisioning path writes, together with the pid that owns it
(`internal/ready`):

| State | Means |
|---|---|
| `imminent` | a git worktree of a registered project with no conductor entry yet — T3 has just created it and the hook has not registered it. **Not ready**; this is the window the old file check got wrong |
| `provisioning` | setup running, with a live process behind it |
| `stalled` | setup claims to be running but its process is gone, or no process was ever recorded. Terminal — re-run `conductor adopt` |
| `pending` | setup done, but the database is not answering or the port is not listening |
| `ready` / `unmanaged` | safe to work |

Because the state is in `conductor.json` rather than in the worktree, it
survives the provisioner being killed, and a crashed setup is reported as
stalled instead of hanging forever.

### Conductor holds the first turn

On every path conductor controls — the TUI, `conductor build`, the ClickUp
dispatcher, `conductor t3 create --prompt` — the thread's first turn is not
submitted until the worktree is ready. An agent cannot get ahead of work that
was never dispatched.

The exception is a thread started in **T3's own composer**: T3 launches the hook
and opens the first turn itself, and conductor never sees it. That path still
depends on the agent reading its context file and running `conductor wait`.

Ports and database URLs reach your process through the environment
(`CONDUCTOR_PORT`, `CONDUCTOR_PORTS`, `CONDUCTOR_PORT_<LABEL>`, the project's own
`.env` written by setup). Read them from there rather than hardcoding: **a woken
worktree has different ports than it had before.** Local database names are
derived from the first port and change with it; remote `dev_<city>` names are
stable across hibernation.

## Where worktrees live

Wherever T3 put them — by default `~/.t3/worktrees/<repo>/<branch>`. The
composer's creation flow has no path parameter, so this is not conductor's
choice to make. Conductor records the real path in `conductor.json` and resolves
everything from there; nothing derives a path from the project and worktree
names any more.

The worktree *entry* is still named after a city, even when the directory is
named after a branch. That is not cosmetic: remote dev databases are
`dev_<city>` on a shared server and must be unique across every project, or two
repositories with a `main` branch collide on one database.

## What conductor cannot do

- **It cannot write into a thread.** `thread.activity.append` is internal to T3;
  both dispatch endpoints reject it from an outside client. Setup output is
  visible because it runs in a real terminal, not because conductor reported it.
- **It cannot override T3's delete flow.** If you confirm "Delete the worktree
  too?", T3 removes the directory itself. The watcher reconciles afterwards.
- **The `t3` CLI cannot drive any of this.** Its subcommands are `start`, `serve`,
  `pair`, `auth`, `project`, `service`, `connect` — no thread, no worktree, no
  turn. Everything goes through the HTTP and WebSocket API.

## Reference

| Command | What it does |
|---|---|
| `conductor adopt` | Register and provision an existing worktree directory |
| `conductor adopt --bind-only` | Record the thread binding without re-provisioning |
| `conductor wait` | Block until setup has finished and the database answers |
| `conductor wait --for all` | …and the dev server is listening |
| `conductor t3 dev status` | The dev server's address, window and port state |
| `conductor t3 dev restart` | Restart it, recreating its window if needed |
| `conductor t3 watch` | Run the reconciler |
| `conductor t3 watch once --dry-run` | One pass, reporting only |
| `conductor t3 logs -f` | Follow the shared dev server |
| `conductor t3 status` | Connectivity and hosted worktrees |
| `conductor run` | Start the dev server (the tmux window already does this) |

In the TUI, the worktree list shows a `THREADS` column (`2L·1A` — live and
archived) and a `hibernated` status. `H` hibernates the selected worktree, `W`
wakes it.
