# Workflows

Canonical patterns. Each workflow is a copy-paste runbook.

## Single task, single agent

The bread-and-butter flow.

```bash
forktrust new fix-payment
ft fix-payment                      # cd in via the shell function
# ... edit, run tests ...
forktrust finish fix-payment
```

If verify-before-ship matters to you (it should), preview first:

```bash
forktrust finish fix-payment --dry-run --json | jq .would_refuse
```

## Parallel tasks (5 agents, 5 worktrees)

Launch multiple agents that won't step on each other.

Terminal 1:
```bash
forktrust new task-a
forktrust ai task-a                 # launches your default AI in the worktree
```

Terminal 2:
```bash
forktrust new task-b
forktrust ai task-b
```

…and so on. Each gets its own:
- worktree directory (`.forktrust/worktrees/task-X/`)
- branch (`fork/task-X`)
- port block (if `[ports]` configured) — written to its own `.env.local`
- copies of `.env` / symlinks of `node_modules` (per your `[[hooks.post_create]]`)

Live monitor across all:

```bash
forktrust status --watch --interval 2s
```

When tasks finish (in any order):

```bash
forktrust finish task-a
forktrust finish task-b
```

Each `finish` is independent. If one has a conflict (exit 2), it doesn't affect others.

## Abandoning work (never lose it)

You decided this task is a dead end. Don't just delete the worktree — let forktrust snapshot it:

```bash
forktrust rm fix-payment
```

Output:
```
==> pushing snapshot as wip/fix-payment-20260601-153045-a1b2c3d
==> deleted local branch fork/fix-payment
==> done
```

Now `wip/fix-payment-20260601-153045-a1b2c3d` is on origin. To restore:

```bash
git fetch origin
git checkout wip/fix-payment-20260601-153045-a1b2c3d
# or: cherry-pick specific commits, or re-create a worktree from this ref
forktrust new fix-payment-take-2 --from origin/wip/fix-payment-20260601-153045-a1b2c3d
```

## Dirty main checkout

You started editing in main by accident. Now `finish` for some other slug refuses with exit 3.

```bash
forktrust finish task-a
# REFUSE: main working tree in /path/to/myapp has 3 uncommitted change(s).
# error: main worktree is dirty (3 files)
```

Fix options:
1. **Commit the main changes** (usually wrong — they're WIP, you don't want them on main yet).
2. **Stash:**
   ```bash
   cd <main-checkout>
   git stash push -u -m "work-in-main"
   cd -
   forktrust finish task-a
   git stash pop                    # back where you were
   ```
3. **Move the work into a worktree:**
   ```bash
   cd <main-checkout>
   git stash push -u
   forktrust new recovery-work
   ft recovery-work
   git stash pop
   ```

## "I edited in main but I should have used a worktree"

Recovery procedure (this happens, even to careful people).

```bash
cd <main-checkout>
# Make sure work is staged or stashed
git status --short

# Create a recovery worktree
forktrust new recovery-<context>

# Get the work over
git stash push -u                           # in main
ft recovery-<context>                       # cd into worktree
git stash pop                               # in worktree

# Continue normally from the worktree
forktrust finish recovery-<context>
```

## CI: gate a PR on dry-run

Use `--dry-run --json` to validate from CI without touching git state.

```bash
forktrust finish $SLUG --dry-run --json > /tmp/finish.json
WOULD_REFUSE=$(jq -r .would_refuse < /tmp/finish.json)
if [ -n "$WOULD_REFUSE" ]; then
    echo "::error::forktrust finish would refuse: $WOULD_REFUSE"
    exit 1
fi
```

## Multi-repo session

You have several registered repos. forktrust handles them all from one binary.

List everything:
```bash
forktrust list
```

Per-repo status:
```bash
forktrust status -p myapp
forktrust status -p otherapp
```

If the same slug exists in multiple repos, finish/rm need disambiguation:
```bash
forktrust finish fix-bug -p myapp
```

(Otherwise exit 7.)

## Health check before a deploy

Run `forktrust doctor` to catch problems before they bite you mid-flow:

```bash
forktrust doctor --json | jq '.checks[] | select(.status != "ok")'
```

Typical findings:
- `main-ref: fail` — main branch doesn't resolve. Push origin/main, or create local main.
- `trust-gate: warn` — command hooks present but config edited. Re-run `forktrust trust`.
- `brew-version: warn` — newer version available. Run `brew upgrade binkovsky/forktrust/forktrust`.

## Per-branch port allocation

You want each agent to have its own dev server port without thinking about it.

`.forktrustconfig` in your repo:
```toml
[ports]
range = "3000-3099"
size  = 10
vars  = ["PORT", "NEXT_PUBLIC_PORT"]
```

Each `forktrust new <slug>` allocates the next aligned block. Block 1 → 3000-3009; block 2 → 3010-3019; etc. Released automatically on `finish`/`rm`.

Inside the worktree, `.env.local` has:
```
# Managed by forktrust. Do not edit; values are overwritten on each `forktrust new`.
PORT=3010
NEXT_PUBLIC_PORT=3010
PORT_END=3019
FORKTRUST_PORT_START=3010
FORKTRUST_PORT_END=3019
FORKTRUST_PORT_SIZE=10
```

Your dev framework (Next.js, etc.) reads `PORT` automatically.

## Reusing node_modules across worktrees

`pnpm install` is expensive. Symlink it from main:

```toml
[[hooks.post_create]]
type = "symlink"
from = "node_modules"
to   = "node_modules"
```

Caveats:
- All worktrees share the same dependency versions. If a worktree needs a different version, it must shadow it manually (e.g. `rm node_modules; pnpm install`).
- If `node_modules` is tracked (not in `.gitignore`), the hook skips it for safety.

## Switching AI tools per project

User config (`~/.config/forktrust/config.toml`):
```toml
[ai]
default = "claude"
```

Override per-invocation:
```bash
forktrust ai my-task --tool cursor
```

Or list available adapters:
```bash
forktrust ai --list
```

## Hooks that depend on the slug

Use template variables:

```toml
[[hooks.post_create]]
type = "command"
run  = "createdb myapp_{{.Slug}} && psql myapp_{{.Slug}} -f db/schema.sql"
```

For each `forktrust new fix-payment`, creates `myapp_fix-payment` database.

(Requires `forktrust trust` because of `command` hooks.)

## Detached HEAD recovery

forktrust doesn't create detached-HEAD worktrees, but git lets users do it manually. If a worktree under `.forktrust/worktrees/<slug>/` ends up detached:

`forktrust rm` exits 1 (refuses — would build a malformed `wip/-<stamp>` ref).

Fix manually:
```bash
cd <main-repo>
git worktree remove .forktrust/worktrees/<slug>
```

Then the next `forktrust new` cleanly re-allocates the slot. Port block is auto-released on the next `Allocate` call's orphan-prune sweep.
