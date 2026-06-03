package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
)

// defaultVerifyTimeout is the per-command wall-clock limit applied when
// `[verify].timeout_seconds` is unset. 10 minutes covers typical CI-style
// test suites without trapping reasonable users; for longer suites, set
// `timeout_seconds` explicitly.
const defaultVerifyTimeout = 10 * time.Minute

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
// Streaming: stdout/stderr of each command are streamed live to the user's
// stderr (so they see test output as it scrolls and stdout stays clean for
// the JSON envelope). The streaming sink ALSO tees into a CAPPED ring buffer
// so a verbose command (e.g. `go test ./... -v` printing gigabytes) cannot
// OOM forktrust — only the last 16 KiB are retained for verify_output.
//
// Each command runs under a context deadline of TimeoutSeconds (default
// `defaultVerifyTimeout`, 10 minutes per command). A hung command (e.g.
// accidental `npm run dev` in the verify list) is killed and reported as
// "timed out after N seconds" rather than blocking finish indefinitely.
func runVerify(jsonMode bool, wtPath string, v *config.VerifyConfig) verifyResult {
	r := verifyResult{Configured: v != nil}
	if v == nil {
		return r
	}

	// Pre-parse .env.local so PORT and friends are visible to verify commands
	// (same contract as command hooks: KEY=VALUE only, NO shell eval).
	envExtras := parseEnvLocalIntoEnv(filepath.Join(wtPath, ".env.local"))

	// Determine timeout per command. 0 in config means "explicit no-timeout"
	// (documented as not recommended); unset (negative impossible due to
	// validation) takes the default 10 minutes.
	var timeout time.Duration
	switch {
	case v.TimeoutSeconds > 0:
		timeout = time.Duration(v.TimeoutSeconds) * time.Second
	case v.TimeoutSeconds == 0:
		// Distinguish "user explicitly set 0" from "field absent": BurntSushi
		// gives us 0 in both cases. Treat as "use default" since the explicit
		// no-timeout is a footgun and users wanting it can set a very large
		// value (e.g. 86400).
		timeout = defaultVerifyTimeout
	}

	for _, command := range v.Commands {
		r.RanCommands = append(r.RanCommands, command)

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		c := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec
		c.Dir = wtPath
		c.Env = append(os.Environ(), envExtras...)

		// Stream to stderr + tee into a CAPPED ring buffer (16 KiB tail).
		// Unbounded captured-bytes was a real OOM hazard on verbose test runs.
		captured := newRingBuffer(16 * 1024)
		streamSink := io.MultiWriter(os.Stderr, captured)
		c.Stdout = streamSink
		c.Stderr = streamSink

		if !jsonMode {
			fmt.Fprintf(os.Stderr, "==> verify: %s\n", command)
		}
		err := c.Run()
		cancel()
		if err != nil {
			r.FailedCommand = command
			// Distinguish timeout from non-zero exit so the user knows whether
			// to fix tests or extend the timeout budget.
			switch {
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				r.FailureReason = fmt.Sprintf("command timed out after %s (raise [verify].timeout_seconds if your tests genuinely need longer)", timeout)
			default:
				r.FailureReason = fmt.Sprintf("command exited non-zero: %v", err)
			}
			r.Output = captured.String()
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
// lives) and adds a "..." prefix if truncated. Used for git-output capture
// where the source is already bounded; for verify subprocess output use the
// ring-buffer streaming approach (newRingBuffer) instead.
func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "... (truncated)\n" + s[len(s)-max:]
}

// ringBuffer is a bounded write-only sink that keeps only the LAST `cap`
// bytes written, discarding older ones. Used as a tee target for verify
// command output so a verbose runner can never OOM forktrust. Safe for
// io.Writer use; not safe for concurrent writers.
type ringBuffer struct {
	buf    []byte // length-cap fixed slice
	head   int    // index of next byte to write
	filled bool   // true once we've wrapped around
}

func newRingBuffer(cap int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, cap)}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	cap := len(r.buf)
	if cap == 0 {
		return len(p), nil
	}
	// If the incoming chunk is larger than capacity, only the tail matters.
	if len(p) >= cap {
		copy(r.buf, p[len(p)-cap:])
		r.head = 0
		r.filled = true
		return len(p), nil
	}
	// Otherwise copy into the ring starting at head, wrapping as needed.
	n := copy(r.buf[r.head:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
		r.head = len(p) - n
		r.filled = true
	} else {
		r.head += n
		if r.head == cap {
			r.head = 0
			r.filled = true
		}
	}
	return len(p), nil
}

// String returns the captured bytes in write order. When the ring has
// wrapped, the result is prefixed with "... (truncated)\n" so consumers know
// they only see the tail.
func (r *ringBuffer) String() string {
	if !r.filled {
		return string(r.buf[:r.head])
	}
	// Reassemble: bytes from head to end, then from start to head.
	out := make([]byte, 0, len(r.buf)+24)
	out = append(out, "... (truncated)\n"...)
	out = append(out, r.buf[r.head:]...)
	out = append(out, r.buf[:r.head]...)
	return string(out)
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
