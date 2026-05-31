# forktrust

> Safe-by-default git worktree manager for parallel AI coding sessions.
> Refuse-on-conflict merges. Never-lose-WIP guarantee. Works with Claude Code, Cursor, Aider, Cline, Codex — any agent that edits files.

`forktrust` isolates each AI chat in its own git worktree so parallel sessions never step on each other. When a chat is done, one command commits, merges to main, pushes, and cleans up — and refuses to do anything destructive when the merge isn't safe.

```
$ forktrust new fix-search
==> creating worktree .forktrust/worktrees/fix-search on new branch fork/fix-search

  cd .forktrust/worktrees/fix-search

# ... agent edits files in that path only ...

$ forktrust finish fix-search
==> 3 uncommitted change(s) — committing to fork/fix-search ("WIP: fix-search")
==> branch is 4 commit(s) ahead of origin/main — merging
==> merging fork/fix-search into main
==> pushing main to origin
==> deleted local branch fork/fix-search
==> finish done
```

## Why

Parallel AI coding sessions break in predictable ways:

- Two chats edit the same file in the same checkout, one overwrites the other.
- An agent auto-resolves a merge conflict and silently picks the wrong side.
- A session ends with uncommitted work, the worktree is removed, the work is gone.
- Three `npm install` runs collide on `package-lock.json`.
- Three dev servers fight over port 3000.

Existing worktree wrappers solve some of this. `forktrust` is opinionated about the parts where being wrong loses work:

1. **Refuse on merge conflict.** `finish` aborts the merge and asks. No `--strategy ours/theirs`. Ever.
2. **Refuse on dirty main.** If the main checkout has uncommitted changes, `finish` won't risk overwriting them.
3. **Never-lose-WIP.** `rm` (abandon) always pushes the current state to `wip/<branch>-YYYYMMDD` on origin before removing anything.
4. **Local-only exclude.** Auto-adds `.forktrust/` to `.git/info/exclude` so worktree dirs don't pollute the main checkout's `git status`. Nothing committed to the project.

## Install

### Homebrew (planned)

```bash
brew install binkovsky/forktrust/forktrust
```

### Go install

```bash
go install github.com/binkovsky/forktrust/cmd/forktrust@latest
```

### Pre-built binaries

[Releases page](https://github.com/binkovsky/forktrust/releases) (Linux + macOS, amd64 + arm64).

## Quickstart

```bash
# One-time: register a repo
forktrust config add ~/code/my-project

# Start a new task in an isolated worktree
forktrust new fix-payment-bug

# Worktree is at: ~/code/my-project/.forktrust/worktrees/fix-payment-bug
# On branch:     fork/fix-payment-bug
cd ~/code/my-project/.forktrust/worktrees/fix-payment-bug

# ... let your agent edit / build / test ...

# When done: commit + merge + push + cleanup
forktrust finish fix-payment-bug

# Or abandon (saves WIP as wip/fix-payment-bug-YYYYMMDD on origin first):
forktrust rm fix-payment-bug
```

## Commands

| Command | Effect |
|---|---|
| `forktrust new <slug> [--install] [-p <project>]` | Create worktree at `.forktrust/worktrees/<slug>` on branch `fork/<slug>`. Copies `.env*` files. |
| `forktrust list` | Show all worktrees across registered projects, dirty status. |
| `forktrust finish <slug> [-m "<msg>"] [-p <project>]` | Commit WIP, fast-forward main, merge `--no-ff`, push, remove. Refuses on conflict or dirty main. |
| `forktrust rm <slug> [--force] [-p <project>]` | Snapshot WIP as `wip/*` on origin, then remove. With `--force`: drop WIP, no push. |
| `forktrust config add <path> [name]` | Register a git repo. |
| `forktrust config list` | Show registered repos. |
| `forktrust config remove <name>` | Drop a repo from the registry. |
| `forktrust config path` | Print the config file path. |

If only one repo is registered (or you run inside a git repo with none registered), `-p` is optional.

## Configuration

`forktrust config add` writes to `~/.config/forktrust/config.toml` (or `$XDG_CONFIG_HOME/forktrust/`):

```toml
[[project]]
name = "my-app"
path = "/Users/me/code/my-app"
main_branch = "main"            # optional, defaults to "main"
install_cmd = "pnpm install"    # optional, used when `new --install`
```

Run `forktrust` with zero config inside any git repo — it auto-detects the repo as the target.

## How it compares

| | forktrust | [claude-squad](https://github.com/smtg-ai/claude-squad) | [gtr](https://github.com/coderabbitai/git-worktree-runner) | [uzi](https://github.com/devflowinc/uzi) |
|---|---|---|---|---|
| Distribution | binary (Go) | binary (Go) | bash + brew | `go install` only |
| TUI | no | yes (tmux) | no | yes (tmux) |
| Refuse-on-conflict merge | yes (hard) | no (manual) | no | no |
| WIP-snapshot on abandon | yes | no | no | no |
| Auto-add to .git/info/exclude | yes | no | no | no |
| Cross-repo `list` | yes | no | no | no |
| AI tool integration | Claude Code plugin (planned) | Claude/Codex/Gemini/Aider/OpenCode | many | Claude Code |
| Port allocation per worktree | planned (Phase 2) | no | no | yes |
| Lockfile lock | planned (Phase 2) | no | no | no |

If you want a heavyweight TUI that runs many agents in tmux side-by-side, use `claude-squad`. If you want a tool that gets out of your way and refuses to do anything that risks losing work, use `forktrust`.

## Roadmap

**Phase 2:**

- Port allocator: per-worktree `.env` overrides so 3 dev servers don't fight over :3000.
- Lockfile lock: `flock` wrapper that serializes `npm install` / `pnpm install` / `cargo build` across parallel worktrees.
- Cross-worktree edit prediction: at `new` time, warn if the slug overlaps with files actively edited in another worktree.
- Claude Code plugin: `/wt-new`, `/wt-finish`, `/wt-list` slash commands; PreToolUse hook that warns when an Edit is going into the main checkout instead of a worktree.

**Phase 3:**

- Cursor / Aider / Cline integrations via their respective hook surfaces.
- `forktrust doctor` — diagnose user setup.
- Optional bubbletea TUI dashboard for those who want it.

## License

MIT — see [LICENSE](LICENSE).
