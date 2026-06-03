# PR mode (`forktrust pr`)

Open a GitHub PR for a worktree branch instead of doing a direct local merge. Shipped in v0.7.4.

Use this when your workflow goes through code review. The worktree stays alive while the PR is reviewed; you clean up later with `forktrust rm` after merge.

## When to use which command

| You want to... | Use |
|---|---|
| Commit + merge locally + push main | `forktrust finish <slug>` |
| Open a PR; let humans review; merge via GitHub | `forktrust pr <slug>` |
| Check PR state (CI / approvals / mergeable) | `forktrust pr-status <slug>` |
| Clean up worktree after PR merged | `forktrust rm <slug>` (ahead==0 fast-path) |

## Quick start

```bash
forktrust new fix-payment --scope "internal/payment/**"
# ... edit, run tests ...
forktrust pr fix-payment              # → opens PR, prints URL
forktrust pr-status fix-payment       # → CI / approvals
# (reviewers approve, CI passes, you click Merge in the GitHub UI)
forktrust rm fix-payment              # → cleanup
```

## `forktrust pr <slug>`

```
forktrust pr <slug>
  [--title "<text>"]
  [--body  "<text>"]
  [--base  <branch>]       # default: project's mainBranch (usually "main")
  [--draft]                # open as draft
  [--no-verify]            # skip [verify] gate (with warning)
  [--no-scope]             # skip scope contract check (with warning)
  [--project <name>]
  [--dry-run]
  [--json]
```

### Pipeline

1. **Pre-flight** (no side effects yet):
   - `gh` available (installed + authenticated) → else exit 17
   - Origin remote configured → else exit 9
   - aheadRef resolves → else exit 12
   - `[verify]` (unless `--no-verify`) → else exit 15
   - Scope contract (unless `--no-scope`) → else exit 16
2. Auto-WIP commit if worktree is dirty.
3. `git push -u origin <branch>` → else exit 4.
4. `gh pr view <branch>` — if a PR already exists, just print its URL (idempotent).
5. Otherwise, `gh pr create --base <base> --head <branch> --title ... --body ...` → else exit 18.
6. Re-fetch PR info to populate JSON.
7. Worktree stays alive. Print URL + next-step hint.

### Title and body defaults

If `--title` is omitted: the first commit subject ahead of the base branch.
If `--body` is omitted: a bullet list of all commit subjects + a footer attributing the PR to forktrust.

You can always override:

```bash
forktrust pr fix-payment \
  --title "Fix payment race condition" \
  --body "Closes #1234. See test in TestPayment_Race."
```

### Idempotency

Re-running `forktrust pr <slug>` after the PR already exists:
- Pre-flight checks run normally.
- `git push` updates the branch with any new commits.
- `gh pr view` finds the existing PR; **no duplicate create call** is made.
- JSON: `pr_existed: true`, `pr_created: false`.

This means you can re-run `forktrust pr` after every batch of edits to push updates without thinking about whether a PR already exists.

### Dry-run

```bash
forktrust pr fix-payment --dry-run --json
```

Runs all pre-flight checks (including scope), reports the plan, but does NOT push or call gh. Verify commands are NOT executed (same exception as `finish --dry-run`).

## `forktrust pr-status <slug>`

```
forktrust pr-status <slug> [--project <name>] [--json]
```

Reads the GitHub PR associated with the branch and reports:

- **PR number, URL, state**: OPEN / CLOSED / MERGED, draft flag
- **mergeable**: MERGEABLE / CONFLICTING / UNKNOWN
- **review decision**: APPROVED / CHANGES_REQUESTED / REVIEW_REQUIRED / (empty if no required reviews)
- **CI checks summary**: overall state (SUCCESS / PENDING / FAILURE / NONE) plus per-conclusion counts
- title, base branch, author, additions/deletions/changed files, last update time

Exit 0 even when no PR exists for the branch (JSON: `pr_exists: false`). That's not an error — it just means no PR has been opened yet.

### Use in CI / agent loops

Wait for green CI + approval:

```bash
while true; do
    out=$(forktrust pr-status fix-payment --json)
    overall=$(echo "$out" | jq -r .checks.overall)
    review=$(echo "$out" | jq -r .review_decision)
    if [ "$overall" = "SUCCESS" ] && [ "$review" = "APPROVED" ]; then
        echo "Ready to merge."
        break
    fi
    sleep 30
done
```

## JSON schemas

### `pr --json` (`prResult`)

```json
{
  "project": "myapp",
  "slug": "fix-payment",
  "worktree_path": "/Users/me/code/myapp/.forktrust/worktrees/fix-payment",
  "branch": "fork/fix-payment",
  "base_branch": "main",
  "dry_run": false,
  "has_origin": true,
  "gh_available": true,

  "verify_configured": true,
  "verify_ran": true,
  "verify_passed": true,
  "verify_ran_commands": ["go test ./..."],
  "scope_configured": true,
  "scope_checked": true,
  "scope_passed": true,
  "scope_allowed": ["internal/payment/**"],

  "uncommitted_files": 0,
  "committed_wip": false,
  "branch_pushed": true,
  "pr_existed": false,
  "pr_created": true,
  "pr_number": 42,
  "pr_url": "https://github.com/owner/repo/pull/42",
  "pr_state": "OPEN",
  "pr_title": "Fix payment race condition",
  "pr_is_draft": false
}
```

All verify/scope/no_verify/no_scope fields have the same meaning as in `finishResult` (see [json-schema.md](./json-schema.md)).

