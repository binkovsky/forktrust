package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
	"github.com/binkovsky/forktrust/internal/ports"
	"github.com/binkovsky/forktrust/internal/scope"
)

var (
	finishMessage  string
	finishProject  string
	finishDryRun   bool
	finishJSON     bool
	finishNoVerify  bool
	finishNoScope   bool
	finishNoSummary bool
)

var finishCmd = &cobra.Command{
	Use:   "finish <slug>",
	Short: "Commit WIP, merge to main, push, remove worktree (refuses on conflict)",
	Long: `Canonical end-of-task command. Pipeline:

  1. commits any uncommitted WIP on the worktree branch
  2. fetches origin/main, fast-forward-pulls the main checkout
  3. merges --no-ff the worktree branch into main
  4. pushes main to origin
  5. removes the worktree and branch

Hard safety guarantees:

  * REFUSES on merge conflict. Never auto-resolves, never uses --strategy
    ours/theirs. Aborts the merge and tells you what to inspect.
  * REFUSES if the main checkout has uncommitted changes. Will not risk
    overwriting work that is not yours.

Exit codes:
  0   success
  2   merge conflict (refuse to auto-resolve)
  3   main worktree is dirty
  4   push to origin failed
  6   no worktree matching slug
  7   slug matches worktrees in multiple projects
  10  main checkout is on the wrong branch
  12  could not determine ahead count (no main reference resolved)
  13  worktree removed but git branch -D failed (branch lingers)
  14  worktree has ignored files that would be permanently deleted
  15  verify gate failed: a [verify].commands entry exited non-zero, or require_clean is set and the worktree is dirty after verify
  16  scope gate failed: the worktree diff touches files outside the declared --scope contract
  19  summary gate failed: one or more commits violate the [summary] contract in .forktrustconfig`,
	Args: cobra.ExactArgs(1),
	RunE: runFinish,
}

func init() {
	finishCmd.Flags().StringVarP(&finishMessage, "message", "m", "", "commit message for uncommitted WIP (default \"WIP: <slug>\")")
	finishCmd.Flags().StringVarP(&finishProject, "project", "p", "", "target project name (required if more than one is registered)")
	finishCmd.Flags().BoolVar(&finishDryRun, "dry-run", false, "print the plan without executing anything")
	finishCmd.Flags().BoolVar(&finishJSON, "json", false, "emit a structured JSON result on stdout (one object)")
	finishCmd.Flags().BoolVar(&finishNoVerify, "no-verify", false, "skip the [verify] gate (prints a warning); only use when you really know what you're doing")
	finishCmd.Flags().BoolVar(&finishNoScope, "no-scope", false, "skip the change-contract scope check (prints a warning); use when you have manually reviewed out-of-scope edits")
	finishCmd.Flags().BoolVar(&finishNoSummary, "no-summary", false, "skip the [summary] commit-message contract check (prints a warning)")
}

