package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ghCheckRun is one CI check inside StatusCheckRollup.
type ghCheckRun struct {
	Name         string `json:"name"`
	Status       string `json:"status"`     // QUEUED | IN_PROGRESS | COMPLETED | PENDING | etc.
	Conclusion   string `json:"conclusion"` // SUCCESS | FAILURE | NEUTRAL | CANCELLED | SKIPPED | TIMED_OUT | ACTION_REQUIRED | STALE | STARTUP_FAILURE | NONE
	WorkflowName string `json:"workflowName"`
	URL          string `json:"detailsUrl,omitempty"`
}

// ghPRInfo is the subset of the GitHub PR JSON returned by `gh pr view --json`.
// We intentionally project only the fields we surface in forktrust JSON so a
// future GH schema change in unused fields cannot break parsing.
type ghPRInfo struct {
	Number            int          `json:"number"`
	URL               string       `json:"url"`
	State             string       `json:"state"` // OPEN | CLOSED | MERGED
	IsDraft           bool         `json:"isDraft"`
	Mergeable         string       `json:"mergeable"`      // MERGEABLE | CONFLICTING | UNKNOWN
	ReviewDecision    string       `json:"reviewDecision"` // APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | (empty if no required reviews)
	StatusCheckRollup []ghCheckRun `json:"statusCheckRollup"`
	Title             string       `json:"title"`
	Body              string       `json:"body"`
	Additions         int          `json:"additions"`
	Deletions         int          `json:"deletions"`
	ChangedFiles      int          `json:"changedFiles"`
	BaseRefName       string       `json:"baseRefName"`
	HeadRefName       string       `json:"headRefName"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
	UpdatedAt string `json:"updatedAt"`
}

// ChecksSummary collapses the list of checks into a single state +
// per-conclusion counts. Used for both human + JSON output.
type checksSummary struct {
	Overall string `json:"overall"` // SUCCESS | PENDING | FAILURE | NONE
	Total   int    `json:"total"`
	Passing int    `json:"passing"`
	Failing int    `json:"failing"`
	Pending int    `json:"pending"`
}

// summarize returns the rollup summary of all checks attached to this PR.
// Rules:
//   - Overall=NONE   when no checks attached
//   - Overall=FAILURE when at least one check ended in FAILURE/CANCELLED/TIMED_OUT/ACTION_REQUIRED/STARTUP_FAILURE
//   - Overall=PENDING when at least one check is QUEUED/IN_PROGRESS/PENDING and no failures
//   - Overall=SUCCESS when every check ended in SUCCESS/NEUTRAL/SKIPPED
func (p *ghPRInfo) summarize() checksSummary {
	s := checksSummary{Overall: "NONE", Total: len(p.StatusCheckRollup)}
	if s.Total == 0 {
		return s
	}
	failed := false
	pending := false
	for _, c := range p.StatusCheckRollup {
		concl := strings.ToUpper(c.Conclusion)
		status := strings.ToUpper(c.Status)
		switch concl {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			s.Passing++
		case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
			s.Failing++
			failed = true
		default:
			// COMPLETED with unknown conclusion → don't count toward pass/fail
			if status == "QUEUED" || status == "IN_PROGRESS" || status == "PENDING" || concl == "" {
				s.Pending++
				pending = true
			}
		}
	}
	switch {
	case failed:
		s.Overall = "FAILURE"
	case pending:
		s.Overall = "PENDING"
	default:
		s.Overall = "SUCCESS"
	}
	return s
}

// ghAvailable returns nil if `gh` is installed AND authenticated.
// Two distinct failure modes get the same exit code (17) but different messages.
func ghAvailable() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found in PATH (install: https://cli.github.com or `brew install gh`)")
	}
	// `gh auth status` exits non-zero when not logged in.
	out, err := exec.Command("gh", "auth", "status").CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh not authenticated; run `gh auth login` first.\nDetails: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// ghPRView returns the PR associated with the given branch in this worktree,
// or (nil, nil) if there is no PR for that branch. Non-nil errors are real
// failures (network, parse, auth).
//
// We pass the branch as the positional argument so the lookup is precise
// even when multiple PRs exist in the repo.
func ghPRView(wtPath, branch string) (*ghPRInfo, error) {
	fields := "number,url,state,isDraft,mergeable,reviewDecision,statusCheckRollup,title,body,additions,deletions,changedFiles,baseRefName,headRefName,author,updatedAt"
	cmd := exec.Command("gh", "pr", "view", branch, "--json", fields) //nolint:gosec
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr := strings.ToLower(string(ee.Stderr))
			// gh prints "no pull requests found for branch <name>" when there's no PR.
			if strings.Contains(stderr, "no pull requests") || strings.Contains(stderr, "no prs") || strings.Contains(stderr, "not found") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("gh pr view: %w", err)
	}
	var info ghPRInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse gh pr view JSON: %w", err)
	}
	return &info, nil
}

// ghPRCreate creates a new PR with the given metadata. Returns the URL.
// Stderr from `gh` is piped through to the user's stderr so the (sometimes
// interactive) progress is visible.
func ghPRCreate(wtPath, base, head, title, body string, draft bool) (string, error) {
	args := []string{"pr", "create", "--base", base, "--head", head, "--title", title, "--body", body}
	if draft {
		args = append(args, "--draft")
	}
	cmd := exec.Command("gh", args...) //nolint:gosec
	cmd.Dir = wtPath
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %w", err)
	}
	// gh prints the PR URL on stdout, possibly preceded by progress text.
	// Take the last URL-looking line.
	url := strings.TrimSpace(string(out))
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			url = line
		}
	}
	return url, nil
}
