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

**So: do not start your own dev server.** One is already running. Read it with:

```bash
conductor t3 logs -f          # follow, inferred from the working directory
conductor t3 logs -n 1000     # more history
```

The port is discovered by T3's own port scanner (`lsof`), so the preview panel
finds it without configuration. It appears in the generic "Local" list rather
than as the thread's own server, because the listening process is tmux's child.
`previewUrl` in `t3.json` is a static string with no templating and can never
carry a per-worktree port — do not set it.

## Wait for provisioning before you work

`conductor adopt` writes `.conductor-provisioning` into the worktree while it
sets up, and removes it when finished.

This matters because **T3 starts the thread's first turn immediately after
launching the setup script, without waiting for it.** A dev database is a full
clone and takes minutes. An agent that does not check will run migrations
against a database that does not exist yet.

```bash
while [ -f .conductor-provisioning ]; do sleep 5; done
```

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
| `conductor t3 watch` | Run the reconciler |
| `conductor t3 watch once --dry-run` | One pass, reporting only |
| `conductor t3 logs -f` | Follow the shared dev server |
| `conductor t3 status` | Connectivity and hosted worktrees |
| `conductor run` | Start the dev server (the tmux window already does this) |

In the TUI, the worktree list shows a `THREADS` column (`2L·1A` — live and
archived) and a `hibernated` status. `H` hibernates the selected worktree, `W`
wakes it.