func runFinish(_ *cobra.Command, args []string) error {
	slug := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	proj, wtPath, err := resolveWorktree(cfg, finishProject, slug)
	if err != nil {
		return err
	}
	branch, err := git.CurrentBranch(wtPath)
	if err != nil {
		return err
	}
	if branch == "" {
		return fmt.Errorf("worktree at %s is detached, finish needs a branch", wtPath)
	}

	mainBranch := proj.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}

	r := finishResult{
		Project:      proj.Name,
		Slug:         slug,
		WorktreePath: wtPath,
		Branch:       branch,
		MainBranch:   mainBranch,
		DryRun:       finishDryRun,
	}

	dirty, err := git.DirtyCount(wtPath)
	if err != nil {
		return err
	}
	r.UncommittedFiles = dirty

	// Fetch origin/<main> so the aheadRef cascade and CommitsAhead reason
	// against the latest state. Soft-fail with a stderr warning so the user
	// knows we may be working off stale refs (e.g. offline, auth expired) —
	// silent failure here previously caused 'non-fast-forward' push failures
	// AFTER local merge had already landed. Skipped when no origin.
	if git.HasOrigin(proj.Path) {
		if _, ferr := git.Run(proj.Path, "fetch", "-q", "origin", mainBranch); ferr != nil && !finishJSON {
			fmt.Fprintf(os.Stderr, "WARN: `git fetch origin %s` failed (%v); proceeding against possibly stale ref. If push later fails non-fast-forward, restore connectivity and re-run finish.\n", mainBranch, ferr)
		}
	}

	if finishDryRun {
		return previewFinish(r, mainBranch, proj.Path)
	}

	notef("finish target: %s (branch %s in %s)", wtPath, branch, proj.Name)

	// 1. Resolve aheadRef FIRST — refuse early if no main ref resolves.
	// R5 fix: previously runFinish committed WIP and THEN refused at the
	// aheadRef check, leaving a phantom 'WIP: <slug>' commit that the
	// dry-run (which refused first, with no side effects) never warned
	// about. Refuse before any side effect so dry-run matches reality.
	hasOrigin := git.HasOrigin(proj.Path)
	aheadRef := ""
	switch {
	case hasOrigin && git.HasRemoteBranch(proj.Path, "origin", mainBranch):
		aheadRef = "origin/" + mainBranch
	case git.HasBranch(proj.Path, mainBranch):
		aheadRef = mainBranch
	default:
		return coded(ExitAheadUnknown, fmt.Errorf("no main reference resolved (tried origin/%s, %s); push or create %s first", mainBranch, mainBranch, mainBranch))
	}

	// PRE-FLIGHT: all pure refusals before any side effect (commit/merge/push).
	// Order matches the checks in previewFinish so dry-run always agrees.

	// 2a. Ignored files would be silently lost.
	if err := refuseIfIgnoredFiles(wtPath, slug); err != nil {
		return err
	}

	// 2b. Main checkout must be on mainBranch (pure read, no side effect).
	current, err := git.CurrentBranch(proj.Path)
	if err != nil {
		return err
	}
	if current != mainBranch {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "REFUSE: main checkout at %s is on branch %q, expected %q.\n", proj.Path, current, mainBranch)
		fmt.Fprintln(os.Stderr, "Merging now would land your work on the wrong branch and lie about success.")
		fmt.Fprintf(os.Stderr, "Switch first:\n  git -C %s checkout %s\n", proj.Path, mainBranch)
		fmt.Fprintln(os.Stderr, "Then re-run forktrust finish.")
		return coded(ExitMainOnWrongBranch, fmt.Errorf("main checkout on %q, expected %q", current, mainBranch))
	}

	// 2c. Main checkout must be clean (pure read, no side effect).
	mainDirty, err := git.DirtyCount(proj.Path)
	if err != nil {
		return err
	}
	if mainDirty > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "REFUSE: main working tree in %s has %d uncommitted change(s).\n", proj.Path, mainDirty)
		fmt.Fprintln(os.Stderr, "Cannot safely merge without risking overwrite. Commit/stash them first, then re-run.")
		fmt.Fprintf(os.Stderr, "Inspect: git -C %s status --short\n", proj.Path)
		return coded(ExitDirtyMain, fmt.Errorf("main worktree is dirty (%d files)", mainDirty))
	}

	// 2d. Verify gate. Last pre-flight check before any git mutation.
	// Skipped under --no-verify (with stderr warning) or when [verify] absent.
	repoCfg, _ := config.LoadRepoConfig(proj.Path) // validated at `new` time; ignore reparse error here, fall through to no-verify behavior
	r.VerifyConfigured = repoCfg != nil && repoCfg.Verify != nil
	if finishNoVerify {
		r.NoVerify = true
		if r.VerifyConfigured {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "WARNING: --no-verify SKIPPED the [verify] gate. The merge will land WITHOUT running:")
			for _, c := range repoCfg.Verify.Commands {
				fmt.Fprintf(os.Stderr, "  - %s\n", c)
			}
			fmt.Fprintln(os.Stderr, "Only use --no-verify when you have already verified manually.")
		}
	} else if r.VerifyConfigured {
		vr := runVerify(finishJSON, wtPath, repoCfg.Verify)
		r.VerifyRan = true
		r.VerifyRanCommands = vr.RanCommands
		r.VerifyPassed = vr.Passed
		if !vr.Passed {
			r.VerifyFailedCommand = vr.FailedCommand
			r.VerifyOutput = vr.Output
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: [verify] gate failed.\n")
			fmt.Fprintf(os.Stderr, "Failed command: %s\n", vr.FailedCommand)
			fmt.Fprintf(os.Stderr, "Reason: %s\n", vr.FailureReason)
			fmt.Fprintln(os.Stderr, "Fix the underlying problem and re-run `forktrust finish`, or pass --no-verify to bypass (not recommended).")
			// Emit JSON with the verify failure context BEFORE returning so
			// programmatic consumers can read verify_failed_command / verify_output.
			// (Same pattern as ExitBranchNotDeleted path: emit, then return coded.)
			_ = emitFinish(r)
			return coded(ExitVerifyFailed, fmt.Errorf("verify failed at command %q: %s", vr.FailedCommand, vr.FailureReason))
		}
	}

	// 2e. Scope (change contract) gate. Runs against the diff vs aheadRef.
	// Skipped under --no-scope (with stderr warning) or when no scope file exists.
	scopeR, scopeErr := evalScope(proj.Path, wtPath, slug, aheadRef)
	if scopeErr != nil {
		// Emit JSON envelope with partial state so --json consumers always
		// get a parseable document on stdout even when scope load fails
		// (e.g. malformed scope TOML). Same contract as verify-failure path.
		_ = emitFinish(r)
		return scopeErr
	}
	r.ScopeConfigured = scopeR.Configured
	r.ScopeAllowed = scopeR.Allowed
	if finishNoScope {
		r.NoScope = true
		if scopeR.Configured {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "WARNING: --no-scope SKIPPED the change-contract check. The merge will land even though the diff may touch files outside the declared scope:")
			for _, g := range scopeR.Allowed {
				fmt.Fprintf(os.Stderr, "  allowed: %s\n", g)
			}
			fmt.Fprintln(os.Stderr, "Only use --no-scope when you have already reviewed the diff manually.")
		}
	} else if scopeR.Configured {
		r.ScopeChecked = true
		r.ScopePassed = scopeR.Passed
		r.ScopeViolationCount = scopeR.ViolationCount
		r.ScopeViolations = truncateStrings(scopeR.Violations, 100)
		if !scopeR.Passed {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: scope gate failed — %d file(s) outside the declared --scope:\n", scopeR.ViolationCount)
			for _, v := range r.ScopeViolations {
				fmt.Fprintf(os.Stderr, "  - %s\n", v)
			}
			if scopeR.ViolationCount > len(r.ScopeViolations) {
				fmt.Fprintf(os.Stderr, "  ... and %d more (see JSON output for full list)\n", scopeR.ViolationCount-len(r.ScopeViolations))
			}
			fmt.Fprintln(os.Stderr, "Allowed globs:")
			for _, g := range scopeR.Allowed {
				fmt.Fprintf(os.Stderr, "  + %s\n", g)
			}
			fmt.Fprintln(os.Stderr, "Either widen the scope (forktrust scope " + slug + " --set \"...\"), revert the out-of-scope changes, or pass --no-scope to bypass (not recommended).")
			_ = emitFinish(r)
			return coded(ExitScopeViolation, fmt.Errorf("scope gate failed: %d file(s) outside declared contract", scopeR.ViolationCount))
		}
	}

	// 2f. Summary (commit-message contract) gate. Runs BEFORE auto-WIP so
	// users with a contract are forced to write real commit messages
	// themselves — an auto-WIP "WIP: <slug>" message would never satisfy
	// an arbitrary user-defined contract.
	sumR, sumErr := evalSummary(proj.Path, wtPath, aheadRef)
	if sumErr != nil {
		_ = emitFinish(r)
		return sumErr
	}
	r.SummaryConfigured = sumR.Configured
	if finishNoSummary {
		r.NoSummary = true
		if sumR.Configured {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "WARNING: --no-summary SKIPPED the [summary] commit-message contract. The merge will land even though commit messages may not satisfy it.")
			fmt.Fprintln(os.Stderr, "Only use --no-summary when you have already reviewed the commit messages manually.")
		}
	} else if sumR.Configured {
		// Refuse to auto-WIP under a contract.
		if dirty > 0 {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: [summary] contract is declared and the worktree has %d uncommitted change(s).\n", dirty)
			fmt.Fprintln(os.Stderr, "Auto-WIP would not satisfy your commit-message rules. Commit your work yourself, e.g.:")
			fmt.Fprintf(os.Stderr, "  git -C %s add -A && git -C %s commit -m \"<your message>\"\n", wtPath, wtPath)
			fmt.Fprintln(os.Stderr, "Or pass --no-summary to bypass (not recommended).")
			_ = emitFinish(r)
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
			if sumR.ViolationCount > len(r.SummaryViolations) {
				fmt.Fprintf(os.Stderr, "  ... and %d more (see JSON output for full list)\n", sumR.ViolationCount-len(r.SummaryViolations))
			}
			fmt.Fprintln(os.Stderr, "Either amend the offending commits (git rebase -i / git commit --amend) or pass --no-summary to bypass.")
			_ = emitFinish(r)
			return coded(ExitSummaryViolation, fmt.Errorf("summary gate: %d violation(s)", sumR.ViolationCount))
		}
	}

	// 2g. NOW it's safe to commit uncommitted WIP on the worktree branch.
	if dirty > 0 {
		msg := finishMessage
		if msg == "" {
			msg = "WIP: " + slug
		}
		notef("%d uncommitted change(s), committing to %s (%q)", dirty, branch, msg)
		if _, err := git.Run(wtPath, "add", "-A"); err != nil {
			return err
		}
		if err := gitStream(finishJSON, wtPath, "commit", "-m", msg); err != nil {
			return coded(ExitHookFailed, fmt.Errorf("commit failed (pre-commit hook?): %w", err))
		}
		r.CommittedWIP = true
	}
	ahead, err := git.CommitsAhead(wtPath, aheadRef)
	if err != nil {
		_ = emitFinish(r) // preserve --json envelope contract
		return err
	}
	r.CommitsAhead = ahead
	if ahead == 0 {
		notef("branch %s has no commits ahead of %s, nothing to merge", branch, aheadRef)
		if err := refuseIfIgnoredFiles(wtPath, slug); err != nil {
			return err
		}
		if err := removeWorktree(finishJSON, proj.Path, wtPath, false); err != nil {
			return err
		}
		r.WorktreeRemoved = true
		// Release port block BEFORE branch -D — same order as the post-merge
		// path — so a branch-delete failure doesn't block port cleanup.
		if storePath, perr := ports.DefaultPath(); perr == nil {
			_ = ports.Release(storePath, proj.Path, slug)
		}
		_ = scope.Remove(proj.Path, slug)
		if _, err := git.Run(proj.Path, "branch", "-D", branch); err == nil {
			r.BranchDeleted = true
		} else {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "WARN: worktree removed, but could not delete local branch %s: %v\n", branch, err)
			fmt.Fprintf(os.Stderr, "  The branch lingers. Investigate: git -C %s branch | grep %s\n", proj.Path, branch)
			r.BranchKept = true
			_ = emitFinish(r)
			return coded(ExitBranchNotDeleted, fmt.Errorf("branch -D %s failed: %w", branch, err))
		}
		return emitFinish(r)
	}
	notef("branch is %d commit(s) ahead of %s, merging", ahead, aheadRef)
	// (wrong-branch and dirty-main checks already ran in pre-flight above)

	// 3. Fast-forward main to origin/main (only if we have origin).
	if hasOrigin {
		if err := gitStream(finishJSON, proj.Path, "pull", "--ff-only", "origin", mainBranch); err != nil {
			return coded(ExitPushFailed, fmt.Errorf("pull --ff-only failed: %w", err))
		}
	}

	// 6. Merge the worktree branch. --no-ff keeps it visible in history.
	notef("merging %s into %s", branch, mainBranch)
	if err := gitStream(finishJSON, proj.Path, "merge", "--no-ff", "--no-edit", branch); err != nil {
		_, _ = git.Run(proj.Path, "merge", "--abort")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "REFUSE: merge of %s into %s produced conflicts. Aborted to leave main clean.\n", branch, mainBranch)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Inspect the conflict:\n  cd %s && git merge %s\n", proj.Path, branch)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Or abandon and snapshot to wip/*:\n  forktrust rm %s\n", slug)
		return coded(ExitMergeConflict, fmt.Errorf("merge conflict: refuse to auto-resolve"))
	}
	r.Merged = true

	// 7. Push main.
	if hasOrigin {
		notef("pushing %s to origin", mainBranch)
		if err := gitStream(finishJSON, proj.Path, "push", "origin", mainBranch); err != nil {
			return coded(ExitPushFailed, fmt.Errorf("push failed (auth? non-fast-forward?): %w", err))
		}
		r.Pushed = true
	} else {
		notef("no origin remote, %s is up-to-date locally only", mainBranch)
	}

	// 7. Remove the worktree + branch.
	// (ignored-files guard ran before merge at step 2a; no need to repeat here)
	if err := removeWorktree(finishJSON, proj.Path, wtPath, false); err != nil {
		return err
	}
	r.WorktreeRemoved = true

	// 8. Release any port block this slug owned (no-op if none). Done BEFORE
	// branch -D so a branch-delete failure still leaves ports released.
	if storePath, perr := ports.DefaultPath(); perr == nil {
		_ = ports.Release(storePath, proj.Path, slug)
	}
	// Clean up the scope file (no-op if none) so a future `forktrust new`
	// with the same slug starts from a clean state.
	_ = scope.Remove(proj.Path, slug)

	// 9. Delete the local branch. R5 fix: surface failures with exit 13
	// (same shape as rm) — merge/push/remove already succeeded, but a
	// silent swallow leaves a stale branch the user thinks is gone.
	if _, err := git.Run(proj.Path, "branch", "-D", branch); err == nil {
		notef("deleted local branch %s", branch)
		r.BranchDeleted = true
	} else {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "WARN: merge/push/remove succeeded, but could not delete local branch %s: %v\n", branch, err)
		fmt.Fprintf(os.Stderr, "  The branch lingers. Investigate: git -C %s branch | grep %s\n", proj.Path, branch)
		r.BranchKept = true
		_ = emitFinish(r)
		return coded(ExitBranchNotDeleted, fmt.Errorf("branch -D %s failed: %w", branch, err))
	}

	notef("finish done")
	return emitFinish(r)
}

