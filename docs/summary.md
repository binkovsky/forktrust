# Summary contract (v0.7.7)

`[summary]` is a per-repo declaration of what commit messages on a worktree
branch must look like. It is enforced by `forktrust finish` and
`forktrust pr` (exit 19 on violation), and previewable via
`forktrust summary <slug> --check`. The contract closes the third side of
the v0.7.x merge gate: **verify** (tests pass) + **scope** (what was
touched) + **summary** (why it was touched).

## Why

`[scope]` ensures the *what* of a change stays within bounds. `[summary]`
ensures the *why* — every commit articulates intent in a structure your
reviewers, ticket system, and changelog generator can consume. Without
this gate an AI agent (or a hurried human) can land a green-CI scope-clean
change titled "wip stuff" — technically correct, audit-hostile.

## Where it lives

In `.forktrustconfig` at the repo root, alongside `[verify]` and `[ports]`:

```toml
[summary]
required               = true
min_body_length        = 20
max_body_length        = 2000
require_subject_prefix = ["feat", "fix", "refactor", "docs", "chore", "test", "perf", "build", "ci", "style", "revert"]
require_ticket_pattern = "[A-Z]+-[0-9]+"     # e.g. JIRA-1234
forbidden_patterns     = ["TODO", "WIP", "TBD"]
```

All fields are optional; omit a field to disable that rule. A `[summary]`
section with every rule disabled is flagged as a no-op by `forktrust doctor`.

## Rules

| Field | Type | Meaning |
|---|---|---|
| `required` | bool | At least one commit must exist in the worktree's range. Empty branches refuse. |
| `min_body_length` | int | Body (everything after the subject + blank-line separator) must be ≥ N bytes. Subject-only commits have body length 0. Disable with 0. |
| `max_body_length` | int | Body must be ≤ N bytes. Disable with 0. |
| `require_subject_prefix` | list | Subject must start with `<prefix>: ` or `<prefix>(scope): ` or `<prefix>!: ` (Conventional Commits). Disable with empty list. |
| `require_ticket_pattern` | regex | A Go regex that must match SOMEWHERE in the commit message (subject + body). Compiled at config-load. Disable with empty string. |
| `forbidden_patterns` | list | Case-insensitive substrings NOT allowed anywhere in subject + body. Useful to ban `WIP`, `TODO`, `fixup!`. Disable with empty list. |

All rules are checked against EVERY commit in the worktree's range
(`origin/<main>..HEAD`, falling back to `<main>..HEAD`). Each failed rule
produces one entry in `summary_violations`. Rules are independent
(no short-circuit) so you see the full picture in one run.

## When the gate runs

The gate is part of `forktrust finish`'s and `forktrust pr`'s pre-flight,
in this order:

```
1. trust gate (.forktrustconfig hooks)
2. main-branch & main-dirty checks
3. ignored-files refusal (exit 14)
4. verify gate         (exit 15)
5. scope gate          (exit 16)
6. summary gate        (exit 19)   ← here
7. auto-WIP commit, merge, push, cleanup
```

If the gate fails, NO git mutation happens — no WIP commit, no merge,
no push. The JSON envelope on stdout contains `summary_violations` for
machine consumption.

### Auto-WIP refusal

If `[summary]` is configured AND the worktree has uncommitted changes,
finish/pr REFUSE BEFORE auto-committing the WIP — the auto-generated
`WIP: <slug>` message would never satisfy your contract. You'll see:

```
REFUSE: [summary] contract is declared and the worktree has 3 uncommitted change(s).
Auto-WIP would not satisfy your commit-message rules. Commit your work yourself, e.g.:
  git -C <path> add -A && git -C <path> commit -m "<your message>"
Or pass --no-summary to bypass (not recommended).
```

This is the right semantics: the contract exists so YOU describe the
change, not so forktrust hides poor descriptions behind an autogen.

## Bypass

`--no-summary` skips the gate and prints a stderr warning. Use only when
you've reviewed the commits manually and the contract failure is wrong
for this single case. Agents must NOT use it without explicit user
consent — same rules as `--no-verify` and `--no-scope`.

## CLI

```bash
forktrust summary <slug>                # print the declared contract
forktrust summary <slug> --check        # evaluate worktree commits; exit 19 on violation
forktrust summary <slug> --check --json # machine-readable check output
```

