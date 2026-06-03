package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
)

var (
	prProject  string
	prDryRun   bool
	prJSON     bool
	prNoVerify  bool
	prNoScope   bool
	prNoSummary bool
	prTitle    string
	prBody     string
	prBase     string
	prDraft    bool
)

var prCmd = &cobra.Command{
	Use:   "pr <slug>",
	Short: "Push the worktree branch and open a GitHub PR (alternative to direct finish)",
	Long: `Open a GitHub PR for the worktree's branch instead of doing a direct local
merge into main. Use this when your workflow goes through code review:

  forktrust new fix-payment --scope "internal/payment/**"
  # ... edit, run tests ...
  forktrust pr fix-payment        # opens PR
  # ... reviewers approve, CI passes, you merge via GitHub UI ...
  forktrust rm fix-payment        # cleanup (ahead==0 fast-path)

The worktree stays alive after pr — only the branch is pushed. Use
forktrust finish if you want to merge locally instead.

Pre-flight (same safety pipeline as finish, minus the main-checkout
checks since we are not merging locally):

  1. aheadRef resolves (else exit 12)
  2. [verify] gate (unless --no-verify) (else exit 15)
  3. scope contract (unless --no-scope) (else exit 16)
  4. auto-WIP commit if dirty
  5. git push -u origin fork/<slug>
  6. gh pr create (or gh pr view if a PR already exists for this branch)

Title and body are auto-generated from the branch's commit messages
unless you pass --title and --body explicitly.

Exit codes:
  0   success (PR opened, or already existed; branch pushed)
  4   git push failed (auth / non-ff / network)
  6   no worktree matching slug
  7   slug matches worktrees in multiple projects
  9   no origin remote configured
  12  could not determine ahead count
  15  verify gate failed
  16  scope contract violated
  17  gh CLI not available (install gh, or run gh auth login)
  18  gh pr create returned non-zero`,
	Args: cobra.ExactArgs(1),
	RunE: runPR,
}

func init() {
	prCmd.Flags().StringVarP(&prProject, "project", "p", "", "target project name (required if more than one is registered)")
	prCmd.Flags().BoolVar(&prDryRun, "dry-run", false, "print the plan + run pre-flight checks without pushing or creating the PR")
	prCmd.Flags().BoolVar(&prJSON, "json", false, "emit a structured JSON result on stdout")
	prCmd.Flags().BoolVar(&prNoVerify, "no-verify", false, "skip the [verify] gate (prints a warning)")
	prCmd.Flags().BoolVar(&prNoScope, "no-scope", false, "skip the scope contract check (prints a warning)")
	prCmd.Flags().BoolVar(&prNoSummary, "no-summary", false, "skip the [summary] commit-message contract check (prints a warning)")
	prCmd.Flags().StringVar(&prTitle, "title", "", "PR title (default: first commit subject from the branch)")
	prCmd.Flags().StringVar(&prBody, "body", "", "PR body (default: bullet list of commit subjects + footer)")
	prCmd.Flags().StringVar(&prBase, "base", "", "base branch for the PR (default: project's mainBranch, typically main)")
	prCmd.Flags().BoolVar(&prDraft, "draft", false, "open the PR as a draft")
}

