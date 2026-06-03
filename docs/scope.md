# Change contracts (`--scope`)

A **scope** is a per-worktree contract declaring which file globs a task is allowed to modify. `forktrust finish` refuses to merge if the diff touches anything outside the contract. Shipped in v0.7.3.

This solves "scope creep" — the AI-agent failure mode where an agent that was asked to fix the login flow ends up also editing `package-lock.json`, `Makefile`, `.gitignore`, and three random refactors that weren't asked for.

## Quick start

Declare a scope at creation:

```bash
forktrust new fix-payment --scope "internal/payment/**, internal/db/migrations/**"
```

Edit normally. When you `finish`, the gate runs:

```bash
forktrust finish fix-payment
# If diff touched only the declared globs: merge proceeds.
# If diff touched something else:
#   REFUSE: scope gate failed — 2 file(s) outside the declared --scope:
#     - package-lock.json
#     - Makefile
#   ... exit 16
```

## How it works

When you pass `--scope`, forktrust writes `<repo>/.forktrust/scopes/<slug>.toml`:

```toml
allowed = ["internal/payment/**", "internal/db/migrations/**"]
created_by = "forktrust new fix-payment --scope ..."
created_at = "2026-06-01T15:30:45Z"
```

The file is stored alongside the worktree tree but NOT inside the worktree, so it doesn't pollute the worktree's own diff.

At `finish` (and `forktrust scope <slug> --check`):

1. Compute the diff: `git diff --name-only <aheadRef>...HEAD` + uncommitted + untracked.
2. Match each changed file against every glob in `allowed` (case-sensitive, forward-slash, doublestar semantics).
3. If any file matches NONE of the globs → exit 16 with the violation list.

Match order:
- `**` crosses directory boundaries
- `*` is one segment
- `?` is one char
- `[abc]` classes
- `{a,b}` alternation
- Plain literals (e.g. `go.mod`) match the exact file at the repo root

## Glob recipes

| Goal | Pattern |
|---|---|
| Everything under `internal/auth/` | `internal/auth/**` |
| Top-level Go files in `cmd/api/` | `cmd/api/*.go` |
| All `.md` under `docs/` at any depth | `docs/*.md`, `docs/**/*.md` |
| One exact file | `go.mod` |
| Multiple directories | `{src,internal}/**` |
| Allow everything (override-style) | `**` |

`**.md` does NOT work — doublestar v4 reads `**` strictly as a directory crossing. Use `docs/**/*.md` or two patterns.

## Commands

### `forktrust new <slug> --scope "globs"`

Saves the scope at task creation. Comma-separated; whitespace tolerated:

```bash
forktrust new auth-fix --scope "internal/auth/**"
forktrust new full-stack --scope "internal/auth/**, web/src/**, docs/auth.md"
forktrust new emergency --scope "**"   # unrestricted (explicit)
```

JSON output now includes a `scope` field with the parsed globs.

### `forktrust scope <slug>`

Inspect or manipulate the scope of an existing worktree.

```bash
# Show
forktrust scope my-task                       # human output
forktrust scope my-task --json                # JSON

# Set / replace
forktrust scope my-task --set "src/**, go.mod"

# Clear (removes restrictions)
forktrust scope my-task --clear

# Check (evaluate against current diff; exits 16 on violation)
forktrust scope my-task --check
forktrust scope my-task --check --json
```

`--set`, `--clear`, and `--check` are mutually exclusive.

### `forktrust finish --no-scope`

Bypass the gate. Prints a `WARNING:` to stderr listing the allowed globs, sets `no_scope: true` in JSON. The merge proceeds even if the diff is out-of-scope.

Use only when you have already reviewed the diff manually. **Agents must never `--no-scope` without explicit user consent** — the gate exists to prevent shipping scope creep.

## JSON additions to `finish --json`

```json
{
  "scope_configured": true,
  "scope_checked": true,
  "scope_passed": false,
  "scope_allowed": ["internal/auth/**"],
  "scope_violations": ["package-lock.json", "Makefile"],
  "scope_violation_count": 2,
  "no_scope": false
}
```

| Field | Type | Meaning |
|---|---|---|
| `scope_configured` | bool | True if a `<repo>/.forktrust/scopes/<slug>.toml` exists |
| `scope_checked` | bool | True if the gate was actually evaluated (false on `--no-scope` or no scope file) |
| `scope_passed` | bool | True if every changed file matches at least one allowed glob |
| `scope_allowed` | string[] | The declared allowed globs (for inspection) |
| `scope_violations` | string[] | Up to 100 violating files (full count in `scope_violation_count`) |
| `scope_violation_count` | int | Full violation count, even if `scope_violations` is truncated |
| `no_scope` | bool | True if `--no-scope` was used |

## Dry-run

Unlike verify, scope check IS executed in dry-run (it's a pure read: `git diff` + glob match, no side effects). `forktrust finish --dry-run --json` accurately predicts exit 16:

```bash
forktrust finish my-task --dry-run --json | jq '.scope_passed, .would_refuse'
# false
# "scope gate failed: 2 file(s) outside declared --scope (exit 16). Widen scope, revert out-of-scope edits, or pass --no-scope to bypass."
```

This means `--dry-run` is a perfect agent reconnaissance tool: it tells you whether `finish` will succeed without executing anything.

## Lifecycle

- Scope is **created** by `forktrust new --scope` OR `forktrust scope --set`.
- Scope is **destroyed** by `forktrust finish` (successful), `forktrust rm`, or `forktrust scope --clear`.
- A new `forktrust new <same-slug>` starts with a fresh state (no inherited scope).

## Workflow patterns

### Strict-AI: every task declares a scope

Configure your prompts so the AI always picks a scope:

```text
For every task, first run `forktrust new <slug> --scope "<globs that match
the work>"`. If the task is broader than you can scope upfront, scope to
"**/*.md" and the directories you're confident in, then widen with
`forktrust scope <slug> --set "..."` if you discover you need more.
NEVER use --no-scope without explicit human confirmation.
```

### CI gate

```bash
# In CI, before allowing a PR merge:
forktrust scope $TASK_SLUG --check --json | jq -e .passed
```

### Widen scope mid-task

You started small, the work turned out to need more:

```bash
forktrust scope my-task                          # see current
forktrust scope my-task --set "src/**, tests/**, go.mod"   # update
```

The previous globs are replaced (not appended) — pass everything you want allowed.

### Discover what's needed

Don't know exactly what to scope? Start permissive, edit, then run check:

```bash
forktrust new exploratory --scope "**"     # unrestricted
# ... edit ...
forktrust scope exploratory --check        # see all changed files
# Decide which dirs are intentional, tighten:
forktrust scope exploratory --set "<the right globs>"
forktrust finish exploratory               # verify the tightened scope still passes
```

## Threat addressed

Without scope contracts, an AI agent can:
- Edit files outside what was asked, "while you're there" style.
- Touch `package-lock.json`, lockfiles, generated code without intent.
- Refactor adjacent code that wasn't reviewed.

With scope:
- The agent's allowed surface is explicit.
- `finish` is a contract: "I only touched what I said I would."
- Out-of-scope edits surface before merge, not after deploy.

## Migration

Existing worktrees have no scope file → unrestricted (backwards compatible). Add scopes incrementally:

```bash
forktrust scope existing-task --set "<globs>"
```

No code change required.
