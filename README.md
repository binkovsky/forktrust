# forktrust

> **Safe-by-default git worktree manager built for AI coding agents.**
> Refuse-on-conflict merges. Never-lose-WIP guarantee. Port allocator per worktree. One command to teach any AI agent how to use it.

`forktrust` lets multiple AI coding agents (Claude Code, Cursor, Aider, Codex, Cline, Continue, OpenCode, Gemini, Auggie) work on the same repo in parallel without stepping on each other. Each task gets its own isolated git worktree, its own ports, and its own `.env.local`. When the task is done, one command merges to main, pushes, and cleans up — and refuses to do anything destructive when the merge isn't safe.

```bash
brew install binkovsky/forktrust/forktrust
forktrust new my-task           # creates isolated worktree
forktrust ai my-task            # launches your configured AI in it
# ... agent edits files in the worktree only ...
forktrust finish my-task        # merge to main + push + cleanup (refuses on conflict)
```

## Documentation

Full reference lives in [**docs/**](./docs/). Key entry points:

- [Getting started](./docs/getting-started.md) — install, register, first worktree (5 min)
- [Commands reference](./docs/commands.md) — every command, every flag
- [`.forktrustconfig`](./docs/config.md) — hooks, ports, trust gate
- [Exit codes](./docs/exit-codes.md) — all 14 with cause / remedy / agent action
- [JSON schemas](./docs/json-schema.md) — stable contract for programmatic use
- [Safety model](./docs/safety-model.md) — every guarantee in depth
- [Change contracts (`--scope`)](./docs/scope.md) — declare which files a task may touch
- [PR mode (`pr`, `pr-status`)](./docs/pr.md) — open a GitHub PR via `gh` and inspect its state
- [Workflows](./docs/workflows.md) — parallel agents, dirty main, abandon-and-restore
- [Troubleshooting](./docs/troubleshooting.md) — every error mapped to a fix
- [AI integration](./docs/ai-integration.md) — Claude Code / Cursor / Aider recipes
- [Shell integration](./docs/shell-integration.md) — `ft` function, completion, fzf

AI agents: read **[AGENTS.md](./AGENTS.md)** first, then `docs/ai-integration.md` + `docs/exit-codes.md` + `docs/safety-model.md`.

## Why

Parallel AI coding sessions break in predictable ways. `forktrust` is opinionated about the parts where being wrong loses work:

| Failure mode | What forktrust does |
|---|---|
| Two agents edit the same checkout, one overwrites the other | Each agent gets its own worktree at `.forktrust/worktrees/<slug>/` |
| Agent auto-resolves a merge conflict and silently picks the wrong side | `finish` REFUSES on any conflict. No `--strategy ours/theirs`. Ever. |
| Session ends with uncommitted work; worktree removed; work gone | `rm` ALWAYS pushes to `wip/<branch>-YYYYMMDD-HHMMSS-<sha7>` on origin first |
| Three `pnpm install` runs collide on `pnpm-lock.yaml` | Symlink hook shares `node_modules` from main; install runs once |
| Three dev servers fight over port 3000 | Per-worktree aligned port block (3000-3009, 3010-3019, ...) auto-written to `.env.local` |
| Agent edits files in the main checkout by accident | `.forktrust/` auto-added to `.git/info/exclude` (never committed) |
| Command fails halfway and leaves a phantom WIP commit | **Pre-flight refusal**: all checks run BEFORE any git mutation. Non-zero exit = no side effects. |
| Dry-run says "OK" but the real command then refuses | **Dry-run matches reality**: `--dry-run --json` predicts the exact exit code the real command would return |
| Worktree has `.env` or `secret.log` and `rm` silently deletes them | `rm`/`finish` REFUSE (exit 14) on ignored files; `--force` skips the guard |
| AI agent ships a broken build / failing tests | `[verify]` gate — `finish` refuses (exit 15) unless your declared `commands` all pass. `--no-verify` for explicit bypass. |
| AI agent edits files outside what was asked ("scope creep") | `--scope` change contract — declare allowed globs at `forktrust new`; `finish` refuses (exit 16) on out-of-scope edits. `--no-scope` for explicit bypass. |
| AI agent wants to open a PR for human review instead of merging directly | `forktrust pr <slug>` — runs the same pre-flight (verify + scope), pushes the branch, opens GitHub PR via `gh`. `pr-status` reports CI/approvals. |

## AI-agent integration in one command

```bash
forktrust agent-docs >> AGENTS.md
# or
forktrust agent-docs >> CLAUDE.md
```

This drops a tested, concise integration snippet into your project's agent-facing doc. Claude Code, Cursor, Codex, Aider read it on session start and use forktrust correctly without further setup. Includes the safety guarantees, stable exit codes, and JSON output schema — everything an agent needs to make the right calls.

## Install

```bash
# Homebrew (macOS + Linux)
brew install binkovsky/forktrust/forktrust

# Go install
go install github.com/binkovsky/forktrust/cmd/forktrust@latest

# Pre-built binaries
# https://github.com/binkovsky/forktrust/releases
```

## Quickstart

```bash
# One-time: register the repo
forktrust config add ~/code/my-project

# Optional: set a default AI tool
forktrust ai --set-default claude

# For each task
forktrust new fix-payment-bug
cd ~/code/my-project/.forktrust/worktrees/fix-payment-bug
# agent does work...
forktrust finish fix-payment-bug
```

## `.forktrustconfig` — declarative per-repo setup

Drop this at the root of your repo (commit it). On `forktrust new <slug>`, the steps run in order.

```toml
# Allocate an aligned port block to each worktree.
# Written to .env.local; auto-released on `finish` / `rm`.
[ports]
range = "3000-3099"
size  = 10
vars  = ["PORT", "NEXT_PUBLIC_PORT", "SERVER_PORT"]

# Copy gitignored files (envs, local configs) into the new worktree.
[[hooks.post_create]]
type = "copy"
from = ".env"
to   = ".env"

# Symlink heavy gitignored dirs from main (skips non-empty tracked dirs).
[[hooks.post_create]]
type = "symlink"
from = "node_modules"
to   = "node_modules"

# Run a shell command. REQUIRES `forktrust trust` to execute.
# Templates: {{.Slug}} {{.Branch}} {{.Path}} {{.MainPath}} {{.Project}}
[[hooks.post_create]]
type = "command"
run  = "pnpm install"
# Optional:
# work_dir = "sub-package"
# env      = { NODE_ENV = "development" }
```

### Trust gate (security)

`copy` and `symlink` hooks run freely (just file ops scoped to the worktree). `command` hooks REFUSE until you explicitly trust the config:

```bash
forktrust trust                  # pin the current SHA-256 of .forktrustconfig
forktrust trust --list           # see all trusted repos
forktrust trust --revoke         # remove trust
```

Any future edit to `.forktrustconfig` auto-revokes trust until you re-run `forktrust trust`. A malicious commit cannot silently inject shell commands.

## Commands

| Command | What it does |
|---|---|
| `forktrust new <slug>` | Create worktree at `.forktrust/worktrees/<slug>` on branch `fork/<slug>` |
| `forktrust list` | All worktrees across all registered repos (use `--json`) |
| `forktrust status` | Per-worktree dirty/ahead/behind/ports (use `--watch`, `--json`) |
| `forktrust cd <slug>` | Print absolute worktree path (for shell `cd` integration) |
| `forktrust shell <slug>` | Open interactive shell in the worktree |
| `forktrust exec <slug> -- <cmd>` | Run any command in the worktree directory |
| `forktrust finish <slug>` | Commit WIP, merge `--no-ff` to main, push, cleanup. Refuses on conflict |
| `forktrust rm <slug>` | Abandon, snapshotting WIP to `wip/<branch>-YYYYMMDD-HHMMSS-<sha7>` first |
| `forktrust ai <slug>` | Launch configured AI tool in the worktree |
| `forktrust scope <slug>` | Show / set / clear / check the change-contract scope for a worktree |
| `forktrust pr <slug>` | Push branch + open GitHub PR (via `gh`) instead of direct merge |
| `forktrust pr-status <slug>` | Show PR status (CI / approvals / mergeable) |
| `forktrust mcp` | Run as a Model Context Protocol stdio server (10 typed tools) |
| `forktrust doctor` | Health check (origin, main ref, hooks, ports, brew version) |
| `forktrust trust [path]` | Approve `.forktrustconfig` command hooks for this repo |
| `forktrust config add <path>` | Register a repo with forktrust |
| `forktrust agent-docs` | Print AGENTS.md snippet to teach an AI agent how to use this |

The mutating + listing commands (`new`, `list`, `status`, `finish`, `rm`, `doctor`) support `--json` for machine-readable output. `finish` and `rm` also accept `--dry-run` to preview without executing. `cd`, `shell`, `config`, `trust`, `exec`, `ai`, `agent-docs` are human-text-only.

### Shell integration (`ft` function)

Add to `~/.zshrc` or `~/.bashrc` so `ft <slug>` cd's into any worktree:

```bash
ft() {
  local p
  p="$(forktrust cd "$1" 2>/dev/null)" || { echo "forktrust: no worktree '$1'" >&2; return 1; }
  cd "$p" || return 1
}
```

Alternatively `forktrust shell <slug>` opens a subshell directly in the worktree (with `FORKTRUST_SLUG` exported so prompts can show which worktree is active).

## Stable exit codes

AI agents and CI scripts can switch on these. They will not change across releases.

| Code | Meaning |
|---|---|
| 0 | success |
| 2 | merge conflict (refuse to auto-resolve) |
| 3 | main worktree is dirty (refuse to overwrite) |
| 4 | push to origin failed |
| 5 | wip/* snapshot push failed (worktree NOT removed) |
| 6 | no worktree matching slug |
| 7 | slug matches multiple projects (use --project) |
| 8 | hook failed (or untrusted command hook) |
| 9 | no origin remote configured |
| 10 | main checkout on the wrong branch (run `git checkout <main>` first) |
| 11 | cwd is in an unregistered git repo (run `forktrust config add .`) |
| 12 | rm/finish could not determine ahead count (push origin/main, or re-run with `--force`) |
| 13 | rm: worktree removed and ports released, but `git branch -D` failed (branch lingers) |
| 14 | rm/finish refused: worktree has ignored files that would be silently lost (move them out or use `--force`) |
| 15 | finish refused: `[verify]` gate failed (command exited non-zero, or `require_clean` and worktree dirty after verify) |
| 16 | finish/scope-check refused: diff touches files outside the declared `--scope` change contract |
| 17 | pr/pr-status: `gh` CLI not available (install gh, or run `gh auth login`) |
| 18 | pr: `gh pr create` returned non-zero (see stderr) |

## How it compares

Verified May 2026 against primary repo sources. Stars are GitHub API counts.

| | forktrust | [gtr](https://github.com/coderabbitai/git-worktree-runner) | [gwq](https://github.com/d-kuro/gwq) | [wtp](https://github.com/satococoa/wtp) | [workz](https://github.com/rohansx/workz) | [claude-squad](https://github.com/smtg-ai/claude-squad) |
|---|---|---|---|---|---|---|
| Language | Go | Bash | Go | Go | Rust | Go |
| Distribution | brew tap | brew tap | brew tap | brew tap | crates.io | brew + install.sh |
| `--ai <tool>` adapter | 9 tools | 9 tools | no | no | no | TUI (5 tools) |
| Declarative copy/symlink/command hooks | yes | yes | yes | yes | auto-detect | no |
| Per-worktree port allocation | yes | no | no | no | yes | no |
| Auto-release ports on finish | **yes** | n/a | n/a | n/a | no | n/a |
| **Refuse-on-conflict merge** | **yes** | no | no | no | no | no |
| **wip/* snapshot on abandon** | **yes** | no | no | no | no | no |
| **`finish` workflow** (commit + merge + push + cleanup) | **yes** | no | no | no | no | no |
| Cross-repo `list` | **yes** | no | no | no | no | no |
| `--json` output | yes | no | yes | no | no | TUI only |
| Trust gate (SHA-pinned config) | yes | yes | no | no | no | no |
| AGENTS.md / CLAUDE.md doc generator | **yes** | no | no | no | no | no |

Hard differentiators (no verified competitor offers these):
- `finish`-style safe merge orchestration with refuse-on-conflict and refuse-on-dirty-main
- `wip/*` snapshot push on abandon (never-lose-WIP guarantee)
- Pre-flight refusal (all checks before any mutation) + dry-run guaranteed to match real behavior
- Ignored-file guard prevents silent `git worktree remove` data loss
- Cross-repo worktree listing
- `agent-docs` command for one-step AI integration
- Auto-release of ports on finish/rm (workz allocates but doesn't release)

## Roadmap

Shipped in v0.5+: `exec`, `status --watch`, cross-worktree edit prediction, AI adapter, `agent-docs`.
Shipped in v0.7.1: `cd`, `shell`, `doctor`, pre-flight refusal model, dry-run parity guarantee.
Shipped in v0.7.2: **`[verify]` gate** — `finish` refuses to merge unless declared `commands` all exit zero; `--no-verify` bypass; exit 15.
Shipped in v0.7.3: **Change contract (`--scope`)** — `forktrust new <slug> --scope "globs"` declares allowed paths; `finish` refuses out-of-scope edits (exit 16). New `forktrust scope <slug>` command (show / set / clear / check). `--no-scope` bypass.
Shipped in v0.7.4: **PR mode** — `forktrust pr <slug>` opens a GitHub PR via `gh` instead of direct merge; `forktrust pr-status <slug>` reports CI / approvals / mergeable. Pre-flight reuse (verify + scope). New exit codes 17 (gh unavailable) + 18 (gh pr create failed).
Shipped in v0.7.5: Adversarial-review hardening — Windows scope fix, verify ring buffer + timeout, autoTitleBody WIP skip, ghPRView state check, JSON envelope contract on all error paths, fetch-failure warning, pr ahead==0 guard, doctor coverage for verify/scope/gh-auth.
Shipped in v0.7.6: **MCP server** — `forktrust mcp` runs as a Model Context Protocol stdio server; 10 typed tools (`forktrust_list`, `forktrust_new`, etc.) for Claude Code / Cursor / any MCP-speaking agent. JSON-RPC 2.0 + MCP 2024-11-05.

Next versions — positioning forktrust as the "merge gate for AI agents":
- **v0.7.6 Summary validation**: agent must produce a summary; cross-checked against diff
- **v0.7.7 Templates + policy packs**: `forktrust new --template nextjs`, `forktrust policy init strict-ai`
- **v0.8.0 Intelligence**: `forktrust plan-merge` (risk scoring), audit ledger, `rollback-info`
- **v0.8.1 Secrets guard**: pre-finish diff scan for keys / tokens / .env
- **v0.9.0 Runtime layer**: `forktrust up/down/logs/web <slug>` (process management)
- **v1.0.0 TUI dashboard**: minimal `forktrust tui` (sessions, diff preview, approve/reject)
- `--no-hooks` ergonomics (per-hook opt-in/out instead of all-or-nothing)
- Windows flock fallback (currently no-op, accepts allocation race)

## Test coverage

57 unit cases (`go test ./... -race`), 7 exhaustive e2e scenarios including 5-process concurrent allocation race (verified flock works), refuse-on-conflict exit codes, SHA-pin auto-revoke, multi-project listing. CI runs on every push.

## License

MIT. See [LICENSE](LICENSE).