func runPR(_ *cobra.Command, args []string) error {
	slug := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	proj, wtPath, err := resolveWorktree(cfg, prProject, slug)
	if err != nil {
		return err
	}
	branch, err := git.CurrentBranch(wtPath)
	if err != nil {
		return err
	}
	if branch == "" {
		return fmt.Errorf("worktree at %s is detached, pr needs a branch", wtPath)
	}

	mainBranch := proj.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}
	baseBranch := prBase
	if baseBranch == "" {
		baseBranch = mainBranch
	}

	r := prResult{
		Project:      proj.Name,
		Slug:         slug,
		WorktreePath: wtPath,
		Branch:       branch,
		BaseBranch:   baseBranch,
		DryRun:       prDryRun,
	}

	dirty, err := git.DirtyCount(wtPath)
	if err != nil {
		return err
	}
	r.UncommittedFiles = dirty

	// We need origin to push the branch and to even ask gh.
	hasOrigin := git.HasOrigin(proj.Path)
	r.HasOrigin = hasOrigin
	if !hasOrigin {
		return coded(ExitNoOriginRemote, fmt.Errorf("no origin remote configured; pr needs origin to push the branch and call gh"))
	}

	// gh availability — fail fast so we don't push to a dead end.
	ghErr := ghAvailable()
	r.GhAvailable = ghErr == nil
	if ghErr != nil {
		return coded(ExitGhNotAvailable, ghErr)
	}

	// Make sure we have the latest origin refs for the aheadRef cascade.
	if _, ferr := git.Run(proj.Path, "fetch", "-q", "origin", mainBranch); ferr != nil && !prJSON {
		fmt.Fprintf(os.Stderr, "WARN: `git fetch origin %s` failed (%v); proceeding against possibly stale ref.\n", mainBranch, ferr)
	}

	// aheadRef cascade — same logic as finish.
	aheadRef := ""
	switch {
	case git.HasRemoteBranch(proj.Path, "origin", mainBranch):
		aheadRef = "origin/" + mainBranch
	case git.HasBranch(proj.Path, mainBranch):
		aheadRef = mainBranch
	default:
		return coded(ExitAheadUnknown, fmt.Errorf("no main reference resolved (tried origin/%s, %s)", mainBranch, mainBranch))
	}

	// ahead==0 guard: when the worktree has no uncommitted work AND no commits
	// ahead of base, there is nothing to open a PR for. Without this guard pr
	// would (a) skip auto-WIP (dirty==0), (b) push an empty branch update,
	// (c) call `gh pr create` which fails with a cryptic "no commits between
	// <base> and <head>" — exit 18 with no actionable message.
	// (When dirty > 0, the auto-WIP commit at line ~225 will make ahead > 0,
	// so this check only fires for a genuinely empty worktree.)
	if dirty == 0 {
		ahead, aheadErr := git.CommitsAhead(wtPath, aheadRef)
		if aheadErr == nil && ahead == 0 {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: worktree %q has 0 uncommitted changes and 0 commits ahead of %s.\n", slug, aheadRef)
			fmt.Fprintln(os.Stderr, "There is nothing to open a PR for. Either make changes, or use `forktrust rm "+slug+"` to clean up.")
			_ = emitPR(r)
			return coded(ExitAheadUnknown, fmt.Errorf("nothing to PR: 0 ahead, 0 dirty"))
		}
	}

	// Verify gate.
	repoCfg, _ := config.LoadRepoConfig(proj.Path)
	r.VerifyConfigured = repoCfg != nil && repoCfg.Verify != nil
	if prNoVerify {
		r.NoVerify = true
		if r.VerifyConfigured && !prDryRun {
			fmt.Fprintln(os.Stderr, "WARNING: --no-verify skipped the [verify] gate.")
		}
	} else if r.VerifyConfigured && !prDryRun {
		vr := runVerify(prJSON, wtPath, repoCfg.Verify)
		r.VerifyRan = true
		r.VerifyRanCommands = vr.RanCommands
		r.VerifyPassed = vr.Passed
		if !vr.Passed {
			r.VerifyFailedCommand = vr.FailedCommand
			r.VerifyOutput = vr.Output
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: [verify] gate failed before opening PR.\n")
			fmt.Fprintf(os.Stderr, "Failed command: %s\nReason: %s\n", vr.FailedCommand, vr.FailureReason)
			_ = emitPR(r)
			return coded(ExitVerifyFailed, fmt.Errorf("verify failed: %s", vr.FailureReason))
		}
	} else if r.VerifyConfigured && prDryRun {
		r.VerifyRanCommands = repoCfg.Verify.Commands
	}

	// Scope gate.
	scopeR, scopeErr := evalScope(proj.Path, wtPath, slug, aheadRef)
	if scopeErr != nil {
		return scopeErr
	}
	r.ScopeConfigured = scopeR.Configured
	r.ScopeAllowed = scopeR.Allowed
	if prNoScope {
		r.NoScope = true
		if scopeR.Configured && !prDryRun {
			fmt.Fprintln(os.Stderr, "WARNING: --no-scope skipped the change-contract check.")
		}
	} else if scopeR.Configured {
		r.ScopeChecked = true
		r.ScopePassed = scopeR.Passed
		r.ScopeViolationCount = scopeR.ViolationCount
		r.ScopeViolations = truncateStrings(scopeR.Violations, 100)
		if !scopeR.Passed {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: scope gate failed — %d file(s) outside declared --scope:\n", scopeR.ViolationCount)
			for _, v := range r.ScopeViolations {
				fmt.Fprintf(os.Stderr, "  - %s\n", v)
			}
			_ = emitPR(r)
			return coded(ExitScopeViolation, fmt.Errorf("scope gate failed"))
		}
	}

	// Summary gate (same semantics as finish: refuse to auto-WIP under contract).
	sumR, sumErr := evalSummary(proj.Path, wtPath, aheadRef)
	if sumErr != nil {
		_ = emitPR(r)
		return sumErr
	}
	r.SummaryConfigured = sumR.Configured
	if prNoSummary {
		r.NoSummary = true
		if sumR.Configured && !prDryRun {
			fmt.Fprintln(os.Stderr, "WARNING: --no-summary skipped the [summary] commit-message contract.")
		}
	} else if sumR.Configured {
		if dirty > 0 {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: [summary] contract is declared and the worktree has %d uncommitted change(s).\n", dirty)
			fmt.Fprintln(os.Stderr, "Auto-WIP would not satisfy your commit-message rules. Commit your work yourself, then re-run `forktrust pr`.")
			_ = emitPR(r)
			return coded(ExitSummaryViolation, fmt.Errorf("summary gate: %d uncommitted change(s) cannot be auto-WIP'd under a [summary] contract", dirty))
		}
		r.SummaryChecked = true
		r.SummaryPassed = sumR.Passed
		r.SummaryCommits = sumR.Commits
		r.SummaryViolationCount = sumR.ViolationCount
		r.SummaryViolations = truncateViolations(sumR.Violations, 100)
		if !sumR.Passed {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: [summary] gate failed — %d violation(s) across %d commit(s):\n", sumR.ViolationCount, sumR.Commits)
			for _, v := range r.SummaryViolations {
				sha := v.CommitSHA
				if len(sha) > 7 {
					sha = sha[:7]
				}
				fmt.Fprintf(os.Stderr, "  %s  [%s]  %s\n", sha, v.Rule, v.Reason)
				if v.Subject != "" {
					fmt.Fprintf(os.Stderr, "    subject: %s\n", v.Subject)
				}
			}
			_ = emitPR(r)
			return coded(ExitSummaryViolation, fmt.Errorf("summary gate: %d violation(s)", sumR.ViolationCount))
		}
	}

	// Dry-run stops here with the plan.
	if prDryRun {
		r.WouldRefuse = ""
		switch {
		case !aheadKnownForPR(aheadRef):
			r.WouldRefuse = fmt.Sprintf("no main reference resolved (exit %d)", ExitAheadUnknown)
		case r.ScopeConfigured && !r.ScopePassed && !prNoScope:
			r.WouldRefuse = fmt.Sprintf("scope gate would fail (exit %d)", ExitScopeViolation)
		case r.SummaryConfigured && !prNoSummary && dirty > 0:
			r.WouldRefuse = fmt.Sprintf("[summary] contract + %d uncommitted file(s); auto-WIP blocked (exit %d)", dirty, ExitSummaryViolation)
		case r.SummaryConfigured && !r.SummaryPassed && !prNoSummary:
			r.WouldRefuse = fmt.Sprintf("[summary] gate would fail: %d violation(s) (exit %d)", r.SummaryViolationCount, ExitSummaryViolation)
		}
		return previewPR(r)
	}

	// Commit any WIP locally so the push includes it.
	if dirty > 0 {
		prNotef("%d uncommitted change(s), committing before push", dirty)
		if _, err := git.Run(wtPath, "add", "-A"); err != nil {
			return err
		}
		if err := gitStream(prJSON, wtPath, "commit", "-m", "WIP: "+slug); err != nil {
			return coded(ExitHookFailed, fmt.Errorf("commit failed: %w", err))
		}
		r.CommittedWIP = true
	}

	// Push the branch to origin. Use -u to set upstream tracking (so subsequent
	// `git pull/push` in the worktree don't need explicit refspecs).
	prNotef("pushing %s to origin", branch)
	if err := gitStream(prJSON, wtPath, "push", "-u", "origin", branch); err != nil {
		return coded(ExitPushFailed, fmt.Errorf("push failed: %w", err))
	}
	r.BranchPushed = true

	// If a PR already exists for this branch, just look it up.
	existing, err := ghPRView(wtPath, branch)
	if err != nil {
		_ = emitPR(r) // preserve --json envelope contract (branch IS pushed)
		return coded(ExitPRCreateFailed, err)
	}
	// `gh pr view <branch>` returns the most recent PR for the branch
	// regardless of state (OPEN/CLOSED/MERGED). Only OPEN PRs are "still
	// receiving updates" — for CLOSED/MERGED, the user has new work and
	// wants a fresh PR. Treating a closed PR as "already open" misleads
	// agents (PRExisted=true, PRCreated=false but the PR is dead).
	if existing != nil && strings.ToUpper(existing.State) == "OPEN" {
		r.PRExisted = true
		r.PRCreated = false
		r.PRNumber = existing.Number
		r.PRURL = existing.URL
		r.PRState = existing.State
		r.PRTitle = existing.Title
		r.PRIsDraft = existing.IsDraft
		prNotef("PR already open: #%d %s", existing.Number, existing.URL)
		printPRBlock(r)
		return emitPR(r)
	}
	if existing != nil {
		// State is CLOSED or MERGED — fall through to create a fresh PR.
		prNotef("previous PR #%d is %s; opening a new PR for the latest commits", existing.Number, existing.State)
	}

	// Generate title and body if not provided.
	title := prTitle
	body := prBody
	if title == "" || body == "" {
		autoTitle, autoBody := autoTitleBody(wtPath, aheadRef, slug)
		if title == "" {
			title = autoTitle
		}
		if body == "" {
			body = autoBody
		}
	}

	prNotef("creating PR: %s -> %s", branch, baseBranch)
	url, err := ghPRCreate(wtPath, baseBranch, branch, title, body, prDraft)
	if err != nil {
		_ = emitPR(r) // preserve --json envelope contract (branch IS pushed)
		return coded(ExitPRCreateFailed, err)
	}
	r.PRCreated = true
	r.PRURL = url

	// Re-fetch full PR info so the JSON has the number and state.
	if info, err := ghPRView(wtPath, branch); err == nil && info != nil {
		r.PRNumber = info.Number
		r.PRState = info.State
		r.PRTitle = info.Title
		r.PRIsDraft = info.IsDraft
	}
	printPRBlock(r)
	return emitPR(r)
}

