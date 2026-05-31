// Package git wraps the git CLI for the operations forktrust needs.
// It intentionally avoids go-git so the binary stays small and the behavior
// matches what the user would see running git by hand.
package git

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Run executes a git command in the given directory and returns trimmed stdout.
// Stderr is captured and surfaced in the returned error.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// RunStream runs git inheriting the parent's stdout/stderr — for user-visible
// operations like merge/push/commit where we want git's own output passed through.
func RunStream(dir string, args ...string) error {
	return RunStreamTo(dir, os.Stdout, os.Stderr, args...)
}

// RunStreamTo runs git directing stdout/stderr to the given writers. Use this
// when emitting JSON on stdout — pass os.Stderr for both so git's chatter
// doesn't pollute the JSON document.
func RunStreamTo(dir string, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
