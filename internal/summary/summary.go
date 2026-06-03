// Package summary enforces the per-repo commit-message contract declared in
// .forktrustconfig's [summary] section. It is the v0.7.7 feature.
//
// The gate is applied in `forktrust finish` and `forktrust pr` pre-flight
// (after verify and scope). Non-compliance returns exit 19
// (ExitSummaryViolated) and the worktree is NOT merged/pushed.
//
// Rules supported:
//   - required: at least one commit must exist
//   - min_body_length / max_body_length: body byte-length window
//   - require_subject_prefix: subject must start with `<prefix>(scope)?: `
//   - require_ticket_pattern: regex must match anywhere in the commit message
//   - forbidden_patterns: case-insensitive substring NOT allowed in subject/body
//
// All commits in the range base..HEAD are checked. Every failing commit
// produces one violation entry per failed rule; rules are independent
// (no short-circuit), so the user sees the full picture in a single run.
package summary

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/binkovsky/forktrust/internal/config"
)

// Commit is the parsed view of one git commit's metadata.
type Commit struct {
	SHA     string // full 40-char OID
	Subject string // first line (no trailing newline)
	Body    string // everything after the blank line, with trailing whitespace trimmed
}

// Violation describes a single rule failure for a single commit.
// One commit can produce multiple violations (one per failed rule).
type Violation struct {
	CommitSHA string `json:"commit_sha"`
	Subject   string `json:"subject"`
	Rule      string `json:"rule"`   // machine-readable rule name (e.g. "min_body_length")
	Reason    string `json:"reason"` // human-readable explanation
}

// LoadCommits returns the commits reachable from headRef but not from baseRef,
// in topological order (newest first). baseRef may be empty — in that case
// the entire history of headRef is returned (useful for first-commit repos).
//
// Implementation: `git -C <repo> log <base>..<head> --format=...` with a NUL
// record separator so multi-line bodies do not collide with line-based parsing.
func LoadCommits(repoPath, baseRef, headRef string) ([]Commit, error) {
	if headRef == "" {
		return nil, fmt.Errorf("LoadCommits: headRef is required")
	}
	// %H = full SHA, %s = subject, %b = body. The %x00 NUL is our record
	// separator; subjects/bodies may contain newlines but never NULs.
	format := "%H%n%s%n%b%x00"
	rng := headRef
	if baseRef != "" {
		rng = baseRef + ".." + headRef
	}
	args := []string{"-C", repoPath, "log", rng, "--no-merges", "--format=" + format}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		// Surface the stderr to callers — it's almost always "bad revision"
		// when a ref is wrong, which is actionable.
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git log: %w (stderr: %s)", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git log: %w", err)
	}
	var commits []Commit
	for _, rec := range bytes.Split(out, []byte{0}) {
		// git inserts its own newline between commits, which sticks to the
		// front of the NEXT record after split-by-NUL. Trim it (and any
		// trailing newline from our own %x00-terminated record). We trim
		// only newlines, not spaces — commit bodies may start with
		// significant whitespace (e.g. code blocks) that we must preserve.
		rec = bytes.Trim(rec, "\n")
		if len(rec) == 0 {
			continue
		}
		// First newline splits SHA from rest; second splits subject from body.
		nl1 := bytes.IndexByte(rec, '\n')
		if nl1 < 0 {
			// Defensive: a record with no newline is malformed; skip rather
			// than crash. This should never happen with our format string.
			continue
		}
		sha := string(rec[:nl1])
		rest := rec[nl1+1:]
		nl2 := bytes.IndexByte(rest, '\n')
		var subject, body string
		if nl2 < 0 {
			subject = string(rest)
			body = ""
		} else {
			subject = string(rest[:nl2])
			body = strings.TrimSpace(string(rest[nl2+1:]))
		}
		commits = append(commits, Commit{SHA: sha, Subject: subject, Body: body})
	}
	return commits, nil
}

