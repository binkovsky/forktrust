package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
)

var shellProject string

var shellCmd = &cobra.Command{
	Use:   "shell <slug>",
	Short: "Open an interactive shell in the worktree",
	Long: `Spawn an interactive shell ($SHELL, or /bin/sh as fallback) with cwd set
to the worktree path. The shell inherits the current environment plus a
FORKTRUST_SLUG variable so prompts/scripts can detect the active worktree.

When you exit the shell, control returns to where you ran forktrust shell.

Examples:
  forktrust shell my-task
  forktrust shell my-task -p myrepo

Exit codes:
  0  success (shell exited cleanly)
  6  no worktree matching slug
  7  slug matches worktrees in multiple projects (use --project)`,
	Args: cobra.ExactArgs(1),
	RunE: runShellCmd,
}

func init() {
	shellCmd.Flags().StringVarP(&shellProject, "project", "p", "", "target project name (required if more than one is registered)")
}

func runShellCmd(_ *cobra.Command, args []string) error {
	slug := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, wtPath, err := resolveWorktree(cfg, shellProject, slug)
	if err != nil {
		return err
	}

	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/sh"
	}

	// Use exec.Command + run-then-propagate rather than syscall.Exec: cross-
	// platform, returns control to caller normally, and lets us add env vars.
	// Live stdio so the shell is fully interactive.
	c := newInteractiveShell(shellPath, wtPath, slug)
	if err := c.Run(); err != nil {
		if exitErr, ok := isExitError(err); ok {
			return &CodedError{Code: exitErr.ExitCode(), Err: fmt.Errorf("shell exited with status %d", exitErr.ExitCode())}
		}
		return err
	}
	return nil
}
