package cli

import (
	"fmt"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/summary"
)

// summaryCheckResult is the structured outcome of evaluating a worktree's
// commit messages against the [summary] section in .forktrustconfig. Same
// shape across runFinish, previewFinish, runPR, and the standalone
// `forktrust summary <slug> --check` command.
type summaryCheckResult struct {
	Configured     bool                 // [summary] section present in .forktrustconfig
	Ran            bool                 // the check actually executed
	Passed         bool                 // no violations
	Commits        int                  // number of commits in range (informational)
	Violations     []summary.Violation  // up to ViolationCount (may be truncated)
	ViolationCount int                  // total violations found
}

// evalSummary runs the commit-message contract check for a worktree:
//  1. Load .forktrustconfig; if [summary] absent → Configured=false.
//  2. List commits in `aheadRef..HEAD` of the worktree.
//  3. Validate each commit against the contract.
//
// Returns a populated summaryCheckResult. A non-nil error only comes from
// configuration load / git log failure; rule failures populate Violations
// instead of returning an error.
func evalSummary(repoPath, wtPath, aheadRef string) (summaryCheckResult, error) {
	r := summaryCheckResult{}

	cfg, err := config.LoadRepoConfig(repoPath)
	if err != nil {
		return r, fmt.Errorf("load .forktrustconfig: %w", err)
	}
	if cfg == nil || cfg.Summary == nil {
		// No contract = treat as backwards-compat allow.
		return r, nil
	}
	r.Configured = true

	commits, err := summary.LoadCommits(wtPath, aheadRef, "HEAD")
	if err != nil {
		return r, fmt.Errorf("load commits: %w", err)
	}
	r.Commits = len(commits)
	r.Ran = true

	vs, err := summary.Check(commits, cfg.Summary)
	if err != nil {
		return r, fmt.Errorf("check summary: %w", err)
	}
	r.Violations = vs
	r.ViolationCount = len(vs)
	r.Passed = r.ViolationCount == 0
	return r, nil
}

// truncateViolations caps the violations slice for JSON output. Total count
// is kept in summaryCheckResult.ViolationCount so callers can detect truncation.
func truncateViolations(v []summary.Violation, n int) []summary.Violation {
	if len(v) <= n {
		return v
	}
	out := make([]summary.Violation, n)
	copy(out, v[:n])
	return out
}
