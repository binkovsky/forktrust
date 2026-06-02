package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
)

var cdProject string

var cdCmd = &cobra.Command{
	Use:   "cd <slug>",
	Short: "Print the absolute path of a worktree (for shell cd integration)",
	Long: `Print ONLY the absolute path of the worktree for the given slug to stdout,
with no decorations. Designed to be wrapped by a shell function for "cd"
ergonomics:

  # Add to ~/.zshrc or ~/.bashrc:
  ft() {
    local p
    p="$(forktrust cd "$1" 2>/dev/null)" || { echo "forktrust: no worktree '$1'" >&2; return 1; }
    cd "$p" || return 1
  }

Then "ft my-task" cd's into the worktree. The 2>/dev/null hides error output
on bad slugs; the shell function prints its own message.

Exit codes:
  0  success (path printed)
  6  no worktree matching slug
  7  slug matches worktrees in multiple projects (use --project)`,
	Args: cobra.ExactArgs(1),
	RunE: runCd,
}

func init() {
	cdCmd.Flags().StringVarP(&cdProject, "project", "p", "", "target project name (required if more than one is registered)")
}

func runCd(_ *cobra.Command, args []string) error {
	slug := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, wtPath, err := resolveWorktree(cfg, cdProject, slug)
	if err != nil {
		return err
	}
	// Print ONLY the path. No newline-trim issues: shell command substitution
	// strips the trailing newline. Anything else (warnings, decorations) would
	// break "cd $(forktrust cd ...)".
	fmt.Println(wtPath)
	return nil
}
