# AGENTS.md - forktrust

This file teaches AI coding agents (Claude Code, Cursor, Aider, Codex, Cline, Continue, OpenCode, Gemini, Auggie) how to use `forktrust` correctly in this repository. It is intentionally short and stable across releases.

> **Note for tool maintainers:** to add this kind of doc to YOUR project, run `forktrust agent-docs >> AGENTS.md`. The output is tested and battle-ready for any project that uses forktrust.

## What forktrust is

A git-worktree manager that isolates parallel AI coding sessions. Each task gets its own copy of the codebase at `.forktrust/worktrees/<slug>/` on branch `fork/<slug>`. Concurrent agents do not step on each other.

## Workflow you MUST follow

1. **Create an isolated worktree for the task:**
   ```bash
   forktrust new <task-slug>
   ```
   Output prints the worktree path. cd into it. Make ALL your edits there.

2. **Do NOT edit files in the main checkout.** The main repo root is for the user's local state and deploy staging. Every edit outside `.forktrust/worktrees/<slug>/` is a bug.

3. **Ship the change:**
   ```bash
   forktrust finish <task-slug>
   ```
   This commits WIP, merges `--no-ff` to main, pushes, and removes the worktree.

4. **Abandon a task without merging:**
   ```bash
   forktrust rm <task-slug>
   ```
   This pushes uncommitted WIP to `wip/<branch>-YYYYMMDD-HHMMSS` on origin before removing, so work survives.

## Hard safety guarantees you can rely on

- `finish` **REFUSES on merge conflict** (exit 2). If you see exit 2, show the user and ask before resolving anything manually. Never override with `--strategy ours/theirs`.
- `finish` **REFUSES if the main checkout has uncommitted changes** (exit 3). Tell the user; do not bypass.
- `rm` **ALWAYS pushes WIP to `wip/*`** on origin before removing (without `--force`). Work is never lost.
- `.forktrust/` is auto-added to `.git/info/exclude` (local-only), so worktree dirs never appear in `git status` for the main checkout.

## Machine-readable output

The state-modifying commands have `--json` for parseable output (`new`, `list`, `status`, `finish`, `rm`). `finish` and `rm` also accept `--dry-run`.

```bash
forktrust list --json
forktrust status --json
forktrust new my-task --json
forktrust finish my-task --json
forktrust finish my-task --dry-run    # preview without executing
```

## Stable exit codes (will not change)

| Code | Meaning | What you should do |
|---|---|---|
| 0 | success | continue |
| 2 | merge conflict | tell user, do not auto-resolve |
| 3 | main worktree is dirty | tell user to commit/stash main's WIP |
| 4 | push to origin failed | check auth / network, retry |
| 5 | wip/* push failed, worktree NOT removed | the work is still local; surface the diagnostic |
| 6 | no worktree matching slug | check slug spelling / `forktrust list` |
| 7 | slug matches multiple projects | re-run with `--project <name>` |
| 8 | hook failed OR untrusted command hook | for untrusted: `forktrust trust` after the user inspects |
| 9 | no origin remote configured | tell user to add origin or use `--force` knowing the risk |
| 10 | main checkout on the wrong branch | run `git -C <repo> checkout <mainBranch>` first |
| 11 | cwd is in an unregistered git repo | run `forktrust config add .` or use `--project` |
| 12 | rm/finish could not determine ahead count | push origin/main, or re-run with `--force` (keeps local branch) |
| 13 | rm: worktree removed, port released, branch -D failed | investigate with `git -C <repo> branch | grep fork/<slug>` |

## When NOT to use forktrust

- Pure read-only investigation (grep, file reads). No worktree needed.
- User explicitly says `edit in main, no worktree` for one task. Override has to be written.

## Quick reference

| Command | Effect |
|---|---|
| `forktrust new <slug>` | Create isolated worktree on `fork/<slug>` |
| `forktrust list [--json]` | All worktrees across registered repos |
| `forktrust finish <slug>` | Merge to main + push + cleanup (refuses on conflict) |
| `forktrust rm <slug>` | Abandon, pushing WIP to `wip/*` first |
| `forktrust ai <slug>` | Launch configured AI tool in the worktree |
| `forktrust trust` | Approve `.forktrustconfig` command hooks for this repo |
| `forktrust agent-docs` | Print this kind of snippet for any other project |

Install: `brew install binkovsky/forktrust/forktrust` or download from <https://github.com/binkovsky/forktrust/releases>.

Source + issues: <https://github.com/binkovsky/forktrust>.
