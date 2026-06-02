# `.forktrustconfig` reference

Per-repo TOML config. Drop at the **root of your repo** (committed to git). forktrust reads it on every `forktrust new` and validates the syntax before doing anything.

If the file is absent, forktrust runs in **zero-config mode**: no hooks, no port allocation, default branch naming.

## Schema overview

```toml
# Allocate an aligned port block per worktree.
# Written to .env.local; auto-released on `finish` / `rm`.
[ports]
range = "3000-3099"
size  = 10
vars  = ["PORT", "NEXT_PUBLIC_PORT", "SERVER_PORT"]

# Copy gitignored files into the new worktree.
[[hooks.post_create]]
type = "copy"
from = ".env"
to   = ".env"

# Symlink heavy gitignored dirs (skips non-empty tracked dirs for safety).
[[hooks.post_create]]
type = "symlink"
from = "node_modules"
to   = "node_modules"

# Run a shell command. REQUIRES `forktrust trust` to execute.
[[hooks.post_create]]
type     = "command"
run      = "pnpm install"
work_dir = "sub-package"   # optional, relative to the new worktree
env      = { NODE_ENV = "development" }   # optional
```

## `[ports]`

| Field | Type | Default | Meaning |
|---|---|---|---|
| `range` | string | `"3000-3999"` | Inclusive port range to allocate from, format `"MIN-MAX"`. |
| `size` | int | `10` | Number of ports per worktree block. Blocks are aligned (3000-3009, 3010-3019, ...). |
| `vars` | string[] | `["PORT"]` | Env var names to populate with the start port. See [Vars semantics](#vars-semantics) below. |

forktrust always writes these into `.env.local` regardless of `vars`:

- `PORT_END` — last port in the block (start + size - 1)
- `FORKTRUST_PORT_START`
- `FORKTRUST_PORT_END`
- `FORKTRUST_PORT_SIZE`

### Vars semantics

The `vars` field uses three distinct states with three distinct behaviors:

| TOML | Behavior |
|---|---|
| omit `vars` entirely | Default: emit `PORT=<start>` |
| `vars = ["X", "Y"]` | Emit `X=<start>` and `Y=<start>` (in addition to the always-emitted `PORT_END` etc.) |
| `vars = []` | Opt-out: do NOT emit `PORT`. Only the always-emitted `FORKTRUST_*` lines appear. |

Each name must match `^[A-Za-z_][A-Za-z0-9_]*$` (POSIX env var name); otherwise validation fails at parse time to prevent newline/`=`/space injection into `.env.local`.

Names that duplicate forktrust-managed names (`PORT_END`, `FORKTRUST_*`) are silently dropped with a warning.

### Generated `.env.local` example

For `range = "3000-3099"`, `size = 10`, `vars = ["PORT", "NEXT_PUBLIC_PORT"]`, block 3010-3019:

```
# Managed by forktrust. Do not edit; values are overwritten on each `forktrust new`.
# Block released automatically on `forktrust finish` / `forktrust rm`.
PORT=3010
NEXT_PUBLIC_PORT=3010
PORT_END=3019
FORKTRUST_PORT_START=3010
FORKTRUST_PORT_END=3019
FORKTRUST_PORT_SIZE=10
```

The first line is the exact `ManagedHeader` — used as ownership proof so `rm`/`finish` know this file is safe to delete on cleanup. See [safety-model.md](./safety-model.md#env-local-ownership).

### Port store

Allocations persist to `~/Library/Application Support/forktrust/ports.json` (macOS) or `~/.config/forktrust/ports.json` (Linux). The store is `flock`-protected so concurrent `forktrust new` runs never collide.

Orphan allocations (where the worktree directory was deleted manually) are pruned automatically on the next `Allocate` call.

## `[[hooks.post_create]]`

A TOML array of tables. Hooks run in declared order. If one fails, subsequent hooks are skipped and the worktree is left in place for inspection.

Three types: `copy`, `symlink`, `command`.

### `type = "copy"`

```toml
[[hooks.post_create]]
type = "copy"
from = ".env"          # relative to MAIN checkout
to   = ".env"          # relative to NEW worktree
```

Copies a file or directory from the main checkout into the new worktree.

Path safety:
- Both `from` and `to` are resolved against their roots with full symlink-escape protection (`pathsafe.SafeJoin`).
- The leaf write uses `O_NOFOLLOW`, so a symlink at the destination is refused atomically.
- `refuseAncestorSymlinks` walks every ancestor of `to` and refuses if any is a symlink (closes the `node_modules/.bin -> /etc/` style attack from a prior symlink hook).
- `to = ".git"` and anything under `.git/` is refused (protected destination).
- For dirs: tracked symlinks inside the source that point outside the source root are silently skipped.

Missing source: hook is reported as skipped, not failed.

### `type = "symlink"`

```toml
[[hooks.post_create]]
type = "symlink"
from = "node_modules"   # relative to MAIN checkout
to   = "node_modules"   # relative to NEW worktree
```

Creates a symlink from `to` (in the new worktree) → `from` (resolved in the main checkout).

Refusals:
- `to` is an existing **regular file** — refused. (Otherwise `from = "README.md" to = "README.md"` would silently turn every write in the worktree into a write to the main checkout.)
- `to` is a non-empty directory (tracked) — refused with a skip note.
- `to` is `.git` or under `.git/`, or `.forktrust` / under `.forktrust/` — refused.
- Source missing — reported as skipped.

Existing symlinks and empty dirs at `to` are replaced (idempotent re-runs).

### `type = "command"`

```toml
[[hooks.post_create]]
type     = "command"
run      = "pnpm install"
work_dir = "sub-package"
env      = { NODE_ENV = "development" }
```

Runs `sh -c "<run>"` with:
- `cwd` set to `<worktree>/<work_dir>` (or worktree root if `work_dir` is empty)
- environment = current env + `env` table + parsed `.env.local` (KEY=VALUE only, NO shell eval — closes the `.env.local` injection vector)
- stdout/stderr inherited (or piped to stderr in `--json` mode)

Template variables expanded in `run` and `env` values:

| Variable | Value |
|---|---|
| `{{.Branch}}` | `fork/<slug>` |
| `{{.Slug}}` | `<slug>` |
| `{{.Path}}` | absolute worktree path |
| `{{.MainPath}}` | absolute main checkout path |
| `{{.Project}}` | registered project name |

Templates use `text/template` with `missingkey=error`, so a typo (e.g. `{{.Patch}}`) errors out rather than silently inserting `<no value>`.

#### Trust gate

**Command hooks REFUSE to run until you trust the config.** This is the only thing in the entire pipeline that can execute arbitrary code from a tracked file, so it's gated explicitly:

```bash
forktrust trust              # pin the current SHA-256 of .forktrustconfig
forktrust trust --list       # see all trusted repos
forktrust trust --revoke     # remove trust
```

Any edit to `.forktrustconfig` changes its SHA-256, which auto-revokes trust. A malicious commit cannot silently inject shell commands — it would re-trigger the trust prompt.

`copy` and `symlink` hooks don't require trust: they are scoped to the worktree, refused by `pathsafe` if they try to escape, and refused if they target protected paths.

`--no-hooks` on `forktrust new` skips command hooks entirely (copy/symlink still run). Useful in automated environments where you want zero shell execution.

## Validation rules

forktrust runs the full validation on every `forktrust new`. If `.forktrustconfig` is invalid, the worktree is NOT created.

- Unknown top-level keys are tolerated (TOML default), but unknown hook fields error.
- `type` in each hook must be `"copy" | "symlink" | "command"`.
- `from`/`to` required for `copy`/`symlink`; forbidden for `command`.
- `run` required for `command`; forbidden for `copy`/`symlink`.
- `[ports].vars[i]` must be a POSIX env var name (regex: `^[A-Za-z_][A-Za-z0-9_]*$`). Otherwise: hard fail.

## Common recipes

### Next.js with hot port allocation

```toml
[ports]
range = "3000-3999"
size  = 10
vars  = ["PORT", "NEXT_PUBLIC_PORT"]

[[hooks.post_create]]
type = "copy"
from = ".env.local.template"
to   = ".env.local.app"      # forktrust writes its own .env.local; user template lives next to it

[[hooks.post_create]]
type = "symlink"
from = "node_modules"
to   = "node_modules"

[[hooks.post_create]]
type = "command"
run  = "cp .env.local.app .env.local.user"
```

### Go monorepo with verify-on-finish (PLANNED for v0.7.2)

```toml
# Not yet implemented — coming in v0.7.2
[verify]
commands     = ["go build ./...", "go test ./..."]
require_clean = true
```

### Backend with database setup

```toml
[ports]
range = "4000-4099"
size  = 10
vars  = ["PORT", "API_PORT"]

[[hooks.post_create]]
type = "command"
run  = "createdb myapp_{{.Slug}}"

[[hooks.post_create]]
type = "command"
run  = "psql -d myapp_{{.Slug}} -f db/schema.sql"
```

(Requires `forktrust trust` because of `command` hooks.)

## Future sections (planned)

These are documented in the [roadmap](../README.md#roadmap) but not implemented yet:

- `[verify]` (v0.7.2) — commands that must pass before `finish` will merge.
- `[scope]` / `--scope` flag (v0.7.3) — restrict which paths the worktree may modify.
- `[summary]` (v0.7.6) — schema for the post-task summary requirement.
- `[[process]]` (v0.9.0) — declare a dev server for `forktrust up/down/logs/web`.

Schema for these will be additive — existing configs keep working.
