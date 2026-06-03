# JSON output schemas

Every command that supports `--json` emits a single JSON object (or array, for `list`) on stdout. Fields are **stable**: existing field names and types are part of the public contract.

Stderr stays human-readable. Always route stderr to a separate stream when piping JSON to a parser.

## Stability rules

- Existing field names will not be removed or renamed across releases.
- Existing field types will not change (e.g. a `string` field stays a string).
- New fields may be added at any time. Parsers must tolerate unknown fields.
- Field order is not guaranteed (Go map iteration). Parse as object, not by position.
- All boolean fields are real `true`/`false`, never strings.
- Path fields are absolute when known.
- Time/date fields use Go's default ISO-8601 (e.g. `2026-06-01T12:34:56Z`).

## `forktrust new --json`

```json
{
  "project": "myapp",
  "slug": "fix-payment",
  "worktree_path": "/Users/me/code/myapp/.forktrust/worktrees/fix-payment",
  "branch": "fork/fix-payment",
  "branch_reused": false,
  "env_files_copied": 1,
  "hooks_run": [
    "copy .env -> .env",
    "symlink node_modules -> node_modules",
    "command: pnpm install"
  ],
  "ports": [3010, 3019],
  "predicted_overlaps": ["src/auth/login.ts"]
}
```

| Field | Type | Meaning |
|---|---|---|
| `project` | string | Registered project name |
| `slug` | string | The slug you passed |
| `worktree_path` | string | Absolute worktree path |
| `branch` | string | `fork/<slug>` |
| `branch_reused` | bool | True if the branch already existed and was checked out |
| `env_files_copied` | int | Count of `.env*` files copied in via zero-config or hooks |
| `hooks_run` | string[] | Summary of each `[[hooks.post_create]]` that fired (in order) |
| `ports` | int[2] | `[start, end]` inclusive, or absent if `[ports]` not configured |
| `predicted_overlaps` | string[] | Files this slug is likely to touch that other live worktrees also touch (experimental) |

## `forktrust list --json`