func previewFinish(r finishResult, mainBranch, mainPath string) error {
	// Mirror actual finish logic so the preview never lies about ahead-count,
	// no-origin behavior, or wrong-branch refusal. R4 fix: use the same
	// HasRemoteBranch+HasBranch cascade as runFinish — previous code used
	// bare "origin/<main>" which silently reported 0 ahead on fresh clones
	// where origin/main hasn't been fetched, then real finish refused with
	// exit 12. Dry-run must match real behavior.
	hasOrigin := git.HasOrigin(mainPath)
	r.HasOrigin = hasOrigin
	var aheadRef string
	switch {
	case hasOrigin && git.HasRemoteBranch(mainPath, "origin", mainBranch):
		aheadRef = "origin/" + mainBranch
	case git.HasBranch(mainPath, mainBranch):
		aheadRef = mainBranch
	}
	ahead := 0
	aheadKnown := aheadRef != ""
	if aheadKnown {
		ahead, _ = git.CommitsAhead(r.WorktreePath, aheadRef)
	}
	r.CommitsAhead = ahead
	mainDirty, _ := git.DirtyCount(mainPath)
	r.MainDirty = mainDirty
	current, _ := git.CurrentBranch(mainPath)
	r.MainCurrentBranch = current

	// Mirror the early ignored-files guard from runFinish (step 2a).
	// previewFinish must surface every refusal that runFinish would hit.
	ignoredN, _ := git.IgnoredCount(r.WorktreePath)

	// Record verify config in the dry-run result so consumers know whether the
	// real command would run a verify gate. We do NOT execute verify commands
	// in dry-run — that would be a side effect (test runners create files,
	// write to network, etc.). The would_refuse field cannot predict verify
	// failure; callers that need to know must run verify themselves or run
	// the real `forktrust finish`.
	repoCfg, _ := config.LoadRepoConfig(mainPath)
	if repoCfg != nil && repoCfg.Verify != nil {
		r.VerifyConfigured = true
		r.VerifyRanCommands = repoCfg.Verify.Commands
	}
	if finishNoVerify {
		r.NoVerify = true
	}

	// Scope eval in dry-run: this is a pure read (git diff + glob match),
	// so we DO execute it. Predicts exit 16 accurately.
	if aheadKnown && !finishNoScope {
		scopeR, _ := evalScope(mainPath, r.WorktreePath, r.Slug, aheadRef)
		r.ScopeConfigured = scopeR.Configured
		r.ScopeAllowed = scopeR.Allowed
		if scopeR.Configured {
			r.ScopeChecked = true
			r.ScopePassed = scopeR.Passed
			r.ScopeViolationCount = scopeR.ViolationCount
			r.ScopeViolations = truncateStrings(scopeR.Violations, 100)
		}
	} else if finishNoScope {
		r.NoScope = true
	}

	// Summary eval in dry-run: pure read (git log + rule check), so we DO
	// execute it. Predicts exit 19 accurately, INCLUDING the "auto-WIP
	// blocked under a [summary] contract" mode (dirty + Configured).
	if aheadKnown && !finishNoSummary {
		sumR, _ := evalSummary(mainPath, r.WorktreePath, aheadRef)
		r.SummaryConfigured = sumR.Configured
		if sumR.Configured {
			r.SummaryChecked = true
			r.SummaryPassed = sumR.Passed
			r.SummaryCommits = sumR.Commits
			r.SummaryViolationCount = sumR.ViolationCount
			r.SummaryViolations = truncateViolations(sumR.Violations, 100)
		}
	} else if finishNoSummary {
		r.NoSummary = true
	}

	// Mirror runFinish refusal order exactly — dry-run must agree with real finish.
	// Real finish order: (1) no main ref → exit 12; (2) ignored files → exit 14;
	// (3) wrong branch → exit 10; (4) dirty main → exit 3;
	// (5) verify → exit 15 (NOT predicted; verify has side effects);
	// (6) scope → exit 16 (predicted; pure diff + glob match).
	switch {
	case !aheadKnown:
		r.WouldRefuse = fmt.Sprintf("no main reference resolved (exit %d). Push origin/%s or create local %s first.", ExitAheadUnknown, mainBranch, mainBranch)
	case ignoredN > 0:
		r.WouldRefuse = fmt.Sprintf("worktree has %d ignored file(s) that would be permanently deleted (exit %d). Move them out or use `forktrust rm --force`.", ignoredN, ExitIgnoredFiles)
	case current != mainBranch:
		r.WouldRefuse = fmt.Sprintf("main checkout on %q, expected %q (exit %d)", current, mainBranch, ExitMainOnWrongBranch)
	case mainDirty > 0:
		r.WouldRefuse = fmt.Sprintf("main checkout is dirty (%d uncommitted file(s)) (exit %d)", mainDirty, ExitDirtyMain)
	case r.ScopeConfigured && !r.ScopePassed && !finishNoScope:
		r.WouldRefuse = fmt.Sprintf("scope gate failed: %d file(s) outside declared --scope (exit %d). Widen scope, revert out-of-scope edits, or pass --no-scope to bypass.", r.ScopeViolationCount, ExitScopeViolation)
	case r.SummaryConfigured && !finishNoSummary && r.UncommittedFiles > 0:
		r.WouldRefuse = fmt.Sprintf("[summary] contract declared and %d uncommitted file(s) — auto-WIP would not satisfy your rules (exit %d). Commit them yourself, or pass --no-summary.", r.UncommittedFiles, ExitSummaryViolation)
	case r.SummaryConfigured && !r.SummaryPassed && !finishNoSummary:
		r.WouldRefuse = fmt.Sprintf("[summary] gate failed: %d violation(s) across %d commit(s) (exit %d). Amend the commits or pass --no-summary.", r.SummaryViolationCount, r.SummaryCommits, ExitSummaryViolation)
	}

	if finishJSON {
		return emitFinish(r)
	}
	fmt.Printf("DRY-RUN: %s\n", r.Slug)
	fmt.Printf("  project:        %s\n", r.Project)
	fmt.Printf("  worktree:       %s\n", r.WorktreePath)
	fmt.Printf("  branch:         %s\n", r.Branch)
	fmt.Printf("  main branch:    %s\n", r.MainBranch)
	fmt.Printf("  main HEAD:      %s%s\n", current, wrongBranchWarn(current, mainBranch))
	fmt.Printf("  has origin:     %v\n", hasOrigin)
	fmt.Printf("  uncommitted:    %d file(s)\n", r.UncommittedFiles)
	if aheadKnown {
		fmt.Printf("  ahead of %-7s %d commit(s)\n", aheadRef+":", r.CommitsAhead)
	} else {
		fmt.Printf("  ahead of main:  ? (unknown — no main reference resolved)\n")
	}
	fmt.Printf("  main dirty:     %d file(s)%s\n", mainDirty, dirtyWarn(mainDirty))
	switch {
	case r.NoVerify && r.VerifyConfigured:
		fmt.Printf("  verify:         CONFIGURED but --no-verify will SKIP %d command(s)\n", len(r.VerifyRanCommands))
	case r.VerifyConfigured:
		fmt.Printf("  verify:         %d command(s) WILL RUN (dry-run does not execute them)\n", len(r.VerifyRanCommands))
		for _, c := range r.VerifyRanCommands {
			fmt.Printf("                    - %s\n", c)
		}
	}
	fmt.Println()
	if r.WouldRefuse != "" {
		fmt.Printf("WOULD REFUSE: %s\n", r.WouldRefuse)
		return emitFinish(r)
	}
	fmt.Println("Would:")
	step := 1
	if r.VerifyConfigured && !r.NoVerify {
		fmt.Printf("  %d. run [verify] (%d command(s); refuse on first non-zero)\n", step, len(r.VerifyRanCommands))
		step++
	}
	if r.UncommittedFiles > 0 {
		msg := r.Message
		if msg == "" {
			msg = "WIP: " + r.Slug
		}
		fmt.Printf("  %d. commit %d file(s) to %s as %q\n", step, r.UncommittedFiles, r.Branch, msg)
		step++
	}
	if hasOrigin {
		fmt.Printf("  %d. pull --ff-only origin %s\n", step, mainBranch)
		step++
	}
	fmt.Printf("  %d. merge --no-ff %s into %s\n", step, r.Branch, mainBranch)
	step++
	if hasOrigin {
		fmt.Printf("  %d. push %s to origin\n", step, mainBranch)
		step++
	}
	fmt.Printf("  %d. remove worktree %s\n", step, r.WorktreePath)
	step++
	fmt.Printf("  %d. delete local branch %s\n", step, r.Branch)
	return emitFinish(r)
}