// prNotef is notef's JSON-aware sibling: in --json mode, progress messages go
// to stderr so stdout stays a pure JSON document. Otherwise behaves like notef.
func prNotef(format string, args ...interface{}) {
	if prJSON {
		fmt.Fprintf(os.Stderr, "==> "+format+"\n", args...)
		return
	}
	notef(format, args...)
}

// aheadKnownForPR is a tiny wrapper so the would_refuse builder in dry-run
// reads like the runFinish equivalent without re-running the cascade.
func aheadKnownForPR(aheadRef string) bool { return aheadRef != "" }

// printPRBlock is the human-friendly stdout summary printed when --json is off.
// Kept separate so the JSON path stays pure (no decoration on stdout).
func printPRBlock(r prResult) {
	if prJSON {
		return
	}
	fmt.Println()
	if r.PRCreated {
		fmt.Printf("PR opened: %s\n", r.PRURL)
	} else if r.PRExisted {
		fmt.Printf("PR already open: %s\n", r.PRURL)
	}
	if r.PRNumber > 0 {
		fmt.Printf("  #%d  %s\n", r.PRNumber, r.PRTitle)
	}
	if r.PRIsDraft {
		fmt.Println("  (draft)")
	}
	fmt.Printf("Branch %s pushed to origin.\n", r.Branch)
	fmt.Println("Worktree stays alive; run `forktrust rm " + r.Slug + "` after the PR merges (or `forktrust finish " + r.Slug + "` to merge locally instead).")
}