```json
{
  "worktrees": [
    {
      "project": "myapp",
      "path": "/Users/me/code/myapp",
      "branch": "main",
      "detached": false,
      "dirty": 0,
      "is_main": true
    },
    {
      "project": "myapp",
      "path": "/Users/me/code/myapp/.forktrust/worktrees/fix-payment",
      "branch": "fork/fix-payment",
      "detached": false,
      "dirty": 2,
      "is_main": false
    }
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `worktrees[].project` | string | Project name |
| `worktrees[].path` | string | Absolute worktree path |
| `worktrees[].branch` | string | Short branch name, or `""` if detached |
| `worktrees[].detached` | bool | True if HEAD is detached |
| `worktrees[].dirty` | int | Count of changed + untracked files (NOT ignored) |
| `worktrees[].is_main` | bool | True for the main checkout |

## `forktrust status --json`

```json
{
  "worktrees": [
    {
      "project": "myapp",
      "slug": "",
      "path": "/Users/me/code/myapp",
      "branch": "main",
      "is_main": true,
      "ahead": 0,
      "behind": 0,
      "ahead_known": true,
      "dirty": 0,
      "age_seconds": 0,
      "port_start": 0,
      "port_end": 0
    },
    {
      "project": "myapp",
      "slug": "fix-payment",
      "path": "/Users/me/code/myapp/.forktrust/worktrees/fix-payment",
      "branch": "fork/fix-payment",
      "is_main": false,
      "ahead": 3,
      "behind": 0,
      "ahead_known": true,
      "dirty": 2,
      "age_seconds": 7200,
      "port_start": 3010,
      "port_end": 3019
    }
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `slug` | string | Path relative to `.forktrust/worktrees/`. Empty for main checkout. Slashes preserved (e.g. `feature/foo`). |
| `branch` | string | Short branch name |
| `is_main` | bool | True for main checkout |
| `ahead` | int | Commits ahead of cascade target (`origin/main` then local `main`). Meaningless if `ahead_known: false`. |
| `behind` | int | Commits behind cascade target |
| `ahead_known` | bool | False ⇒ no cascade target resolved; `ahead`/`behind` are 0 but not because branches are equal |
| `dirty` | int | Uncommitted + untracked files (NOT ignored) |
| `age_seconds` | int | Seconds since worktree directory mtime |
| `port_start` / `port_end` | int | Allocated port block; 0 if none |

## `forktrust finish --json`

```json
{
  "project": "myapp",
  "slug": "fix-payment",
  "worktree_path": "/Users/me/code/myapp/.forktrust/worktrees/fix-payment",
  "branch": "fork/fix-payment",
  "main_branch": "main",
  "dry_run": false,
  "uncommitted_files": 2,
  "committed_wip": true,
  "commits_ahead": 3,
  "main_dirty": 0,
  "main_current_branch": "main",
  "would_refuse": "",
  "has_origin": true,
  "verify_configured": true,
  "verify_ran": true,
  "verify_passed": true,
  "verify_ran_commands": ["go build ./...", "go test ./..."],
  "merged": true,
  "pushed": true,
  "worktree_removed": true,
  "branch_deleted": true,
  "branch_kept": false
}
```

| Field | Type | Meaning |
|---|---|---|
| `dry_run` | bool | True if `--dry-run` was used |
| `uncommitted_files` | int | Pre-run dirty count |
| `committed_wip` | bool | True if an auto-WIP commit was created in step 2 |
| `commits_ahead` | int | Counted against the resolved cascade target |
| `main_dirty` | int | Dirty count of main checkout (dry-run only) |
| `main_current_branch` | string | Current branch of main checkout (dry-run only) |
| `would_refuse` | string | DRY-RUN ONLY. Empty if real `finish` would proceed; otherwise human reason ending in `(exit N)`. |
| `has_origin` | bool | True if `origin` remote configured |
| `verify_configured` | bool | True if `.forktrustconfig` has a `[verify]` section |
| `verify_ran` | bool | True if verify was executed in this invocation (false on `--no-verify`, dry-run, or no config) |
| `verify_passed` | bool | True if every verify command exited zero AND `require_clean` (if set) is satisfied |
| `verify_ran_commands` | string[] | The verify commands actually attempted (in order). On failure, last entry is the failing command. In dry-run: the full list that would run. |
| `verify_failed_command` | string | The command that failed verify; empty if verify passed or was skipped |
| `verify_output` | string | Tail (~8 KiB) of the failing command's combined stdout+stderr; empty if verify passed or was skipped |
| `no_verify` | bool | True when `--no-verify` bypassed the gate |
| `merged` | bool | True if merge step completed |
| `pushed` | bool | True if push to origin completed |
| `worktree_removed` | bool | True if worktree dir was removed |
| `branch_deleted` | bool | True if `git branch -D` succeeded |
| `branch_kept` | bool | True if branch -D failed (exit 13) — mutually exclusive with `branch_deleted` |

### Dry-run parity

When `dry_run: true`, the `would_refuse` field is the contract. If empty, the real `finish` would proceed to merge. If non-empty, the real `finish` would exit with the code at the end of the string.

The check order in `would_refuse` exactly matches `runFinish`:
1. `!aheadKnown` → exit 12
2. `ignoredN > 0` → exit 14
3. `current != mainBranch` → exit 10
4. `mainDirty > 0` → exit 3

Verify (exit 15) is NOT predicted by `would_refuse` — dry-run does not execute verify commands (they have side effects). Use `verify_configured` + `verify_ran_commands` to know what real `finish` will run.

## `forktrust rm --json`

```json
{
  "project": "myapp",
  "slug": "fix-payment",
  "worktree_path": "/Users/me/code/myapp/.forktrust/worktrees/fix-payment",
  "branch": "fork/fix-payment",
  "dry_run": false,
  "force": false,
  "uncommitted_files": 1,
  "commits_ahead": 0,
  "ahead_known": true,
  "would_push_wip": false,
  "would_refuse": "",
  "wip_branch": "wip/fix-payment-20260601-153045-a1b2c3d",
  "wip_pushed": true,
  "worktree_removed": true,
  "branch_deleted": true,
  "branch_kept": false
}
```

| Field | Type | Meaning |
|---|---|---|
| `force` | bool | True if `--force` was used |
| `uncommitted_files` | int | Pre-run dirty count |
| `commits_ahead` | int | Cascade-target ahead count |
| `ahead_known` | bool | False ⇒ cascade target didn't resolve |
| `would_push_wip` | bool | DRY-RUN: would the real `rm` push to wip/*? |
| `would_refuse` | string | DRY-RUN: same contract as finish |
| `wip_branch` | string | Final wip/* name (only set if push succeeded). Format: `wip/<branch-without-fork-prefix>-YYYYMMDD-HHMMSS-<sha7>`. |
| `wip_pushed` | bool | True if wip/* push to origin succeeded |
| `worktree_removed` | bool | True if worktree dir removed |
| `branch_deleted` | bool | True if local branch deleted |
| `branch_kept` | bool | True if branch -D failed (exit 13) |

Dry-run order:
1. `ignoredN > 0` AND not `--force` → exit 14
2. `!aheadKnown` AND not `--force` → exit 12
3. `hadWork && !--force && !hasOrigin` → exit 9

## `forktrust doctor --json`

```json
{
  "version": "0.7.1",
  "checks": [
    {
      "name": "git",
      "scope": "global",
      "status": "ok",
      "message": "git version 2.50.1"
    },
    {
      "name": "main-ref",
      "scope": "myapp",
      "status": "fail",
      "message": "no \"main\" reference found (tried origin and local)",
      "hint": "push or create \"main\" first; finish/rm will exit 12 otherwise"
    }
  ],
  "summary": {
    "ok": 7,
    "warn": 1,
    "fail": 1
  }
}
```

| Field | Type | Meaning |
|---|---|---|
| `version` | string | forktrust binary version |
| `checks[].name` | string | Stable check identifier (e.g. `git`, `main-ref`, `trust-gate`) |
| `checks[].scope` | string | `"global"` or a project name |
| `checks[].status` | string | One of `"ok"`, `"warn"`, `"fail"` |
| `checks[].message` | string | Human description |
| `checks[].hint` | string | Optional fix hint |
| `summary` | object | Counts |

Process exit code: 0 if `summary.fail == 0`, 1 otherwise. Warnings don't fail.

## Standard error format on coded errors

When forktrust exits non-zero, stderr contains a human message. Programmatic callers should use the exit code, not parse this text. But for completeness, the shape is:

```
error: <short description>
```

For commands that emit JSON, the JSON object on stdout is still produced when possible (especially for `--dry-run` would-refuse cases). When a hard error prevents emission (e.g. project not found), only stderr is written.
