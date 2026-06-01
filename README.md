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
| `forktrust finish <slug>` | Commit WIP, merge `--no-ff` to main, push, cleanup. Refuses on conflict |
| `forktrust rm <slug>` | Abandon, snapshotting WIP to `wip/<branch>-YYYYMMDD-HHMMSS-<sha7>` first |
| `forktrust ai <slug>` | Launch configured AI tool in the worktree |
| `forktrust trust [path]` | Approve `.forktrustconfig` command hooks for this repo |
| `forktrust config add <path>` | Register a repo with forktrust |
| `forktrust agent-docs` | Print AGENTS.md snippet to teach an AI agent how to use this |

The mutating + listing commands (`new`, `list`, `status`, `finish`, `rm`) support `--json` for machine-readable output. `finish` and `rm` also accept `--dry-run` to preview without executing. `config`, `trust`, `exec`, `ai`, `agent-docs` are human-text-only.

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
- Cross-repo worktree listing
- `agent-docs` command for one-step AI integration
- Auto-release of ports on finish/rm (workz allocates but doesn't release)

## Roadmap

Shipped in v0.5+: `exec`, `status --watch`, cross-worktree edit prediction, AI adapter, `agent-docs`.

Open:

- MCP server (`forktrust mcp`) so AI agents can call worktree ops as native tools
- Claude Code plugin (slash commands + skill + hook) in a sister repo
- `--no-hooks` ergonomics (per-hook opt-in/out instead of all-or-nothing)
- Windows flock fallback (currently no-op, accepts allocation race)

## Test coverage

57 unit cases (`go test ./... -race`), 7 exhaustive e2e scenarios including 5-process concurrent allocation race (verified flock works), refuse-on-conflict exit codes, SHA-pin auto-revoke, multi-project listing. CI runs on every push.

## License

MIT. See [LICENSE](LICENSE).
