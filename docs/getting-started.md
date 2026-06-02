# Getting started

This guide walks you from zero to a finished worktree in under 5 minutes.

## 1. Install

### Homebrew (recommended, macOS + Linux)

```bash
brew install binkovsky/forktrust/forktrust
forktrust --version   # should print 0.7.1 or later
```

### Go install (works anywhere with Go 1.22+)

```bash
go install github.com/binkovsky/forktrust/cmd/forktrust@latest
```

### Pre-built binary

Download from [github.com/binkovsky/forktrust/releases](https://github.com/binkovsky/forktrust/releases), extract, put on `PATH`.

### Verify install with `doctor`

```bash
forktrust doctor
```

You should see `[ ok ]` lines for `git`, `gh` (optional), and `brew-version`. If anything is fail/warn, the hint tells you what to fix.

## 2. Register a repo

forktrust manages worktrees across multiple repos. Each repo must be registered once:

```bash
cd ~/code/my-project
forktrust config add .
```

Optional name (defaults to directory basename):

```bash
forktrust config add ~/code/my-project myapp
```

List registered repos:

```bash
forktrust config list
```

## 3. Add the shell function

Drop this into `~/.zshrc` or `~/.bashrc`:

```bash
ft() {
  local p
  p="$(forktrust cd "$1" 2>/dev/null)" || { echo "forktrust: no worktree '$1'" >&2; return 1; }
  cd "$p" || return 1
}
```

After `source ~/.zshrc`, you can `ft my-task` to cd into any worktree.

## 4. Create your first worktree

```bash
forktrust new fix-login-bug
```

What this does:
1. Creates `.forktrust/worktrees/fix-login-bug/` inside the repo.
2. Creates branch `fork/fix-login-bug` from `origin/main` (or `main` if no origin).
3. Runs any `[[hooks.post_create]]` declared in `.forktrustconfig`.
4. Allocates a port block if `[ports]` is configured; writes `.env.local`.
5. Adds `.forktrust/` to `.git/info/exclude` so the directory never appears in `git status` of the main checkout.

Now `cd` in:

```bash
ft fix-login-bug
# or
forktrust shell fix-login-bug   # opens a subshell directly in the worktree
```

You are now in a clean, isolated checkout. Edit files normally — they don't touch the main checkout.

## 5. Do the work

Make whatever changes you need. Run tests, dev servers, etc. The worktree has its own port block (if configured) so you can run `npm run dev` here while another worktree runs the same on different ports.

You don't need to commit — `finish` and `rm` both handle uncommitted work.

## 6. Ship it: `finish`

When the work is good:

```bash
forktrust finish fix-login-bug
```

This will, in order:
1. **Pre-flight refusal checks** (no side effects yet):
   - Resolve `aheadRef` — if no main reference is reachable, exit 12.
   - Check for ignored files in the worktree — exit 14 if any.
   - Check main checkout is on `mainBranch` — exit 10 otherwise.
   - Check main checkout is clean — exit 3 otherwise.
2. Commit any uncommitted work on the worktree branch (message: `WIP: <slug>` or your `--message`).
3. Pull origin/main (if origin is configured).
4. Merge `--no-ff` into main. **Refuses on conflict — exit 2.**
5. Push main to origin.
6. Remove the worktree.
7. Release the port block.
8. Delete the local branch.

Always preview first if you're unsure:

```bash
forktrust finish fix-login-bug --dry-run --json
```

The `would_refuse` field tells you exactly what (if anything) would block the real command. See [safety-model.md](./safety-model.md) for the dry-run parity guarantee.

## 7. Or throw it away: `rm`

If the work shouldn't ship but you don't want to lose it either:

```bash
forktrust rm fix-login-bug
```

This snapshots any uncommitted/unpushed code to `wip/fix-login-bug-YYYYMMDD-HHMMSS-<sha7>` on origin, removes the worktree, releases ports, deletes the local branch.

Work is **never lost** unless you pass `--force` (which skips the wip/* push). See [safety-model.md](./safety-model.md) for the full guarantee.

## Next steps

- **[Workflows](./workflows.md)** — running 5 agents in parallel, monitoring with `status --watch`, AI integration patterns.
- **[Configuration](./config.md)** — set up `.forktrustconfig` for hooks, ports, and (future) verify/scope contracts.
- **[Commands reference](./commands.md)** — every command in detail.
- **[AI integration](./ai-integration.md)** — Claude Code, Cursor, Aider recipes.
