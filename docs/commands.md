# Commands reference

Every forktrust command, every flag, every behavior. Examples for both human and JSON output.

Common flags (where supported):
- `--project <name>` / `-p <name>` — pick a registered project explicitly. Required when a slug exists in more than one repo (otherwise exit 7).
- `--json` — emit a single JSON object on stdout. Stable schema; see [json-schema.md](./json-schema.md).
- `--dry-run` — for `finish` and `rm`: print/emit the plan + would-refuse reason without executing.

All commands return one of the documented [exit codes](./exit-codes.md).

---

## `forktrust new <slug>`

Create an isolated worktree on a fresh branch.

```
forktrust new <slug> [--from <ref>] [--scope "globs"] [--install] [--no-hooks] [--project <name>] [--json]
```

What happens:
1. Resolves the base ref. Cascade: `--from` if given, else `origin/<mainBranch>` if reachable, else local `<mainBranch>`, else `HEAD`.
2. Creates worktree at `<repo>/.forktrust/worktrees/<slug>/` on branch `fork/<slug>`.
3. Loads `.forktrustconfig` (if present), validates it, refuses on syntax error.
4. Runs `[[hooks.post_create]]` in declared order. `copy` and `symlink` always run. `command` hooks require the config to be trusted (see [trust gate](./safety-model.md#trust-gate)).
5. Allocates a port block if `[ports]` is configured; writes `<slug>/.env.local` with the `ManagedHeader`.
6. Adds `.forktrust/` to `.git/info/exclude` (idempotent).
7. Emits the worktree path + summary (or JSON).

Flags:
- `--from <ref>` — explicit base ref. Useful for forking off a feature branch or a specific commit. Empty string is rejected.
- `--install` — run the configured install command (legacy; prefer `[[hooks.post_create]] type="command"` in `.forktrustconfig`).
- `--no-hooks` — skip command hooks (copy/symlink still run; trust gate also skipped). Useful when an agent should not execute arbitrary scripts.
- `--scope "globs"` — declare the change contract for this task: a comma-separated list of glob patterns. `finish` will refuse (exit 16) if the diff touches files outside these globs. See [scope.md](./scope.md). Example: `--scope "internal/auth/**, go.mod"`.

Example:

```bash
forktrust new fix-payment
# ==> created .forktrust/worktrees/fix-payment on branch fork/fix-payment from origin/main
```

JSON:

```bash
forktrust new fix-payment --json
```

```json
{
  "project": "myapp",
  "slug": "fix-payment",
  "worktree_path": "/Users/me/code/myapp/.forktrust/worktrees/fix-payment",
  "branch": "fork/fix-payment",
  "branch_reused": false,
  "env_files_copied": 1,
  "hooks_run": ["copy .env -> .env", "symlink node_modules -> node_modules"],
  "ports": [3000, 3009],
  "predicted_overlaps": []
}
```

---

## `forktrust list`

List every worktree across every registered project.

```
forktrust list [--json]
```

Example:

```
PROJECT    SLUG          BRANCH               PATH                                              DIRTY
myapp      fix-payment   fork/fix-payment     /Users/me/code/myapp/.forktrust/worktrees/...     2
myapp      (main)        main                 /Users/me/code/myapp                              0
otherapp   add-auth      fork/add-auth        /Users/me/code/otherapp/.forktrust/...            0
```

JSON: array of `worktreeEntry` — see [json-schema.md](./json-schema.md#listresult).

---

## `forktrust status`

Per-worktree dashboard: dirty count, ahead/behind, allocated ports, age.

```
forktrust status [--watch] [--interval 5s] [--project <name>] [--json]
```

Flags:
- `--watch` / `-w` — auto-refresh until Ctrl-C. Use for live monitoring.
- `--interval <dur>` — refresh interval (default `5s`). Accepts Go duration syntax: `200ms`, `2s`, `1m`.
- `--project <name>` / `-p <name>` — limit to one registered project.

Example:

```
PROJECT  SLUG          BRANCH            DIRTY  AHEAD  BEHIND  PORTS       AGE
myapp    fix-payment   fork/fix-payment      2      3       0  3000-3009   2h
myapp    add-search    fork/add-search       0      1       0  3010-3019   4h
```

`AHEAD` is computed against the cascade target (origin/main, then local main). If no reference resolves, shown as `?` and `ahead_known: false` in JSON.

---

## `forktrust finish <slug>`

The canonical "ship it" command: commit + merge + push + cleanup.

```
forktrust finish <slug> [--message "<text>"] [--no-verify] [--no-scope] [--dry-run] [--project <name>] [--json]
```

Pipeline (all pre-flight checks happen BEFORE any mutation):

1. **PRE-FLIGHT** — refusals are pure reads, no side effects:
   - Resolve `aheadRef` cascade. No reference reachable → exit 12.
   - Worktree has ignored files (forktrust-managed `.env.local` excluded) → exit 14.
   - Main checkout on wrong branch → exit 10.
   - Main checkout dirty → exit 3.
   - `[verify]` commands (if configured, unless `--no-verify`) → exit 15.
   - Scope contract (if `<repo>/.forktrust/scopes/<slug>.toml` exists, unless `--no-scope`) → exit 16.
2. Commit uncommitted WIP on the worktree branch (message: `--message` or `WIP: <slug>`).
3. Compute commits-ahead. If 0 → fast-path: remove worktree + release ports + delete branch.
4. Pull origin/main if origin is configured.
5. `git merge --no-ff` worktree branch → main. **Conflict → abort merge → exit 2.**
6. Push main to origin (if origin) → push fail → exit 4.
7. Remove worktree.
8. Release port block (always, even if step 9 fails).
9. `git branch -D <branch>`. Failure → exit 13 (worktree already gone, branch lingers).

Flags:
- `-m, --message <text>` — commit message for the auto-WIP commit. Default: `WIP: <slug>`.
- `--no-verify` — skip the `[verify]` gate (prints a stderr WARNING listing the skipped commands). JSON: `no_verify: true`. Use only when you have already verified manually.
- `--no-scope` — skip the change-contract scope check (prints a stderr WARNING listing the allowed globs). JSON: `no_scope: true`. Use only when you have already reviewed out-of-scope edits.
- `--dry-run` — emit the would-refuse reason without executing. Exit 0; the JSON's `would_refuse` field tells you what (if anything) would have blocked. Note: dry-run does NOT execute verify commands (they would be a side effect); it reports `verify_configured` + `verify_ran_commands` so consumers know what the real command would do.

Example success:

```bash
forktrust finish fix-payment
```

Example dry-run JSON:

```json
{
  "project": "myapp",
  "slug": "fix-payment",
  "worktree_path": "...",
  "branch": "fork/fix-payment",
  "main_branch": "main",
  "dry_run": true,
  "uncommitted_files": 2,
  "committed_wip": false,
  "commits_ahead": 3,
  "main_dirty": 0,
  "main_current_branch": "main",
  "would_refuse": "",
  "has_origin": true,
  "merged": false,
  "pushed": false,
  "worktree_removed": false,
  "branch_deleted": false,
  "branch_kept": false
}
```

When `would_refuse` is non-empty, the string ends with `(exit N)` so an agent can extract the exit code the real command would return.

---

## `forktrust rm <slug>`

Abandon a worktree. Always snapshots WIP to `wip/<branch>-YYYYMMDD-HHMMSS-<sha7>` on origin first (never-lose-WIP).

```
forktrust rm <slug> [--force] [--dry-run] [--project <name>] [--json]
```

Pipeline:

1. Resolve branch (`git branch --show-current` in the worktree). Detached HEAD → exit 1.
2. Compute commits-ahead.
3. **PRE-FLIGHT** (skipped by `--force`):
   - `aheadKnown == false` → exit 12 (would not know what to back up).
   - Ignored files in worktree → exit 14.
   - Work to snapshot (`dirty > 0 || ahead > 0`) but no origin → exit 9.
4. Snapshot path (skipped by `--force`):
   - Commit uncommitted WIP.
   - Recompute `wip/<branch>-<stamp>-<sha7>` after the commit so SHA reflects the final tip (collision-proof).
   - `git push origin HEAD:refs/heads/<wip-branch>`. Failure → exit 5 (worktree NOT removed).
5. Remove worktree.
6. Release port block.
7. Delete local branch unless we forced and had work (then the local branch stays, so the commits are still reachable).

Flags:
- `--force` — skip all guards. Drops uncommitted work. Worktree removed unconditionally; local branch kept if there was work, deleted if clean.
- `--dry-run` — preview. The JSON's `would_refuse` and `would_push_wip` describe the plan.

JSON dry-run example (with ignored files):

```json
{
  "project": "myapp",
  "slug": "fix-payment",
  "branch": "fork/fix-payment",
  "dry_run": true,
  "force": false,
  "uncommitted_files": 1,
  "commits_ahead": 0,
  "ahead_known": true,
  "would_push_wip": false,
  "would_refuse": "worktree has 1 ignored file(s) that would be permanently deleted (exit 14). Move them out or use --force.",
  "worktree_removed": false,
  "branch_deleted": false,
  "branch_kept": false
}
```

---

## `forktrust cd <slug>`

Print the absolute worktree path to stdout. Nothing else. Designed for shell `cd` integration.

```
forktrust cd <slug> [--project <name>]
```

```bash
forktrust cd fix-payment
# /Users/me/code/myapp/.forktrust/worktrees/fix-payment
```

Use the `ft` shell function (see [shell-integration.md](./shell-integration.md)) to wrap this into a single-word `ft fix-payment` command.

Exit 6 if slug not found, exit 7 if ambiguous.

---

## `forktrust shell <slug>`

Open an interactive `$SHELL` inside the worktree.

```
forktrust shell <slug> [--project <name>]
```

The subshell:
- has `cwd` set to the worktree
- inherits the current environment, plus `FORKTRUST_SLUG=<slug>` so prompts/scripts can detect the active worktree
- when it exits, control returns to where you ran `forktrust shell`

```bash
forktrust shell fix-payment
# (you are now in a subshell at .forktrust/worktrees/fix-payment)
# $ echo $FORKTRUST_SLUG
# fix-payment
# $ exit
```

`$SHELL` defaults to `/bin/sh` if the env var is unset.

---

## `forktrust exec <slug> -- <cmd> [args...]`

Run a command with `cwd` set to the worktree. Stdin/stdout/stderr are inherited; exit code is propagated.

```
forktrust exec <slug> [--project <name>] -- <cmd> [args...]
```

The `--` separator is conventional (so flags after it go to your command, not to forktrust). Recommended.

```bash
forktrust exec fix-payment -- npm test
forktrust exec fix-payment -- git status
forktrust exec fix-payment -- npm run dev -- --port 4000
```

---

## `forktrust mcp`

Run as a Model Context Protocol stdio server. Shipped in v0.7.6. See [mcp.md](./mcp.md) for the full guide.

```
forktrust mcp
```

Reads newline-delimited JSON-RPC 2.0 requests from stdin and writes responses to stdout. Designed to be spawned by an MCP client (Claude Code, Cursor, etc.), not invoked interactively.

10 tools exposed: `forktrust_list`, `forktrust_status`, `forktrust_new`, `forktrust_cd`, `forktrust_finish`, `forktrust_rm`, `forktrust_scope`, `forktrust_pr`, `forktrust_pr_status`, `forktrust_doctor`.

Configure in Claude Code's `settings.json`:

```json
{
  "mcpServers": {
    "forktrust": {"command": "forktrust", "args": ["mcp"]}
  }
}
```

---

## `forktrust pr <slug>`

Open a GitHub PR for the worktree's branch instead of merging locally. Shipped in v0.7.4. See [pr.md](./pr.md) for the full guide.

```
forktrust pr <slug>
  [--title "<text>"] [--body "<text>"] [--base <branch>] [--draft]
  [--no-verify] [--no-scope]
  [--project <name>] [--dry-run] [--json]
```

Pipeline:

1. **PRE-FLIGHT**:
   - `gh` available (installed + authenticated) → else exit 17
   - origin remote → else exit 9
   - aheadRef resolves → else exit 12
   - `[verify]` (unless `--no-verify`) → else exit 15
   - scope contract (unless `--no-scope`) → else exit 16
2. Auto-WIP commit if dirty.
3. `git push -u origin <branch>` → else exit 4.
4. `gh pr view <branch>` — if PR exists, print URL; idempotent.
5. Otherwise `gh pr create` → else exit 18.

Worktree stays alive after `pr`. Clean up with `forktrust rm <slug>` after merge.

Title/body auto-generated from commit subjects if not provided.

---

## `forktrust pr-status <slug>`

Show the GitHub PR status for a worktree.

```
forktrust pr-status <slug> [--project <name>] [--json]
```

Reports number, URL, state, mergeable, review decision, CI checks summary, title, author, diff stats, updated time.

Exit 0 even when there is no PR for the branch (JSON: `pr_exists: false`).

Exit codes: 6 (no slug), 7 (ambiguous), 9 (no origin), 17 (gh unavailable).

---

## `forktrust scope <slug>`

Manage the change-contract scope for a worktree. Shipped in v0.7.3. See [scope.md](./scope.md) for the full guide.

```
forktrust scope <slug> [--set "globs"] [--clear] [--check] [--project <name>] [--json]
```

Modes (mutually exclusive):

- **No mode flag** — print the current scope (or `"no scope set"`).
- `--set "a/**, b/**"` — replace the scope with this comma-separated glob list. Whitespace tolerated.
- `--clear` — remove the scope file. Worktree becomes unrestricted.
- `--check` — evaluate the worktree diff against the scope and exit 16 if any file is out-of-scope. Use this in CI or as a standalone check.

Flag: `--json` for structured output.

Examples:

```bash
# Inspect
forktrust scope my-task
forktrust scope my-task --json | jq .allowed

# Set
forktrust scope my-task --set "internal/auth/**, go.mod"

# Re-check before finish
forktrust scope my-task --check && forktrust finish my-task

# Widen mid-flight if needed
forktrust scope my-task --set "internal/auth/**, internal/session/**"

# Remove (back to unrestricted)
forktrust scope my-task --clear
```

Exit codes:
- 0 — show/set/clear succeeded, or `--check` passed
- 6 — no worktree matching slug
- 7 — slug matches worktrees in multiple projects
- 12 — `--check` could not resolve main ref to diff against
- 16 — `--check` found out-of-scope edits

---

## `forktrust ai <slug>`

Launch a configured AI tool (Claude Code, Cursor, Aider, Codex, …) inside the worktree.

```
forktrust ai <slug> [--tool <name>] [--project <name>]
forktrust ai --list                    # list supported adapters
forktrust ai --set-default <tool>      # save tool as ai.default in user config
```

Flags:
- `--tool <name>` — AI adapter to launch (overrides `ai.default` in user config). Adapters known: `claude`, `cursor`, `aider`, `codex`, `cline`, `continue`, `opencode`, `gemini`, `auggie`.
- `--list` — print supported adapters and exit.
- `--set-default <tool>` — store the tool as your default in the forktrust user config; future `forktrust ai <slug>` uses it without `--tool`.

The adapter is just `cd <worktree> && <tool>` under the hood. Errors propagate from the tool.

---

## `forktrust doctor`

Run a health check across the forktrust install and all registered projects.

```
forktrust doctor [--project <name>] [--json]
```

Checks:

**Global:**
- `git` binary present
- `gh` binary present (warn if missing; needed for v0.7.4+ PR mode)
- `brew-version` matches running binary (warns if you skipped `brew upgrade`)
- ports store path resolvable

**Per project:**
- repo path exists and contains `.git/`
- `mainBranch` resolves (origin or local)
- `.forktrust/` is in `.git/info/exclude`
- `.forktrustconfig` is syntactically valid (or absent — both ok)
- trust gate: command hooks require trusted config

Exit 0 if all checks pass (warnings are allowed). Exit 1 if any check fails.

Human output:

```
forktrust doctor (v0.7.1)
----------------------------------------
[ ok ] git                    global: git version 2.50.1
[ ok ] gh                     global: gh version 2.92.0
[ ok ] brew-version           global: forktrust 0.7.1
[ ok ] ports-store            global: /Users/me/Library/Application Support/forktrust/ports.json
[ ok ] repo-path              myapp: /Users/me/code/myapp
[ ok ] main-ref               myapp: origin/main resolves
[ ok ] exclude-entry          myapp: .forktrust/ in .git/info/exclude
[ ok ] repo-config            myapp: 2 post_create hook(s); ports configured
[ ok ] trust-gate             myapp: command hooks trusted (SHA pinned)
----------------------------------------
summary: 9 ok, 0 warn, 0 fail
```

JSON:

```json
{
  "version": "0.7.1",
  "checks": [
    {"name": "git", "scope": "global", "status": "ok", "message": "git version 2.50.1"},
    {"name": "main-ref", "scope": "myapp", "status": "fail", "message": "no \"main\" reference found", "hint": "push or create \"main\" first; finish/rm will exit 12 otherwise"}
  ],
  "summary": {"ok": 8, "warn": 0, "fail": 1}
}
```

---

## `forktrust trust [path]`

Approve a repo's `.forktrustconfig` so its `command` hooks may execute.

```
forktrust trust [path]              # trust the given repo (or cwd's repo if omitted)
forktrust trust --list              # show all trusted repos
forktrust trust --revoke [path]     # remove the given repo from the trust list
```

Trust is per-repo and is **SHA-pinned**: any edit to `.forktrustconfig` auto-revokes trust until you re-run `forktrust trust`. This prevents a malicious commit from silently injecting shell hooks.

Trust store: `~/.config/forktrust/trust.toml` (or platform equivalent).

---

## `forktrust config`

Manage the project registry (which repos forktrust knows about).

```
forktrust config add <path> [name]    # register a repo (defaults name to basename(path))
forktrust config list                 # show all registered projects
forktrust config remove <name>        # unregister
forktrust config path                 # print path of the user config file
```

`config add` rejects a path that resolves to the same canonical directory as an already-registered one (via `filepath.EvalSymlinks`). This prevents the "same repo registered twice under different names" footgun that breaks `resolveWorktree`.

User config: `~/.config/forktrust/config.toml`.

---

## `forktrust agent-docs`

Print a copy-paste-ready AI-integration snippet to stdout.

```
forktrust agent-docs                  # print to stdout
forktrust agent-docs >> AGENTS.md     # append to your project's agent doc
forktrust agent-docs >> CLAUDE.md
```

The snippet is the same content as [ai-integration.md](./ai-integration.md), version-pinned to the running binary so it never drifts.

---

## `forktrust --version`

Print the binary version.

```bash
forktrust --version
# forktrust version 0.7.1
```

When building from source without `goreleaser`, this prints `dev`.
