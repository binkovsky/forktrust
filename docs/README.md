# forktrust documentation

Complete reference for [forktrust](https://github.com/binkovsky/forktrust) — the safe-by-default git worktree manager for parallel AI coding sessions.

This documentation tree is the single source of truth. AGENTS.md, llms.txt, and the project README point here for full details.

## For users

Start here if you are a human using forktrust day-to-day:

- **[Getting started](./getting-started.md)** — install, register your first repo, create + finish your first worktree (5-minute walkthrough).
- **[Workflows](./workflows.md)** — canonical patterns: parallel tasks, dirty main, abandoning work, multi-repo sessions, CI scripting.
- **[Commands reference](./commands.md)** — every command, every flag, every behavior, with examples.
- **[.forktrustconfig reference](./config.md)** — per-repo TOML config: hooks, ports, verify gate.
- **[Change contracts (`--scope`)](./scope.md)** — declare which files a task is allowed to touch; `finish` refuses out-of-scope edits.
- **[PR mode (`forktrust pr`)](./pr.md)** — open a GitHub PR instead of merging locally; `pr-status` reports CI / approvals / mergeable.
- **[MCP server (`forktrust mcp`)](./mcp.md)** — run as a Model Context Protocol stdio server so AI agents call forktrust as typed native tools.
- **[Shell integration](./shell-integration.md)** — `ft` function, `forktrust shell`, prompt integration, autocomplete.
- **[Troubleshooting](./troubleshooting.md)** — every error message and exit code mapped to a fix.

## For AI agents

If you are an AI coding agent (Claude Code, Cursor, Aider, Codex, etc.), read these in order:

1. **[AI integration guide](./ai-integration.md)** — exactly how an agent should call forktrust, what to do on each exit code, when NOT to use it. Includes Claude Code, Cursor, and Aider integration recipes.
2. **[Exit codes catalog](./exit-codes.md)** — every exit code (0-14) with cause, remedy, and the agent action it implies.
3. **[JSON output schemas](./json-schema.md)** — exact fields emitted by every `--json` command; field stability guarantees.
4. **[Safety model](./safety-model.md)** — the pre-flight refusal guarantee, never-lose-WIP, refuse-on-conflict, ignored-file guard, `.env.local` ownership rules. Required reading to reason about forktrust correctly.

## Reference

- **[Glossary](#glossary)** — terms used across docs.
- **[Versioning policy](#versioning-policy)** — what is stable, what is not.
- **[Roadmap](../README.md#roadmap)** — see project README.

---

## Glossary

| Term | Meaning |
|---|---|
| **slug** | Short identifier for a task. Becomes the worktree directory name and part of the branch name (`fork/<slug>`). |
| **worktree** | A separate working directory tied to the same `.git` directory as the main checkout, on its own branch. forktrust puts each one at `.forktrust/worktrees/<slug>`. |
| **main checkout** | The original clone of the repo. forktrust never edits files here directly — only on `finish` when merging the worktree branch into `main`. |
| **mainBranch** | The integration branch (default `main`, configurable per-project). |
| **aheadRef** | The reference forktrust counts commits ahead of. Cascade: `origin/<mainBranch>` if it exists, else local `<mainBranch>`. If neither resolves: exit 12. |
| **wip/* branch** | `wip/<branch-without-fork-prefix>-YYYYMMDD-HHMMSS-<sha7>`. Always pushed by `rm` before removing the worktree, so committed/uncommitted work survives. |
| **pre-flight** | The phase of `finish`/`rm` that runs all refusal checks BEFORE any git mutation. Non-zero exit ⇒ no side effects. |
| **dry-run parity** | Guarantee that `<cmd> --dry-run --json`'s `would_refuse` field and exit code exactly match what the real command would do. |
| **hook** | An entry in `[[hooks.post_create]]` that fires after `forktrust new`. Three types: `copy`, `symlink`, `command`. |
| **trust gate** | Approval mechanism for `command` hooks: SHA-pinned `.forktrustconfig`. Any edit auto-revokes trust until `forktrust trust` is re-run. |
| **verify gate** | `[verify]` section in `.forktrustconfig` declaring commands that MUST exit zero before `finish` may merge. Refusal = exit 15 in the finish pre-flight. Skipped via `--no-verify` (with warning). |
| **scope gate** | Per-worktree change contract at `<repo>/.forktrust/scopes/<slug>.toml` declaring which glob patterns the task may modify. Refusal = exit 16 in the finish pre-flight. Skipped via `--no-scope` (with warning). |
| **port block** | Aligned range (e.g. `3000-3009`, `3010-3019`) reserved per-slug and written into `.env.local`. Auto-released on `finish` / `rm`. |
| **ManagedHeader** | Exact first line forktrust writes into every `.env.local`: `# Managed by forktrust. Do not edit; values are overwritten on each `forktrust new`.\n`. Used as ownership proof. |

## Versioning policy

These are **stable** across releases (won't break without a major version bump):

- **Exit codes** (0-14). See [exit-codes.md](./exit-codes.md). New codes may be added at the high end; existing codes never change meaning.
- **JSON schemas** for `--json` output. New fields may be added; existing fields keep their names and types. See [json-schema.md](./json-schema.md).
- **Command names and core flags** (`--json`, `--dry-run`, `--project`, `--force`, `--no-hooks`). `forktrust new my-task` will always do the same thing.
- **wip/* name format**: `wip/<branch>-YYYYMMDD-HHMMSS-<sha7>`.

These are **not stable** (may change without notice):

- Human-readable stderr text (use exit codes + JSON for programmatic parsing).
- Internal Go package APIs (`internal/...` — these are not a public library).
- Default values that don't affect correctness (e.g. `--interval` default for `status --watch`).
