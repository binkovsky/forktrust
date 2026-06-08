# forktrust — AGENTS.md

[forktrust](https://github.com/binkovsky/forktrust) isolates parallel AI coding sessions in their own git worktrees so multiple agents do not step on each other, with a refuse-on-conflict merge gate and a never-lose-WIP guarantee.

This file is the hub for AI agents. The detailed reference lives in `docs/`.

## Read first

1. **[docs/ai-integration.md](./docs/ai-integration.md)** — what an agent must/must-not do, decision template, examples for Claude Code / Cursor / Aider.
2. **[docs/exit-codes.md](./docs/exit-codes.md)** — exit codes 0-14 with cause + remedy + agent action.
3. **[docs/safety-model.md](./docs/safety-model.md)** — pre-flight refusal, dry-run parity, never-lose-WIP, refuse-on-conflict, refuse-on-ignored-loss, `.env.local` ownership, trust gate, path safety.

## Quick start (4 commands)

```bash
forktrust new <slug>                            # create isolated worktree
ft <slug>                                       # cd into it (shell function — see docs/shell-integration.md)
# ... edit, run tests ...
forktrust finish <slug> --dry-run --json        # preview — read would_refuse
forktrust finish <slug>                         # ship (commit + merge + push + cleanup)
```

To abandon (snapshots WIP to `wip/<branch>-YYYYMMDD-HHMMSS-<sha7>` first):

```bash
forktrust rm <slug>
```

To open a GitHub PR instead of merging locally (review workflow):

```bash
forktrust pr <slug>                  # pushes branch + opens PR
forktrust pr-status <slug>           # checks CI / approvals
# (human merges via GitHub UI)
forktrust rm <slug>                  # cleanup after merge
```

## Eight hard guarantees you can rely on

1. **Pre-flight refusal.** `finish`/`rm` make all refusal checks BEFORE any git mutation. Non-zero exit ⇒ no commit, merge, push, or branch delete happened.
2. **Dry-run parity.** `<cmd> --dry-run --json`'s `would_refuse` and exit code exactly match the real command. (Exception: dry-run does NOT execute `[verify]` commands; scope gate IS evaluated in dry-run.)
3. **Never-lose-WIP.** `rm` pushes uncommitted/unpushed work to `wip/<branch>-YYYYMMDD-HHMMSS-<sha7>` on origin before touching local state. Only `--force` skips it.
4. **Refuse-on-conflict.** `finish` aborts the merge on any conflict (exit 2). No `--strategy ours/theirs`, ever.
5. **Refuse-on-ignored-loss.** `rm`/`finish` exit 14 if the worktree has ignored files that `git worktree remove` would silently delete. `--force` skips (rm only).
6. **Verify gate.** If `.forktrustconfig` declares `[verify].commands`, `finish` refuses (exit 15) unless every command exits zero. Skippable via `--no-verify` (with stderr warning) but agents must NOT bypass without user consent.
7. **Scope gate (change contract).** If the worktree has a scope file (`forktrust new --scope "..."` or `forktrust scope --set`), `finish` refuses (exit 16) if the diff touches files outside the declared globs. Skippable via `--no-scope` (with warning); agents must NOT bypass without consent.
8. **Summary gate (commit-message contract).** If `.forktrustconfig` declares `[summary]` rules (Conventional Commits prefix, body length, ticket regex, forbidden patterns), `finish` and `pr` refuse (exit 19) if any commit violates a rule. Skippable via `--no-summary` (with warning); agents must NOT bypass without consent.

Full details: [docs/safety-model.md](./docs/safety-model.md).

## Exit codes (most important)

| Code | Meaning | Agent action |
|---|---|---|
| 0 | success | proceed |
| 2 | merge conflict | STOP — ask user |
| 3 | main is dirty | tell user to stash/commit |
| 6 | no such slug | `forktrust list`; check spelling |
| 7 | ambiguous slug | re-run with `--project` |
| 10 | main on wrong branch | tell user to `git checkout` |
| 12 | no main ref resolved | ask user; never `--force` |
| 14 | ignored files | list them; ask user; never `--force` |
| 15 | verify gate failed | surface `verify_failed_command` + tail of `verify_output`; ask user; never `--no-verify` |
| 16 | scope contract violated | surface `scope_violations` to user; ask user; never `--no-scope` |
| 17 | gh CLI not available | tell user to install gh or run `gh auth login`; never auto-install |
| 18 | `gh pr create` failed | surface stderr; show repro command; don't blind-retry |
| 19 | summary contract violated | surface `summary_violations` to user; ask user; never `--no-summary` |

Full table in [docs/exit-codes.md](./docs/exit-codes.md).

## JSON output

Every state-modifying command supports `--json`:

```bash
forktrust new my-task --json
forktrust status --json
forktrust finish my-task --dry-run --json   # consult would_refuse
forktrust rm my-task --dry-run --json
```

Schemas: [docs/json-schema.md](./docs/json-schema.md). Stable across releases.

## Five rules for agents

1. Every task starts with `forktrust new <slug>`.
2. All edits happen inside `forktrust cd <slug>`'s path (or after `forktrust shell <slug>`).
3. Branch decisions on exit codes, not stderr text.
4. Read `--dry-run --json` before any mutation if you're not certain.
5. `--force`, `forktrust trust`, `forktrust config add`, and `git checkout` on main need explicit user consent.

Anything else is an agent bug, not a forktrust bug.

## Shell integration

```bash
# Add to ~/.zshrc or ~/.bashrc
ft() {
  local p
  p="$(forktrust cd "$1" 2>/dev/null)" || { echo "forktrust: no worktree '$1'" >&2; return 1; }
  cd "$p" || return 1
}
```

Then `ft <slug>` cd's into any worktree. See [docs/shell-integration.md](./docs/shell-integration.md) for tab completion, fzf picker, prompts.

## Reference index

| Topic | Doc |
|---|---|
| Getting started | [docs/getting-started.md](./docs/getting-started.md) |
| Every command + flag | [docs/commands.md](./docs/commands.md) |
| `.forktrustconfig` | [docs/config.md](./docs/config.md) |
| Exit codes | [docs/exit-codes.md](./docs/exit-codes.md) |
| JSON schemas | [docs/json-schema.md](./docs/json-schema.md) |
| Safety model | [docs/safety-model.md](./docs/safety-model.md) |
| Common workflows | [docs/workflows.md](./docs/workflows.md) |
| Troubleshooting | [docs/troubleshooting.md](./docs/troubleshooting.md) |
| Change contracts (`--scope`) | [docs/scope.md](./docs/scope.md) |
| PR mode (`pr`, `pr-status`) | [docs/pr.md](./docs/pr.md) |
| MCP server (`forktrust mcp`) | [docs/mcp.md](./docs/mcp.md) |
| Summary contract (`[summary]`) | [docs/summary.md](./docs/summary.md) |
| Templates (`forktrust init`) | [docs/templates.md](./docs/templates.md) |
| AI integration recipes | [docs/ai-integration.md](./docs/ai-integration.md) |
| Shell integration | [docs/shell-integration.md](./docs/shell-integration.md) |

Install:

```bash
brew install binkovsky/forktrust/forktrust
```

or download from https://github.com/binkovsky/forktrust/releases.
