package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
	"github.com/binkovsky/forktrust/internal/summary"
)

var (
	summaryProject string
	summaryCheck   bool
	summaryShow    bool
	summaryJSON    bool
)

var summaryCmd = &cobra.Command{
	Use:   "summary <slug>",
	Short: "Show or check the [summary] commit-message contract for a worktree",
	Long: `Inspect or evaluate the per-repo [summary] commit-message contract declared in
.forktrustconfig. The contract is enforced automatically by ` + "`forktrust finish`" + ` and
` + "`forktrust pr`" + ` (exit 19 on violation); this command lets you preview the state.

Modes (default is --show):
  forktrust summary <slug>           print the contract from .forktrustconfig
  forktrust summary <slug> --show    same as above (explicit)
  forktrust summary <slug> --check   evaluate the worktree's commits against the contract;
                                       exit 19 if any commit violates a rule

Exit codes:
  0   success (show / passing check)
  6   no worktree matching slug
  7   slug matches worktrees in multiple projects
  12  --check could not resolve a main reference to scope the commit range
  19  --check found one or more commits violating the contract`,
	Args: cobra.ExactArgs(1),
	RunE: runSummary,
}

func init() {
	summaryCmd.Flags().StringVarP(&summaryProject, "project", "p", "", "target project name (required if more than one is registered)")
	summaryCmd.Flags().BoolVar(&summaryShow, "show", false, "print the declared [summary] contract (default)")
	summaryCmd.Flags().BoolVar(&summaryCheck, "check", false, "evaluate the worktree's commits against the contract; exit 19 on violation")
	summaryCmd.Flags().BoolVar(&summaryJSON, "json", false, "emit a structured JSON object on stdout")
}

// summaryShowResult is the stable JSON shape for `forktrust summary [--json]`.
type summaryShowResult struct {
	Project              string              `json:"project"`
	Slug                 string              `json:"slug"`
	Configured           bool                `json:"configured"`
	Required             bool                `json:"required,omitempty"`
	MinBodyLength        int                 `json:"min_body_length,omitempty"`
	MaxBodyLength        int                 `json:"max_body_length,omitempty"`
	RequireSubjectPrefix []string            `json:"require_subject_prefix,omitempty"`
	RequireTicketPattern string              `json:"require_ticket_pattern,omitempty"`
	ForbiddenPatterns    []string            `json:"forbidden_patterns,omitempty"`
	Checked              bool                `json:"checked"`
	Passed               bool                `json:"passed"`
	Commits              int                 `json:"commits,omitempty"`
	Violations           []summary.Violation `json:"violations,omitempty"`
	ViolationCount       int                 `json:"violation_count,omitempty"`
	Action               string              `json:"action,omitempty"` // "show" | "check"
}

func runSummary(_ *cobra.Command, args []string) error {
	slug := args[0]
	if summaryShow && summaryCheck {
		return fmt.Errorf("--show and --check are mutually exclusive")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	proj, wtPath, err := resolveWorktree(cfg, summaryProject, slug)
	if err != nil {
		return err
	}
	repoCfg, err := config.LoadRepoConfig(proj.Path)
	if err != nil {
		return err
	}
	r := summaryShowResult{Project: proj.Name, Slug: slug}
	if repoCfg != nil && repoCfg.Summary != nil {
		s := repoCfg.Summary
		r.Configured = true
		r.Required = s.Required
		r.MinBodyLength = s.MinBodyLength
		r.MaxBodyLength = s.MaxBodyLength
		r.RequireSubjectPrefix = s.RequireSubjectPrefix
		r.RequireTicketPattern = s.RequireTicketPattern
		r.ForbiddenPatterns = s.ForbiddenPatterns
	}

	if summaryCheck {
		r.Action = "check"
		mainBranch := proj.MainBranch
		if mainBranch == "" {
			mainBranch = "main"
		}
		aheadRef := ""
		switch {
		case git.HasOrigin(proj.Path) && git.HasRemoteBranch(proj.Path, "origin", mainBranch):
			aheadRef = "origin/" + mainBranch
		case git.HasBranch(proj.Path, mainBranch):
			aheadRef = mainBranch
		}
		if aheadRef == "" {
			return coded(ExitAheadUnknown, fmt.Errorf("no main reference resolved; cannot list commits to check"))
		}
		sumR, err := evalSummary(proj.Path, wtPath, aheadRef)
		if err != nil {
			return err
		}
		r.Checked = sumR.Ran
		r.Passed = sumR.Passed
		r.Commits = sumR.Commits
		r.ViolationCount = sumR.ViolationCount
		r.Violations = truncateViolations(sumR.Violations, 100)
		if !sumR.Configured {
			return emitSummary(r, "no [summary] section in .forktrustconfig; nothing to check")
		}
		if !sumR.Passed {
			_ = emitSummary(r, "")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: %d violation(s) across %d commit(s):\n", sumR.ViolationCount, sumR.Commits)
			for _, v := range r.Violations {
				sha := v.CommitSHA
				if len(sha) > 7 {
					sha = sha[:7]
				}
				fmt.Fprintf(os.Stderr, "  %s  [%s]  %s\n", sha, v.Rule, v.Reason)
				if v.Subject != "" {
					fmt.Fprintf(os.Stderr, "    subject: %s\n", v.Subject)
				}
			}
			if sumR.ViolationCount > len(r.Violations) {
				fmt.Fprintf(os.Stderr, "  ... and %d more\n", sumR.ViolationCount-len(r.Violations))
			}
			return coded(ExitSummaryViolation, fmt.Errorf("%d commit-message violation(s)", sumR.ViolationCount))
		}
		return emitSummary(r, "[summary] contract satisfied")
	}

	// show
	r.Action = "show"
	if !r.Configured {
		return emitSummary(r, "no [summary] section in .forktrustconfig (worktree has no commit-message contract)")
	}
	return emitSummary(r, "")
}

func emitSummary(r summaryShowResult, headline string) error {
	if summaryJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	if headline != "" {
		fmt.Println(headline)
	}
	fmt.Printf("project: %s\n", r.Project)
	fmt.Printf("slug:    %s\n", r.Slug)
	if !r.Configured {
		return nil
	}
	fmt.Println("contract:")
	if r.Required {
		fmt.Println("  required: true")
	}
	if r.MinBodyLength > 0 {
		fmt.Printf("  min_body_length: %d\n", r.MinBodyLength)
	}
	if r.MaxBodyLength > 0 {
		fmt.Printf("  max_body_length: %d\n", r.MaxBodyLength)
	}
	if len(r.RequireSubjectPrefix) > 0 {
		fmt.Printf("  require_subject_prefix: %v\n", r.RequireSubjectPrefix)
	}
	if r.RequireTicketPattern != "" {
		fmt.Printf("  require_ticket_pattern: %s\n", r.RequireTicketPattern)
	}
	if len(r.ForbiddenPatterns) > 0 {
		fmt.Printf("  forbidden_patterns: %v\n", r.ForbiddenPatterns)
	}
	if r.Checked {
		if r.Passed {
			fmt.Printf("check:   PASS — %d commit(s) within contract\n", r.Commits)
		} else {
			fmt.Printf("check:   FAIL — %d violation(s) across %d commit(s)\n", r.ViolationCount, r.Commits)
		}
	}
	return nil
}