// Check validates each commit against cfg and returns all violations.
// Returns nil violations when cfg is nil (no contract → no failures).
// Order: violations follow commit order (LoadCommits' newest-first), and
// within a single commit, rules are checked in the deterministic order
// listed in the package doc.
func Check(commits []Commit, cfg *config.SummaryConfig) ([]Violation, error) {
	if cfg == nil {
		return nil, nil
	}
	var ticketRE *regexp.Regexp
	if cfg.RequireTicketPattern != "" {
		re, err := regexp.Compile(cfg.RequireTicketPattern)
		if err != nil {
			// This should have been caught by config Validate(); defense in depth.
			return nil, fmt.Errorf("require_ticket_pattern is invalid: %w", err)
		}
		ticketRE = re
	}
	// Precompute lowercased forbidden patterns once (we case-insensitively
	// check by lowercasing the haystack, not the needles, every time).
	lowerForbidden := make([]string, len(cfg.ForbiddenPatterns))
	for i, p := range cfg.ForbiddenPatterns {
		lowerForbidden[i] = strings.ToLower(p)
	}
	var violations []Violation
	if cfg.Required && len(commits) == 0 {
		violations = append(violations, Violation{
			Rule:   "required",
			Reason: "no commits in worktree range (required = true)",
		})
		return violations, nil
	}
	for _, c := range commits {
		// Subject prefix
		if len(cfg.RequireSubjectPrefix) > 0 {
			if !hasAnyPrefix(c.Subject, cfg.RequireSubjectPrefix) {
				violations = append(violations, Violation{
					CommitSHA: c.SHA, Subject: c.Subject,
					Rule:   "require_subject_prefix",
					Reason: fmt.Sprintf("subject must start with one of %v followed by optional \"(scope)\" and \": \"", cfg.RequireSubjectPrefix),
				})
			}
		}
		// Body length
		bodyLen := len(c.Body)
		if cfg.MinBodyLength > 0 && bodyLen < cfg.MinBodyLength {
			violations = append(violations, Violation{
				CommitSHA: c.SHA, Subject: c.Subject,
				Rule:   "min_body_length",
				Reason: fmt.Sprintf("body is %d bytes; minimum is %d", bodyLen, cfg.MinBodyLength),
			})
		}
		if cfg.MaxBodyLength > 0 && bodyLen > cfg.MaxBodyLength {
			violations = append(violations, Violation{
				CommitSHA: c.SHA, Subject: c.Subject,
				Rule:   "max_body_length",
				Reason: fmt.Sprintf("body is %d bytes; maximum is %d", bodyLen, cfg.MaxBodyLength),
			})
		}
		// Ticket regex (subject + body)
		if ticketRE != nil {
			haystack := c.Subject + "\n" + c.Body
			if !ticketRE.MatchString(haystack) {
				violations = append(violations, Violation{
					CommitSHA: c.SHA, Subject: c.Subject,
					Rule:   "require_ticket_pattern",
					Reason: fmt.Sprintf("no match for %q in subject or body", cfg.RequireTicketPattern),
				})
			}
		}
		// Forbidden patterns (case-insensitive substring)
		if len(lowerForbidden) > 0 {
			lowerHay := strings.ToLower(c.Subject + "\n" + c.Body)
			for i, low := range lowerForbidden {
				if strings.Contains(lowerHay, low) {
					violations = append(violations, Violation{
						CommitSHA: c.SHA, Subject: c.Subject,
						Rule:   "forbidden_patterns",
						Reason: fmt.Sprintf("contains forbidden substring %q (case-insensitive)", cfg.ForbiddenPatterns[i]),
					})
				}
			}
		}
	}
	return violations, nil
}

// hasAnyPrefix reports whether subject matches "<p>" or "<p>(scope): " or
// "<p>: " for any p in prefixes. Conventional Commits style. We allow an
// optional "!" before ":" to signal breaking changes (feat!: ...).
func hasAnyPrefix(subject string, prefixes []string) bool {
	for _, p := range prefixes {
		if !strings.HasPrefix(subject, p) {
			continue
		}
		rest := subject[len(p):]
		// Accept "p: " | "p(...): " | "p!: " | "p(...)!: "
		if strings.HasPrefix(rest, ": ") {
			return true
		}
		if strings.HasPrefix(rest, "!: ") {
			return true
		}
		if strings.HasPrefix(rest, "(") {
			// Find the closing paren; what follows must be ": " or "!: ".
			end := strings.Index(rest, ")")
			if end < 0 {
				continue
			}
			after := rest[end+1:]
			if strings.HasPrefix(after, ": ") || strings.HasPrefix(after, "!: ") {
				return true
			}
		}
	}
	return false
}