func wrongBranchWarn(current, want string) string {
	if current != want {
		return fmt.Sprintf(" (WRONG — expected %q, would refuse)", want)
	}
	return ""
}

func dirtyWarn(n int) string {
	if n > 0 {
		return " (would refuse)"
	}
	return ""
}

func notef(format string, args ...interface{}) {
	if finishJSON {
		return
	}
	fmt.Printf("==> "+format+"\n", args...)
}

// resolveWorktree finds a worktree by slug, optionally filtered to one project.
// Returns the project, the worktree path, and a coded error if there's no match
// or multiple matches across projects.
func resolveWorktree(cfg *config.Config, projectName, slug string) (*config.Project, string, error) {
	projects := cfg.AllProjects()
	type hit struct {
		proj *config.Project
		path string
	}
	var hits []hit
	for i := range projects {
		if projectName != "" && projects[i].Name != projectName {
			continue
		}
		p := filepath.Join(projects[i].Path, ".forktrust", "worktrees", slug)
		if _, err := os.Stat(p); err == nil {
			hits = append(hits, hit{&projects[i], p})
		}
	}
	if len(hits) == 0 {
		extra := ""
		if projectName != "" {
			extra = fmt.Sprintf(" in project %q", projectName)
		}
		return nil, "", coded(ExitNoWorktree, fmt.Errorf("no worktree matching %q%s", slug, extra))
	}
	if len(hits) > 1 {
		names := make([]string, len(hits))
		for i, h := range hits {
			names[i] = h.proj.Name
		}
		return nil, "", coded(ExitAmbiguousSlug, fmt.Errorf("multiple matches: disambiguate with --project (one of: %s)", strings.Join(names, ", ")))
	}
	return hits[0].proj, hits[0].path, nil
}

// refuseIfIgnoredFiles refuses with ExitIgnoredFiles if the worktree contains
// any ignored files (other than forktrust-managed .env.local). Called before
// any removeWorktree invocation because git worktree remove silently deletes
// ignored files without listing them — git status --porcelain does not show them.
func refuseIfIgnoredFiles(wtPath, slug string) error {
	ignoredN, _ := git.IgnoredCount(wtPath)
	if ignoredN == 0 {
		return nil
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "REFUSE: worktree has %d ignored file(s) that would be permanently deleted.\n", ignoredN)
	fmt.Fprintln(os.Stderr, "git worktree remove deletes ignored files without warning (they are not tracked).")
	fmt.Fprintf(os.Stderr, "List them:  git -C %s ls-files --others --ignored --exclude-standard\n", wtPath)
	fmt.Fprintln(os.Stderr, "Move them out of the worktree, then re-run.")
	fmt.Fprintf(os.Stderr, "Or discard: forktrust rm %s --force   (does NOT run finish, just abandons)\n", slug)
	return coded(ExitIgnoredFiles, fmt.Errorf("worktree has %d ignored file(s) that would be lost", ignoredN))
}