### `pr-status --json` (`prStatusResult`)

```json
{
  "project": "myapp",
  "slug": "fix-payment",
  "branch": "fork/fix-payment",
  "gh_available": true,
  "pr_exists": true,
  "pr_number": 42,
  "pr_url": "https://github.com/owner/repo/pull/42",
  "pr_state": "OPEN",
  "pr_is_draft": false,
  "mergeable": "MERGEABLE",
  "review_decision": "APPROVED",
  "checks": {
    "overall": "SUCCESS",
    "total": 5,
    "passing": 5,
    "failing": 0,
    "pending": 0
  },
  "title": "Fix payment race condition",
  "base_branch": "main",
  "author": "dimas",
  "additions": 120,
  "deletions": 15,
  "changed_files": 7,
  "updated_at": "2026-06-01T15:00:00Z"
}
```

| Field | Type | Meaning |
|---|---|---|
| `pr_exists` | bool | False if no PR is open for this branch (other fields will be empty) |
| `mergeable` | string | GitHub's mergeable computation: `MERGEABLE`, `CONFLICTING`, or `UNKNOWN` (not yet computed) |
| `review_decision` | string | `APPROVED`, `CHANGES_REQUESTED`, `REVIEW_REQUIRED`, or `""` when no required reviewers |
| `checks.overall` | string | `SUCCESS` (all good), `PENDING` (waiting), `FAILURE` (at least one failed), `NONE` (no checks attached) |
| `checks.passing/failing/pending` | int | Counts by conclusion |

### Check status rollup logic

| Conclusion | Counts as |
|---|---|
| `SUCCESS`, `NEUTRAL`, `SKIPPED` | passing |
| `FAILURE`, `CANCELLED`, `TIMED_OUT`, `ACTION_REQUIRED`, `STARTUP_FAILURE` | failing |
| Status `QUEUED` / `IN_PROGRESS` / `PENDING` with no conclusion | pending |

`overall` is `FAILURE` if any check failed, otherwise `PENDING` if any is pending, otherwise `SUCCESS` (or `NONE` if no checks attached at all).

## Pre-flight reuse with `finish`

`pr` and `finish` share the same safety pre-flight where it makes sense:

|  | finish | pr |
|---|---|---|
| aheadRef resolves (exit 12) | yes | yes |
| Ignored files (exit 14) | yes | NO (worktree stays alive) |
| Main on right branch (exit 10) | yes | NO (no local merge) |
| Main dirty (exit 3) | yes | NO |
| Verify gate (exit 15) | yes | yes |
| Scope contract (exit 16) | yes | yes |
| gh available (exit 17) | NO | yes |

So `forktrust pr --dry-run --json` is the right tool to validate "would this PR open cleanly?" without side effects.

## Workflow patterns

### Hand off to human review

```bash
forktrust new feat-X --scope "feature_X/**"
# ... agent does work ...
forktrust pr feat-X
# Tell the human reviewer the URL, await approval+merge in GitHub UI.
forktrust pr-status feat-X --json | jq .checks.overall   # check periodically
# Once merged in GitHub, clean up:
forktrust rm feat-X
```

### Stacked drafts

Open a draft PR early so reviewers can see direction; promote to ready when done:

```bash
forktrust pr feat-X --draft
# ... iterate, re-run forktrust pr feat-X to push updates ...
gh pr ready feat-X    # promote to ready (or via UI)
```

### CI-driven auto-merge

Use `pr-status --json` in a CI gate that decides whether your bot can auto-merge:

```bash
status=$(forktrust pr-status $SLUG --json)
overall=$(echo "$status" | jq -r .checks.overall)
review=$(echo "$status" | jq -r .review_decision)
mergeable=$(echo "$status" | jq -r .mergeable)

if [ "$overall" = "SUCCESS" ] && [ "$review" = "APPROVED" ] && [ "$mergeable" = "MERGEABLE" ]; then
    gh pr merge $(echo "$status" | jq -r .pr_number) --squash --auto
fi
```

## Bypass flags

- `--no-verify` — skip the `[verify]` gate. Same semantics as `finish --no-verify`. Stderr WARNING.
- `--no-scope` — skip the scope contract. Same semantics as `finish --no-scope`. Stderr WARNING.

Agents must NOT use either without explicit user consent.

## Comparison with `finish`

| | `forktrust finish` | `forktrust pr` |
|---|---|---|
| Merges locally | yes | no |
| Pushes main | yes | no |
| Pushes branch | n/a | yes |
| Creates PR | no | yes |
| Removes worktree | yes | no (stays alive) |
| Refuse-on-conflict | yes | n/a (GitHub handles) |
| Verify gate | yes | yes |
| Scope gate | yes | yes |
| Needs `gh` | no | yes |
| Worktree after | gone | alive (clean up with `rm`) |

## Threat addressed

PRs are the human-review checkpoint. `forktrust pr` makes that the easiest path:

- Agents don't have permission to merge to main; they open PRs.
- Pre-flight (verify + scope) runs locally so reviewers never see broken-build PRs.
- `pr-status --json` is the structured handoff between agent and CI.

## Troubleshooting

See [exit-codes.md](./exit-codes.md#17--gh-cli-not-available) and [exit-codes.md#18](./exit-codes.md#18--gh-pr-create-failed) for exit 17/18 specifically.

If `gh pr create` succeeded but `pr_number` is 0 in JSON: the post-create `gh pr view` lookup failed. The PR IS open (the URL field is set); re-run `forktrust pr-status <slug>` to get the number.
