package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var agentDocsCmd = &cobra.Command{
	Use:   "agent-docs",
	Short: "Print a ready-to-paste AGENTS.md / CLAUDE.md snippet for AI integration",
	Long: `Prints a concise, copy-paste-ready snippet that teaches any AI coding agent
(Claude Code, Cursor, Aider, Codex, etc.) how to use forktrust in this project.

Drop it into your project's AGENTS.md, CLAUDE.md, .cursorrules, or any other
AI-facing doc. The agent will read it on session start and use forktrust
correctly without further setup.

Pipe to a file:
  forktrust agent-docs >> AGENTS.md
  forktrust agent-docs >> CLAUDE.md

Or print to stdout for ad-hoc copy:
  forktrust agent-docs`,
	RunE: runAgentDocs,
}

func runAgentDocs(_ *cobra.Command, _ []string) error {
	fmt.Print(agentDocsTemplate)
	return nil
}

// agentDocsTemplate is the canonical AI-integration snippet. Keep it concise
// and stable across releases — users paste it once and expect it to keep working.
const agentDocsTemplate = `## Using forktrust for parallel tasks

This project uses [forktrust](https://github.com/binkovsky/forktrust) to isolate
parallel AI coding sessions in their own git worktrees. Each task gets its own
copy of the codebase so concurrent agents do not step on each other.

### Workflow (do this for every new task)

1. Create an isolated worktree for the task:
` + "   ```bash\n" + `   forktrust new <task-slug>          # creates .forktrust/worktrees/<task-slug>
` + "   ```\n" + `
2. cd into the worktree. Either use the path printed in step 1, or use the
   ` + "`ft`" + ` shell function (see "Shell integration" below):
` + "   ```bash\n" + `   ft <task-slug>                     # cd into the worktree
   forktrust shell <task-slug>        # or: open an interactive shell there
` + "   ```\n" + `   Do NOT edit files in the main checkout.

3. When the task is done, ship the change:
` + "   ```bash\n" + `   forktrust finish <task-slug>       # commit WIP + merge to main + push + cleanup
` + "   ```\n" + `
4. If you want to throw the work away (without merging), use:
` + "   ```bash\n" + `   forktrust rm <task-slug>           # snapshots WIP to wip/<branch>-YYYYMMDD-HHMMSS-<sha7> on origin first
` + "   ```\n" + `
### Hard safety guarantees you can rely on

- **Pre-flight refusal:** ` + "`finish`" + ` and ` + "`rm`" + ` perform ALL refusal checks BEFORE
  any git mutation. If the command exits non-zero, no commit, merge, push, or
  branch deletion happened. Side effects are never partial.
- **Dry-run matches reality:** ` + "`finish --dry-run`" + ` and ` + "`rm --dry-run`" + ` predict
  the exact exit code the real command would return. Use ` + "`--json`" + ` and read
  ` + "`would_refuse`" + ` to decide before executing.
- ` + "`finish`" + ` REFUSES on merge conflict (exit 2). No auto-resolve, ever.
- ` + "`finish`" + ` REFUSES if main checkout is dirty (exit 3) or on wrong branch (exit 10).
- ` + "`rm`" + ` and ` + "`finish`" + ` REFUSE if the worktree has ignored files (exit 14) —
  ` + "`git worktree remove`" + ` deletes them silently. Move them out or use ` + "`--force`" + `.
- ` + "`rm`" + ` ALWAYS pushes uncommitted WIP to ` + "`wip/<branch>-YYYYMMDD-HHMMSS-<sha7>`" + ` on
  origin before removing. Work is never lost without ` + "`--force`" + `.
  The ` + "`<sha7>`" + ` suffix is the short SHA of the WIP commit; it makes wip/*
  names unique even when ` + "`rm`" + ` is run on different branches in the same second.
- **Verify gate:** if ` + "`.forktrustconfig`" + ` declares ` + "`[verify].commands`" + `, ` + "`finish`" + `
  REFUSES (exit 15) unless every command exits zero. The commands typically
  run tests / linters / builds. ` + "`--no-verify`" + ` bypasses with a stderr warning,
  but agents must NOT use it without explicit user consent — the gate exists
  to prevent shipping broken code.
- **Scope gate (change contract):** declare allowed paths upfront with
  ` + "`forktrust new <slug> --scope \"globs\"`" + ` (or ` + "`forktrust scope <slug> --set ...`" + `).
  ` + "`finish`" + ` REFUSES (exit 16) if the diff touches files outside the declared
  globs. JSON shows ` + "`scope_violations`" + ` (list) and ` + "`scope_violation_count`" + `.
  ` + "`--no-scope`" + ` bypass with warning; agents must NOT bypass without consent.

### Machine-readable output

The state-modifying commands (` + "`new`" + `, ` + "`list`" + `, ` + "`status`" + `, ` + "`finish`" + `, ` + "`rm`" + `) and
` + "`doctor`" + ` support ` + "`--json`" + ` for parseable output:

` + "```bash\n" + `forktrust list --json
forktrust status --json
forktrust new my-task --json
forktrust finish my-task --json
forktrust finish my-task --dry-run --json   # preview, no execution
forktrust doctor --json                     # health report
` + "```\n" + `
Exit codes are stable across releases. Switch on them, not on stderr text:

| Code | Meaning | What an agent should do |
|---|---|---|
| 0  | success | proceed |
| 2  | merge conflict (refuse to auto-resolve) | surface to user, ask before doing anything |
| 3  | main worktree is dirty | tell user to commit/stash main, then retry |
| 4  | push to origin failed | check auth/network, retry |
| 5  | wip/* snapshot push failed (worktree NOT removed) | check origin auth; retry ` + "`rm`" + ` |
| 6  | no worktree matching slug | check slug; list with ` + "`forktrust list`" + ` |
| 7  | slug matches multiple projects | pass ` + "`--project <name>`" + ` |
| 8  | hook failed (or untrusted command hook) | inspect ` + "`.forktrustconfig`" + `; run ` + "`forktrust trust`" + ` if hooks are safe |
| 9  | no origin remote configured | add a remote or run with ` + "`--force`" + ` |
| 10 | main checkout is on the wrong branch | tell user to ` + "`git checkout <main>`" + ` |
| 11 | cwd is in an unregistered git repo | run ` + "`forktrust config add .`" + ` |
| 12 | could not determine ahead count (no main reference resolved) | push origin/main, or re-run ` + "`rm --force`" + ` |
| 13 | rm/finish: worktree removed but ` + "`git branch -D`" + ` failed (branch lingers) | tell user the branch is still around |
| 14 | worktree has ignored files that would be lost | tell user to move them out, or pass ` + "`--force`" + ` |
| 15 | [verify] gate failed (test/lint/build command returned non-zero) | surface ` + "`verify_failed_command`" + ` + tail of ` + "`verify_output`" + ` to user; NEVER ` + "`--no-verify`" + ` without consent |
| 16 | scope contract violated (diff touches files outside declared --scope) | surface ` + "`scope_violations`" + ` to user; NEVER ` + "`--no-scope`" + ` without consent |

### Inspecting state

` + "```bash\n" + `forktrust list --json               # all worktrees across all registered repos
forktrust status --json             # per-worktree dirty/ahead/behind/ports
forktrust finish <slug> --dry-run   # show the plan without executing
forktrust rm <slug> --dry-run       # show the abandon plan without executing
forktrust doctor                    # health check: origin, main, hooks, ports
` + "```\n" + `
### Shell integration

Add to ~/.zshrc or ~/.bashrc for ` + "`cd`" + ` ergonomics:

` + "```bash\n" + `ft() {
  local p
  p="$(forktrust cd "$1" 2>/dev/null)" || { echo "forktrust: no worktree '$1'" >&2; return 1; }
  cd "$p" || return 1
}
` + "```\n" + `
Then ` + "`ft my-task`" + ` cd's into the worktree. Or use ` + "`forktrust shell <slug>`" + ` to
spawn a subshell inside the worktree (with ` + "`FORKTRUST_SLUG`" + ` exported).

### When NOT to use forktrust

- For tiny, read-only investigations (just grep, no edits).
- When the user explicitly asks you to edit in main with the override
  ` + "`edit in main, no worktree`" + `.

### Quick reference

| Command | Effect |
|---|---|
| ` + "`forktrust new <slug>`" + ` | Create isolated worktree on branch ` + "`fork/<slug>`" + ` |
| ` + "`forktrust list`" + ` | List all worktrees (use ` + "`--json`" + `) |
| ` + "`forktrust status`" + ` | Per-worktree dirty/ahead/ports (use ` + "`--watch`" + ` for live) |
| ` + "`forktrust cd <slug>`" + ` | Print absolute worktree path (for shell ` + "`cd`" + `) |
| ` + "`forktrust shell <slug>`" + ` | Open interactive shell in the worktree |
| ` + "`forktrust exec <slug> -- <cmd>`" + ` | Run a command in the worktree directory |
| ` + "`forktrust finish <slug>`" + ` | Merge to main, push, cleanup (refuses on conflict) |
| ` + "`forktrust rm <slug>`" + ` | Abandon, pushing WIP to ` + "`wip/*`" + ` first |
| ` + "`forktrust ai <slug>`" + ` | Launch configured AI tool in the worktree |
| ` + "`forktrust scope <slug>`" + ` | Show / set / clear / check change-contract scope |
| ` + "`forktrust trust`" + ` | Approve this repo's ` + "`.forktrustconfig`" + ` command hooks |
| ` + "`forktrust doctor`" + ` | Health check (origin, main ref, hooks, ports) |

Install with ` + "`brew install binkovsky/forktrust/forktrust`" + ` or download from
https://github.com/binkovsky/forktrust/releases.
`