JSON shape:

```json
{
  "project": "myproj",
  "slug": "fix-bug",
  "configured": true,
  "required": true,
  "min_body_length": 20,
  "require_subject_prefix": ["feat","fix","docs"],
  "checked": true,
  "passed": false,
  "commits": 2,
  "violation_count": 3,
  "violations": [
    {
      "commit_sha": "abc1234567...",
      "subject": "wip stuff",
      "rule": "require_subject_prefix",
      "reason": "subject must start with one of [feat fix docs] followed by optional \"(scope)\" and \": \""
    },
    {
      "commit_sha": "abc1234567...",
      "subject": "wip stuff",
      "rule": "min_body_length",
      "reason": "body is 0 bytes; minimum is 20"
    },
    {
      "commit_sha": "abc1234567...",
      "subject": "wip stuff",
      "rule": "forbidden_patterns",
      "reason": "contains forbidden substring \"WIP\" (case-insensitive)"
    }
  ],
  "action": "check"
}
```

## JSON envelope fields on finish / pr

Added to `forktrust finish --json` and `forktrust pr --json`:

| Field | Meaning |
|---|---|
| `summary_configured` | `.forktrustconfig` has a `[summary]` section |
| `summary_checked` | the gate was evaluated (false on `--no-summary` or no config) |
| `summary_passed` | every commit satisfied every rule |
| `summary_commits` | number of commits inspected (informational) |
| `summary_violations` | array of `{commit_sha, subject, rule, reason}` (capped at 100) |
| `summary_violation_count` | total count (may exceed `summary_violations` if truncated) |
| `no_summary` | `--no-summary` bypassed the gate |

## MCP

The `forktrust_summary` MCP tool wraps the standalone command:

```json
{
  "name": "forktrust_summary",
  "arguments": {
    "slug": "fix-bug",
    "check": true
  }
}
```

The `forktrust_finish` and `forktrust_pr` MCP tools accept a `no_summary`
boolean for the same bypass semantics. See [mcp.md](./mcp.md).

## Doctor

`forktrust doctor` reports a `summary-config` check per project:

```
[OK]   myproj: summary-config — 4 rule(s) active: required, min_body_length=20, subject_prefix(11), forbidden(3)
[WARN] myproj: summary-config — [summary] section present but every rule is disabled (no-op)
```

## Common patterns

### Conventional Commits

```toml
[summary]
required               = true
require_subject_prefix = ["feat", "fix", "refactor", "docs", "chore", "test", "perf", "build", "ci", "style", "revert"]
```

### JIRA-linked

```toml
[summary]
required               = true
require_ticket_pattern = "[A-Z]+-[0-9]+"
min_body_length        = 30
```

### Explicit-no-WIP

```toml
[summary]
forbidden_patterns = ["WIP", "TODO", "FIXME", "fixup!", "DO NOT MERGE"]
```

### Strict combo (recommended for shipping branches)

```toml
[summary]
required               = true
min_body_length        = 50
require_subject_prefix = ["feat", "fix", "refactor", "docs", "chore", "test"]
require_ticket_pattern = "[A-Z]+-[0-9]+"
forbidden_patterns     = ["WIP", "TODO", "TBD"]
```

## Interaction with verify and scope

The three gates are independent. A run fails on the FIRST gate that
refuses; if you want to see all failures in one pass, run each standalone
command:

```bash
forktrust finish <slug> --dry-run --json   # shows would_refuse + all eval fields
forktrust scope   <slug> --check --json
forktrust summary <slug> --check --json
```

## Limitations

- Merge commits are excluded from the check (`git log --no-merges`).
- Trailers (`Co-Authored-By:`, etc.) ARE part of the body for length
  calculations and forbidden-pattern checks. Tune `min_body_length`
  upward if you want trailers to count toward the requirement.
- Only commit messages are checked. PR titles/bodies are out of scope —
  forktrust does not own those.
- `require_ticket_pattern` is a Go regex (`regexp/syntax`), not PCRE.
  Lookahead / lookbehind are unavailable. For typical ticket formats
  (`PROJ-123`, `#1234`) this is rarely an issue.

## Version history

- v0.7.7: introduced.