func previewPR(r prResult) error {
	if prJSON {
		return emitPR(r)
	}
	fmt.Printf("DRY-RUN: pr %s\n", r.Slug)
	fmt.Printf("  project:    %s\n", r.Project)
	fmt.Printf("  worktree:   %s\n", r.WorktreePath)
	fmt.Printf("  branch:     %s -> %s\n", r.Branch, r.BaseBranch)
	fmt.Printf("  uncommitted:%d file(s)\n", r.UncommittedFiles)
	fmt.Printf("  gh:         %v\n", r.GhAvailable)
	if r.VerifyConfigured {
		fmt.Printf("  verify:     %d command(s) WILL RUN (dry-run does not execute them)\n", len(r.VerifyRanCommands))
	}
	if r.ScopeConfigured {
		fmt.Printf("  scope:      checked — passed=%v, violations=%d\n", r.ScopePassed, r.ScopeViolationCount)
	}
	fmt.Println()
	if r.WouldRefuse != "" {
		fmt.Printf("WOULD REFUSE: %s\n", r.WouldRefuse)
		return emitPR(r)
	}
	fmt.Println("Would:")
	step := 1
	if r.VerifyConfigured && !r.NoVerify {
		fmt.Printf("  %d. run [verify] (%d command(s); refuse on first non-zero)\n", step, len(r.VerifyRanCommands))
		step++
	}
	if r.UncommittedFiles > 0 {
		fmt.Printf("  %d. commit %d file(s) as %q\n", step, r.UncommittedFiles, "WIP: "+r.Slug)
		step++
	}
	fmt.Printf("  %d. git push -u origin %s\n", step, r.Branch)
	step++
	fmt.Printf("  %d. gh pr create --base %s --head %s\n", step, r.BaseBranch, r.Branch)
	return emitPR(r)
}

