package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
)

// verifyResult describes the outcome of running a [verify] section.
type verifyResult struct {
	// Configured is true if the repo had a [verify] section at all.
	// (When false, all other fields are zero and the caller treats verify as "skipped (no config)".)
	Configured bool
	// Passed is true if every command exited zero AND require_clean (if set) was satisfied.
	Passed bool
	// RanCommands is the list of commands that were actually invoked (in order).
	// On failure, the failing command is the last element.
	RanCommands []string
	// FailedCommand is the command that failed, empty if Passed.
	FailedCommand string
	// FailureReason explains why verify failed (command non-zero, or "require_clean: worktree dirty after verify").
	FailureReason string
	// Output is the combined stdout+stderr of the failing command (truncated to ~8 KiB).
	Output string
}

// runVerify executes the [verify] section against the worktree. Returns a
// verifyResult describing the outcome; the caller decides whether to refuse.
//
// Streaming: in non-JSON mode the stdout/stderr of each command is streamed
// live to the user's stderr (so they see test output as it scrolls). In JSON
// mode we still stream to stderr (keep stdout pristine for the JSON envelope)
// and ALSO capture into a tee'd buffer so the final result can include the
// failing command's tail in the JSON output.
func runVerify(jsonMode bool, wtPath string, v *config.VerifyConfig) verifyResult {
	r := verifyResult{Configured: v != nil}
	if v == nil {
		return r
	}

	// Pre-parse .env.local so PORT and friends are visible to verify commands
	// (same contract as command hooks: KEY=VALUE only, NO shell eval).
	envExtras := parseEnvLocalIntoEnv(filepath.Join(wtPath, ".env.local"))

	for _, command := range v.Commands {
		r.RanCommands = append(r.RanCommands, command)

		c := exec.Command("sh", "-c", command) //nolint:gosec
		c.Dir = wtPath
		c.Env = append(os.Environ(), envExtras...)

		// Always stream to stderr (preserve stdout for JSON callers). Tee into
		// a capped buffer so we can report the tail in the JSON result.
		var captured bytes.Buffer
		streamSink := io.MultiWriter(os.Stderr, &captured)
		c.Stdout = streamSink
		c.Stderr = streamSink

		if !jsonMode {
			fmt.Fprintf(os.Stderr, "==> verify: %s\n", command)
		}
		if err := c.Run(); err != nil {
			r.FailedCommand = command
			r.FailureReason = fmt.Sprintf("command exited non-zero: %v", err)
			r.Output = truncateOutput(captured.String(), 8192)
			return r
		}
	}

	// require_clean: after every command passes, verify that the worktree is
	// still clean. This catches build artifacts that aren't .gitignore'd.
	if v.RequireClean {
		n, err := git.DirtyCount(wtPath)
		if err == nil && n > 0 {
			r.FailedCommand = "(require_clean)"
			r.FailureReason = fmt.Sprintf("require_clean: worktree has %d uncommitted file(s) after verify", n)
			out, _ := git.Run(wtPath, "status", "--short")
			r.Output = truncateOutput(out, 8192)
			return r
		}
	}

	r.Passed = true
	return r
}

// truncateOutput keeps the tail of large outputs (where the error usually
// lives) and adds a "..." prefix if truncated.
func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "... (truncated)\n" + s[len(s)-max:]
}

// envVarLineRE matches `KEY=VALUE` lines, capturing key. Used to safely lift
// KEY=VALUE pairs out of a .env.local without invoking a shell.
var envVarLineRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// parseEnvLocalIntoEnv reads a .env.local file (if present) and returns lines
// of the form "KEY=VALUE" suitable for appending to exec.Cmd.Env. NEVER runs
// a shell. Silently skips malformed lines.
func parseEnvLocalIntoEnv(path string) []string {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !envVarLineRE.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}
