package cli

import (
	"errors"
	"os"
	"os/exec"
)

// newInteractiveShell returns an *exec.Cmd that, when Run, spawns an
// interactive shell at wtPath. Stdin/stdout/stderr are wired to os.* so the
// user gets a real interactive session. FORKTRUST_SLUG is exported so prompts
// or session scripts can detect which worktree they are in.
func newInteractiveShell(shellPath, wtPath, slug string) *exec.Cmd {
	c := exec.Command(shellPath)
	c.Dir = wtPath
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = append(os.Environ(), "FORKTRUST_SLUG="+slug)
	return c
}

// isExitError unwraps an *exec.ExitError without exposing exec import to
// every caller. Returns (*exec.ExitError, true) if the error is one.
func isExitError(err error) (*exec.ExitError, bool) {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee, true
	}
	return nil, false
}
