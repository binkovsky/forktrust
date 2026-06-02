# AI integration guide

How AI coding agents should use forktrust. Covers Claude Code, Cursor, Aider, Codex, and any tool that runs shell commands. The future MCP server (v0.7.5) is sketched at the end.

## Core principle

**One task = one slug = one worktree = one branch.** The agent never edits in the main checkout. When the task is done, `finish` ships it; if abandoned, `rm` snapshots it.

## Decision template for agents

Pseudocode an agent should follow:

```
On new task:
  slug = pick_short_kebab_slug(task)
  run: forktrust new <slug> --json
  if exit != 0: surface error, stop
  cwd = json.worktree_path
  do_work(cwd)

On task complete:
  preview = run: forktrust finish <slug> --dry-run --json
  if preview.would_refuse:
    explain to user (use exit code from "(exit N)" suffix)
    stop
  run: forktrust finish <slug> --json
  if exit != 0:
    apply decision flow from exit-codes.md
    stop
  report success with merge SHA

On abandon:
  run: forktrust rm <slug> --json
  if exit != 0:
    apply decision flow from exit-codes.md
    stop
  report: work snapshotted to json.wip_branch
```

## What an agent MUST do

1. **Always run from worktree, not main.** If you're not sure, use `forktrust cd <slug>` to get the path, then `cd` there before any file edit.
2. **Switch on exit codes, not stderr text.** See [exit-codes.md](./exit-codes.md). Stderr text may change between versions; exit codes are stable.
3. **Use `--dry-run --json` for forecasting.** Read `would_refuse` before running `finish`/`rm` for real.
4. **Never use `--force` without explicit user consent.** `--force` is the only way to lose work in forktrust.
5. **Never run `forktrust trust` without user consent.** It grants shell-execution to a tracked file.

## What an agent MUST NOT do

- Auto-resolve a merge conflict (exit 2). Hand to user.
- Auto-`forktrust config add` an unregistered repo (exit 11) without asking.
- Auto-`--force` to silence exit 14 (ignored files) or exit 12 (ahead unknown). That's data loss.
- Edit files in the main checkout. If you find yourself there, stop and use `forktrust new`.

## Claude Code

Add to your project's `CLAUDE.md`:

```bash
forktrust agent-docs >> CLAUDE.md
```

The snippet teaches Claude:
- The pipeline (`new` → work → `finish`)
- All exit codes mapped to actions
- JSON output schemas
- Pre-flight + dry-run parity guarantees
- The `ft` shell function for `cd`

### Recommended `CLAUDE.md` boilerplate

```markdown
## Working on this project

This project uses forktrust for parallel coding sessions. Read
docs/ai-integration.md and docs/exit-codes.md before doing any
multi-step work.

For every non-trivial task:
1. `forktrust new <slug>` — get an isolated worktree
2. `forktrust cd <slug>` to get the path, then `cd` there
3. Do the work
4. `forktrust finish <slug> --dry-run --json` to preview
5. `forktrust finish <slug>` to ship, OR `forktrust rm <slug>` to abandon

NEVER edit files in the main checkout. NEVER use --force without asking.
```

## Cursor

Add the same `forktrust agent-docs` output to `.cursorrules` (or whatever your project uses).

Cursor's `/multitask` will work alongside forktrust: each subagent should be told to use its own slug.

## Aider

In your `.aider.conf.yml`:

```yaml
read:
  - AGENTS.md
  - docs/ai-integration.md
```

Aider will read these on session start.

For a session bound to one worktree:

```bash
forktrust new my-task
cd $(forktrust cd my-task)
aider
```

## Codex / generic shell-based agents

Any agent that can run shell commands works. The pattern:

```bash
slug="task-$(date +%s)"
forktrust new "$slug" --json > /tmp/new.json
cd $(jq -r .worktree_path /tmp/new.json)

# ... agent does work ...

# Preview
forktrust finish "$slug" --dry-run --json > /tmp/preview.json
reason=$(jq -r .would_refuse /tmp/preview.json)
if [ -n "$reason" ]; then
    echo "would refuse: $reason" >&2
    exit 1
fi

# Ship
forktrust finish "$slug" --json
```

## Parallel agents (the hard case)

When you orchestrate multiple agents (e.g. one runs frontend, one runs backend), give each a separate slug:

```bash
forktrust new frontend-task &
forktrust new backend-task &
wait

# Agent 1 in worktree 1
( cd $(forktrust cd frontend-task) && agent_runs_here ) &

# Agent 2 in worktree 2
( cd $(forktrust cd backend-task) && agent_runs_here ) &

wait

# Finish in any order; failures are independent
forktrust finish frontend-task || handle_failure
forktrust finish backend-task  || handle_failure
```

Each worktree has its own port block (if `[ports]` is configured) so dev servers don't collide.

### Conflict prediction

For experimental cross-worktree edit prediction, `forktrust new` runs a per-slug analysis and emits `predicted_overlaps`:

```bash
forktrust new fix-payment --json | jq .predicted_overlaps
# ["src/payment/checkout.ts"]
```

If two live worktrees both touch the same file, expect a merge conflict at `finish`. Future v0.8.0 will expose this as a separate `forktrust plan-merge` command.

## Future: MCP server (v0.7.5)

Coming: `forktrust mcp` will run an MCP-protocol stdio server, exposing forktrust operations as typed tools so MCP-compatible agents (Claude Code, Cursor) call them directly without shelling out.

Planned tool surface:

| Tool | Inputs | Returns |
|---|---|---|
| `new_worktree` | `slug`, `project?`, `from?`, `install?`, `no_hooks?` | new JSON result |
| `list_worktrees` | (none) | list JSON result |
| `status` | `project?` | status JSON |
| `finish_worktree` | `slug`, `project?`, `message?`, `dry_run?` | finish JSON |
| `rm_worktree` | `slug`, `project?`, `force?`, `dry_run?` | rm JSON |
| `get_ports` | `slug` | `{start, end}` |
| `doctor` | `project?` | doctor JSON |

Configure in Claude Code's `mcpServers`:

```json
{
  "mcpServers": {
    "forktrust": {
      "command": "forktrust",
      "args": ["mcp"]
    }
  }
}
```

Until MCP ships, the shell-command pattern above is the official integration path.

## Summary: the contract

If an AI agent does these five things, it cannot lose work or corrupt main:

1. Every task starts with `forktrust new <slug>`.
2. All edits happen inside `forktrust cd <slug>`'s path.
3. Decisions branch on exit codes, not stderr text.
4. `--dry-run --json` is consulted before any mutation if the agent isn't certain.
5. `--force`, `forktrust trust`, `forktrust config add`, and `git checkout` on main are gated by explicit user consent.

Anything else is an agent bug, not a forktrust bug.