// autoTitleBody builds a default PR title and body from the branch's commits
// (the ones ahead of aheadRef). Title = first commit subject. Body = bullet
// list of all subjects + a footer pointing back to forktrust.
func autoTitleBody(wtPath, aheadRef, slug string) (string, string) {
	subjects, _ := git.Run(wtPath, "log", "--pretty=%s", aheadRef+"..HEAD")
	lines := []string{}
	for _, l := range strings.Split(strings.TrimSpace(subjects), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	// Pick the title: prefer the most recent meaningful commit subject. We
	// run after the auto-WIP commit (pr.go:218-224) which means lines[0] is
	// often literally "WIP: <slug>" — a terrible PR title. Skip leading WIP-
	// prefixed subjects so reviewers see the real work being shipped.
	title := "forktrust: " + slug
	for _, line := range lines {
		if !isWIPSubject(line, slug) {
			title = line
			break
		}
	}
	// If every subject is WIP-like, fall back to the most recent one rather
	// than the generic placeholder; at least it identifies the slug.
	if title == "forktrust: "+slug && len(lines) > 0 {
		title = lines[0]
	}

	var b strings.Builder
	if len(lines) == 0 {
		b.WriteString(fmt.Sprintf("Opened from forktrust worktree `%s`.\n", slug))
	} else {
		b.WriteString("## Commits\n\n")
		// log emits newest first; reverse for chronological reading.
		for i := len(lines) - 1; i >= 0; i-- {
			b.WriteString("- " + lines[i] + "\n")
		}
	}
	b.WriteString("\n---\nOpened with [forktrust](https://github.com/binkovsky/forktrust) `pr " + slug + "`.\n")
	return title, b.String()
}

// isWIPSubject reports whether a commit subject looks like an auto-generated
// WIP commit. Used by autoTitleBody to skip these when picking a PR title.
// Matches: "WIP: <slug>" (forktrust auto-WIP), "WIP <anything>", "wip <anything>",
// and the explicit "WIP snapshot before worktree removal" prefix used by `rm`.
func isWIPSubject(subject, slug string) bool {
	s := strings.TrimSpace(subject)
	if s == "" {
		return true
	}
	if s == "WIP: "+slug {
		return true
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "wip:") || strings.HasPrefix(lower, "wip ") {
		return true
	}
	if strings.HasPrefix(s, "WIP snapshot before worktree removal") {
		return true
	}
	return false
}
