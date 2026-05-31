package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
)

var execProject string

var execCmd = &cobra.Command{
	Use:                   "exec <slug> -- <cmd> [args...]",
	Short:                 "Run a command in the worktree's directory",
	DisableFlagsInUseLine: false,
	Long: `Run an arbitrary command with cwd set to the worktree path. Stdin/stdout/stderr
are inherited so interactive tools work. The exit code is the command's exit code.

The "--" separator is conventional (so flags after it are passed to the command,
not parsed by forktrust). It is recommended but not strictly required.

Examples:
  forktrust exec my-task -- npm test
  forktrust exec my-task -- git status
  forktrust exec my-task -- pwd
  forktrust exec my-task -- npm run dev -- --port 4000`,
	Args: cobra.MinimumNArgs(2),
	RunE: runExec,
}

func init() {
	execCmd.Flags().StringVarP(&execProject, "project", "p", "", "target project name (required if more than one is registered)")
}

func runExec(cmd *cobra.Command, args []string) error {
	slug := args[0]
	// Detect "--" position. If present, command starts after it; otherwise
	// the command starts at arg index 1.
	dashAt := cmd.ArgsLenAtDash()
	var cmdArgs []string
	if dashAt > 0 {
		cmdArgs = args[dashAt:]
	} else {
		cmdArgs = args[1:]
	}
	if len(cmdArgs) == 0 {
		return fmt.Errorf("missing command. usage: forktrust exec <slug> -- <cmd> [args...]")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, wtPath, err := resolveWorktree(cfg, execProject, slug)
	if err != nil {
		return err
	}

	c := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	c.Dir = wtPath
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		// Propagate the command's exit code through our coded-error pipeline.
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &CodedError{Code: exitErr.ExitCode(), Err: err}
		}
		return err
	}
	return nil
}
