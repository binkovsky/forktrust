// Package git wraps the git CLI for the operations forktrust needs.
// It intentionally avoids go-git so the binary stays small and the behavior
// matches what the user would see running git by hand.
package git

import (
	"bytes"
	"fmt"
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
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
