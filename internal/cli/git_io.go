package cli

import (
	"io"
	"os"

	"github.com/binkovsky/forktrust/internal/git"
)

// gitStream is a thin wrapper around git.RunStream that routes git's stdout to
// the right place for the current output mode. In --json mode we send git's
// chatter to stderr so stdout stays clean for the JSON document.
//
// Callers should use this instead of git.RunStream from inside command handlers
// that may emit JSON.
func gitStream(jsonMode bool, dir string, args ...string) error {
	stdout := io.Writer(os.Stdout)
	if jsonMode {
		stdout = os.Stderr
	}
	return git.RunStreamTo(dir, stdout, os.Stderr, args...)
}

// addWorktreeNew creates a new worktree on a new branch, respecting JSON mode.
func addWorktreeNew(jsonMode bool, repo, path, branch string) error {
	return gitStream(jsonMode, repo, "worktree", "add", "-b", branch, path)
}

// addWorktreeExisting creates a new worktree checking out an existing branch.
func addWorktreeExisting(jsonMode bool, repo, path, branch string) error {
	return gitStream(jsonMode, repo, "worktree", "add", path, branch)
}

// removeWorktree removes a registered worktree, respecting JSON mode.
func removeWorktree(jsonMode bool, repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	return gitStream(jsonMode, repo, args...)
}
