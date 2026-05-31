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
2. cd into the worktree path printed by step 1, then make your changes there.
   Do NOT edit files in the main checkout. The worktree path looks like
   ` + "`.forktrust/worktrees/<task-slug>/`" + `.

3. When the task is done, ship the change:
` + "   ```bash\n" + `   forktrust finish <task-slug>      # commit WIP + merge to main + push + cleanup
` + "   ```\n" + `
4. If you want to throw the work away (without merging), use:
` + "   ```bash\n" + `   forktrust rm <task-slug>          # snapshots WIP to wip/<branch>-YYYYMMDD on origin first
` + "   ```\n" + `
### Hard safety guarantees you can rely on

- ` + "`finish`" + ` REFUSES on merge conflict (no auto-resolve). If exit code is 2,
  the merge has a conflict; show the user and ask before doing anything.
- ` + "`finish`" + ` REFUSES if the main checkout has uncommitted changes (exit 3).
- ` + "`rm`" + ` ALWAYS pushes uncommitted WIP to ` + "`wip/<branch>-YYYYMMDD`" + ` on origin
  before removing. Work is never lost without ` + "`--force`" + `.
- Worktree directories are auto-added to ` + "`.git/info/exclude`" + ` so they never
  pollute ` + "`git status`" + ` for the main checkout.

### Machine-readable output

Every command supports ` + "`--json`" + ` for parseable output:

` + "```bash\n" + `forktrust list --json
forktrust new my-task --json
forktrust finish my-task --json
` + "```\n" + `
Exit codes are stable:

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

### Inspecting state

` + "```bash\n" + `forktrust list --json               # all worktrees across all registered repos
forktrust finish <slug> --dry-run   # show the plan without executing
forktrust rm <slug> --dry-run       # show the abandon plan without executing
` + "```\n" + `
### When NOT to use forktrust

- For tiny, read-only investigations (just grep, no edits).
- When the user explicitly asks you to edit in main with the override
  ` + "`edit in main, no worktree`" + `.

### Quick reference

| Command | Effect |
|---|---|
| ` + "`forktrust new <slug>`" + ` | Create isolated worktree on branch ` + "`fork/<slug>`" + ` |
| ` + "`forktrust list`" + ` | List all worktrees (use ` + "`--json`" + `) |
| ` + "`forktrust finish <slug>`" + ` | Merge to main, push, cleanup (refuses on conflict) |
| ` + "`forktrust rm <slug>`" + ` | Abandon, pushing WIP to ` + "`wip/*`" + ` first |
| ` + "`forktrust ai <slug>`" + ` | Launch configured AI tool in the worktree |
| ` + "`forktrust trust`" + ` | Approve this repo's ` + "`.forktrustconfig`" + ` command hooks |

Install with ` + "`brew install binkovsky/forktrust/forktrust`" + ` or download from
https://github.com/binkovsky/forktrust/releases.
`
