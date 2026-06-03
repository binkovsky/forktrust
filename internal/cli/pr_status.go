package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
)

var (
	prStatusProject string
	prStatusJSON    bool
)

var prStatusCmd = &cobra.Command{
	Use:   "pr-status <slug>",
	Short: "Show the GitHub PR status for a worktree (CI / approvals / mergeable)",
	Long: `Show the open PR for the worktree's branch with:

  - PR number, URL, state (OPEN | CLOSED | MERGED), draft flag
  - mergeable status (MERGEABLE | CONFLICTING | UNKNOWN)
  - review decision (APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | empty)
  - CI checks summary (overall + per-conclusion counts)
  - title, base branch, author, additions / deletions / changed files
  - last update time

Useful for "is it safe to merge yet?" checks from CI or AI agents.

Exit codes:
  0  PR found and reported (regardless of state)
  6  no worktree matching slug
  7  slug matches worktrees in multiple projects
  9  no origin remote configured
  17 gh CLI not available

Note: exits 0 even when there is no PR for the branch (PRExists=false in
JSON). That is not an error condition — it just means no PR has been
opened yet. Use JSON's pr_exists field to switch on this.`,
	Args: cobra.ExactArgs(1),
	RunE: runPRStatus,
}

func init() {
	prStatusCmd.Flags().StringVarP(&prStatusProject, "project", "p", "", "target project name (required if more than one is registered)")
	prStatusCmd.Flags().BoolVar(&prStatusJSON, "json", false, "emit a structured JSON result on stdout")
}

func runPRStatus(_ *cobra.Command, args []string) error {
	slug := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	proj, wtPath, err := resolveWorktree(cfg, prStatusProject, slug)
	if err != nil {
		return err
	}
	branch, err := git.CurrentBranch(wtPath)
	if err != nil {
		return err
	}
	if branch == "" {
		return fmt.Errorf("worktree at %s is detached", wtPath)
	}

	r := prStatusResult{
		Project: proj.Name,
		Slug:    slug,
		Branch:  branch,
	}

	if !git.HasOrigin(proj.Path) {
		return coded(ExitNoOriginRemote, fmt.Errorf("no origin remote configured; pr-status needs origin to call gh"))
	}

	if err := ghAvailable(); err != nil {
		r.GhAvailable = false
		_ = emitPRStatus(r)
		return coded(ExitGhNotAvailable, err)
	}
	r.GhAvailable = true

	info, err := ghPRView(wtPath, branch)
	if err != nil {
		return fmt.Errorf("gh pr view: %w", err)
	}
	if info == nil {
		r.PRExists = false
		if !prStatusJSON {
			fmt.Fprintf(os.Stderr, "No PR open for branch %s. Run `forktrust pr %s` to create one.\n", branch, slug)
		}
		return emitPRStatus(r)
	}

	r.PRExists = true
	r.PRNumber = info.Number
	r.PRURL = info.URL
	r.PRState = info.State
	r.PRIsDraft = info.IsDraft
	r.Mergeable = info.Mergeable
	r.ReviewDecision = info.ReviewDecision
	r.Checks = info.summarize()
	r.Title = info.Title
	r.BaseBranch = info.BaseRefName
	r.Author = info.Author.Login
	r.Additions = info.Additions
	r.Deletions = info.Deletions
	r.ChangedFiles = info.ChangedFiles
	r.UpdatedAt = info.UpdatedAt

	if !prStatusJSON {
		fmt.Printf("PR #%d  %s\n", r.PRNumber, r.PRURL)
		fmt.Printf("  title:        %s\n", r.Title)
		fmt.Printf("  state:        %s%s\n", r.PRState, draftSuffix(r.PRIsDraft))
		fmt.Printf("  base:         %s\n", r.BaseBranch)
		fmt.Printf("  author:       @%s\n", r.Author)
		fmt.Printf("  changes:      +%d / -%d across %d file(s)\n", r.Additions, r.Deletions, r.ChangedFiles)
		fmt.Printf("  mergeable:    %s\n", emptyDefault(r.Mergeable, "UNKNOWN"))
		fmt.Printf("  review:       %s\n", emptyDefault(r.ReviewDecision, "REVIEW_NOT_REQUIRED"))
		fmt.Printf("  checks:       %s (passing=%d failing=%d pending=%d / total=%d)\n",
			r.Checks.Overall, r.Checks.Passing, r.Checks.Failing, r.Checks.Pending, r.Checks.Total)
		fmt.Printf("  updated:      %s\n", r.UpdatedAt)
	}
	return emitPRStatus(r)
}

func draftSuffix(draft bool) string {
	if draft {
		return " (draft)"
	}
	return ""
}

func emptyDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
