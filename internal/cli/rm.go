package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
	"github.com/binkovsky/forktrust/internal/ports"
)

var (
	rmForce   bool
	rmProject string
	rmDryRun  bool
	rmJSON    bool
)

var rmCmd = &cobra.Command{
	Use:   "rm <slug>",
	Short: "Abandon a worktree (snapshots WIP as wip/* on origin, then removes)",
	Long: `Abandon a worktree without merging to main. Pipeline:

  1. if there are uncommitted changes, commits them and pushes the branch
     to origin as wip/<branch>-YYYYMMDD-HHMMSS-<sha7> (the never-lose-WIP guarantee)
  2. removes the worktree
  3. deletes the local branch only if WIP was safely pushed or never existed

Use --force only when you really want to throw the work away (skips wip-push,
forces removal).

Exit codes:
  0   success
  5   wip/* snapshot push failed (worktree NOT removed)
  6   no worktree matching slug
  7   slug matches worktrees in multiple projects
  9   no origin remote configured
  12  could not determine ahead count (no main reference resolved); re-run with --force
  13  worktree removed but git branch -D failed (branch lingers)
  14  worktree has ignored files that would be permanently deleted`,
	Args: cobra.ExactArgs(1),
	RunE: runRm,
}

func init() {
	rmCmd.Flags().BoolVar(&rmForce, "force", false, "force-remove (skips wip/* push, drops uncommitted work)")
	rmCmd.Flags().StringVarP(&rmProject, "project", "p", "", "target project name (required if more than one is registered)")
	rmCmd.Flags().BoolVar(&rmDryRun, "dry-run", false, "print the plan without executing anything")
	rmCmd.Flags().BoolVar(&rmJSON, "json", false, "emit a structured JSON result on stdout (one object)")
}

func runRm(_ *cobra.Command, args []string) error {
	slug := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	proj, wtPath, err := resolveWorktree(cfg, rmProject, slug)
	if err != nil {
		return err
	}
	branch, _ := git.CurrentBranch(wtPath)
	dirty, err := git.DirtyCount(wtPath)
	if err != nil {
		return err
	}

	// Detached HEAD worktrees are not something forktrust creates; refusing
	// rm of one keeps us from constructing a malformed wip ref like
	// "wip/-<stamp>" and from deleting a nameless branch we never owned.
	//
	// We do NOT release the port block here even though the user will have
	// to clean up via `git worktree remove`. Releasing now while the
	// worktree still lives lets a subsequent `forktrust new` re-allocate
	// the same range — the user's still-bound services (in the detached
	// worktree) collide with the new ones. The orphan-prune pass on the
	// next Allocate call cleans the stale entry once the directory is
	// actually gone.
	if branch == "" {
		fmt.Fprintf(os.Stderr, "REFUSE: worktree at %s is detached (no branch).\n", wtPath)
		fmt.Fprintln(os.Stderr, "forktrust does not manage detached worktrees. Clean up manually:")
		fmt.Fprintf(os.Stderr, "  git -C %s worktree remove %s\n", proj.Path, wtPath)
		fmt.Fprintln(os.Stderr, "Port block (if any) will be auto-released on the next `forktrust new` once the dir is gone.")
		return coded(ExitGenericError, fmt.Errorf("worktree is detached; refusing"))
	}

	// Count commits the branch has that are NOT on origin/<mainBranch> (or
	// local mainBranch if no origin). These would be SILENTLY LOST by a
	// plain `git worktree remove` + `branch -D`, so we treat them as work
	// to snapshot just like uncommitted dirty files.
	//
	// FAIL-CLOSED: if neither reference resolves, aheadKnown=false and we
	// must refuse (or, with --force, KEEP the branch — see hadWork below).
	mainBranch := proj.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}
	hasOrigin := git.HasOrigin(wtPath)
	ahead, aheadKnown := computeAheadCascade(wtPath, mainBranch, hasOrigin)

	// wipBranch is computed lazily (see buildWipBranch below) so the SHA
	// component reflects the FINAL tip commit — after any uncommitted WIP is
	// committed — making same-second collision structurally impossible.
	stamp := time.Now().Format("20060102-150405")
	buildWipBranch := func(wtPath string) string {
		slug := strings.TrimPrefix(branch, "fork/")
		sha := git.ShortSHA(wtPath)
		if sha == "" {
			return fmt.Sprintf("wip/%s-%s", slug, stamp)
		}
		return fmt.Sprintf("wip/%s-%s-%s", slug, stamp, sha)
	}
	// wipBranch for dry-run preview (uses current HEAD, before any WIP commit).
	wipBranch := buildWipBranch(wtPath)

	r := rmResult{
		Project:          proj.Name,
		Slug:             slug,
		WorktreePath:     wtPath,
		Branch:           branch,
		DryRun:           rmDryRun,
		Force:            rmForce,
		UncommittedFiles: dirty,
		CommitsAhead:     ahead,
		AheadKnown:       aheadKnown,
	}

	// Dry-run MUST never refuse — its whole purpose is to safely preview.
	// previewRm surfaces the would-refuse via r.WouldRefuse instead.
	if rmDryRun {
		return previewRm(r, wipBranch, proj.Path, ahead, aheadKnown, hasOrigin)
	}

	// Non-dry-run: refuse if ahead is unknown and not --force. Build the
	// "Tried" list from what computeAheadCascade actually attempted so the
	// message doesn't lie about a HEAD fallback that no longer exists.
	if !aheadKnown && !rmForce {
		tried := []string{}
		if hasOrigin {
			tried = append(tried, "origin/"+mainBranch)
		}
		tried = append(tried, "local "+mainBranch)
		fmt.Fprintln(os.Stderr, "REFUSE: could not determine if branch has unpushed work.")
		fmt.Fprintf(os.Stderr, "Tried: %s — all failed.\n", strings.Join(tried, ", "))
		if hasOrigin {
			fmt.Fprintf(os.Stderr, "Push origin/%s first (so we have a reference), or:\n", mainBranch)
		} else {
			fmt.Fprintf(os.Stderr, "Create local %s first, or:\n", mainBranch)
		}
		fmt.Fprintf(os.Stderr, "  forktrust rm %s --force   # keeps the local branch so any commits stay reachable\n", slug)
		return coded(ExitAheadUnknown, fmt.Errorf("could not determine ahead count; refuse to delete without --force"))
	}

	rmf("target: %s (branch %s in %s)", wtPath, branch, proj.Name)

	// PRE-FLIGHT: refuse before any side effect (commit / push / remove).
	// These checks must run BEFORE the snapshot block so we never create a
	// WIP commit or push wip/* and then refuse — leaving partially-applied
	// state and a confusing error.
	if !rmForce {
		// 1. Ignored files would be silently deleted by git worktree remove.
		if ignoredN, _ := git.IgnoredCount(wtPath); ignoredN > 0 {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: worktree has %d ignored file(s) (e.g. secret.log, build artifacts)\n", ignoredN)
			fmt.Fprintln(os.Stderr, "that are NOT tracked by git and would be PERMANENTLY DELETED by `git worktree remove`.")
			fmt.Fprintln(os.Stderr, "Move them out of the worktree first, then re-run.")
			fmt.Fprintf(os.Stderr, "List them: git -C %s ls-files --others --ignored --exclude-standard\n", wtPath)
			fmt.Fprintf(os.Stderr, "Or drop them: forktrust rm %s --force\n", slug)
			return coded(ExitIgnoredFiles, fmt.Errorf("worktree has %d ignored file(s) that would be lost", ignoredN))
		}
		// 2. No-origin refuse: if there is work to snapshot but nowhere to push,
		// refuse now rather than after the WIP commit has been made.
		if (dirty > 0 || ahead > 0) && !hasOrigin {
			fmt.Fprintln(os.Stderr, "REFUSE: no origin remote, WIP would only be local.")
			fmt.Fprintln(os.Stderr, "Re-run with --force to remove anyway (keeps local branch with the commits).")
			return coded(ExitNoOriginRemote, fmt.Errorf("no origin remote"))
		}
	}

	// Snapshot path triggers on EITHER uncommitted changes OR committed-but-
	// -unpushed commits ahead of main. This closes the never-lose-WIP gap.
	if (dirty > 0 || ahead > 0) && !rmForce {
		if dirty > 0 {
			rmf("%d uncommitted change(s), committing before snapshot", dirty)
			if _, err := git.Run(wtPath, "add", "-A"); err != nil {
				return err
			}
			commitMsg := "WIP snapshot before worktree removal (" + time.Now().Format("2006-01-02") + ")"
			if err := gitStream(rmJSON, wtPath, "commit", "-m", commitMsg); err != nil {
				return coded(ExitHookFailed, fmt.Errorf("WIP commit failed (pre-commit hook?): %w. Use --force to drop the WIP", err))
			}
		}
		// Rebuild wipBranch AFTER any WIP commit so the SHA component
		// reflects the final tip. This makes same-second collision impossible:
		// two rm runs on different branches always have different SHAs.
		wipBranch = buildWipBranch(wtPath)
		if dirty > 0 {
			rmf("pushing snapshot as %s", wipBranch)
		} else {
			rmf("0 uncommitted but %d commit(s) ahead of %s — pushing as %s", ahead, mainBranch, wipBranch)
		}
		if !hasOrigin {
			// Should be unreachable after the pre-flight above; kept as a
			// safety net in case rmForce changed mid-flight (it can't, but
			// belt + suspenders on irreversible operations).
			fmt.Fprintln(os.Stderr, "REFUSE: no origin remote, WIP would only be local.")
			fmt.Fprintln(os.Stderr, "Re-run with --force to remove anyway (keeps local branch with the commits).")
			return coded(ExitNoOriginRemote, fmt.Errorf("no origin remote"))
		}
		pushRef := fmt.Sprintf("HEAD:refs/heads/%s", wipBranch)
		if err := gitStream(rmJSON, wtPath, "push", "origin", pushRef); err != nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: push of WIP snapshot to origin/%s failed.\n", wipBranch)
			fmt.Fprintln(os.Stderr, "NOT removing worktree to avoid losing the commit.")
			fmt.Fprintf(os.Stderr, "Inspect: cd %s && git push origin %s\n", wtPath, pushRef)
			return coded(ExitWipPushFailed, fmt.Errorf("WIP push failed. Use --force to remove anyway (keeps local branch)"))
		}
		r.WipBranch = wipBranch
		r.WipPushed = true
	}

	// (ignored-file and no-origin pre-flights already ran above; no repeated check needed)

	if err := removeWorktree(rmJSON, proj.Path, wtPath, rmForce); err != nil {
		return err
	}
	r.WorktreeRemoved = true

	// Release any port block this slug owned (no-op if none).
	if storePath, perr := ports.DefaultPath(); perr == nil {
		_ = ports.Release(storePath, proj.Path, slug)
	}

	// Only delete the local branch if either:
	//   - PROVABLY clean (no dirty AND no unpushed commits AND we KNOW the
	//     ahead count — !aheadKnown forces the conservative branch), OR
	//   - WIP was safely pushed to wip/* on origin.
	// If we --force'd over an unknown state, KEEP the branch so any commits
	// remain reachable. The fail-closed guarantee must hold even with --force.
	hadWork := dirty > 0 || ahead > 0 || !aheadKnown
	if !hadWork || r.WipPushed {
		if branch != "" && git.HasBranch(proj.Path, branch) {
			// Surface branch -D failures (was previously silently
			// swallowed). If we can't delete the branch, the user needs
			// to know so they don't think rm succeeded fully.
			if _, err := git.Run(proj.Path, "branch", "-D", branch); err == nil {
				rmf("deleted local branch %s", branch)
				r.BranchDeleted = true
			} else {
				// R4 fix: surface AND exit non-zero so scripts/agents don't
				// see green when the branch they expected to be gone still
				// lingers. Worktree IS removed and ports ARE released, so
				// it's not a total failure — distinct exit code.
				fmt.Fprintln(os.Stderr)
				fmt.Fprintf(os.Stderr, "WARN: could not delete local branch %s: %v\n", branch, err)
				fmt.Fprintf(os.Stderr, "  The branch lingers. Investigate with: git -C %s branch | grep %s\n", proj.Path, branch)
				r.BranchKept = true
				_ = emitRm(r)
				return coded(ExitBranchNotDeleted, fmt.Errorf("branch -D %s failed: %w", branch, err))
			}
		}
	} else {
		rmf("KEEPING local branch %s (WIP commit only exists locally, don't lose it)", branch)
		r.BranchKept = true
	}

	rmf("done")
	return emitRm(r)
}

func previewRm(r rmResult, wipBranch, mainPath string, ahead int, aheadKnown bool, hasOrigin bool) error {
	// Mirror actual rm pre-flight order exactly so dry-run always agrees with real rm.
	hadWork := r.UncommittedFiles > 0 || ahead > 0 || !aheadKnown
	ignoredN, _ := git.IgnoredCount(r.WorktreePath)
	switch {
	case ignoredN > 0 && !r.Force:
		r.WouldRefuse = fmt.Sprintf("worktree has %d ignored file(s) that would be permanently deleted (exit %d). Move them out or use --force.", ignoredN, ExitIgnoredFiles)
	case !aheadKnown && !r.Force:
		r.WouldRefuse = fmt.Sprintf("could not determine ahead count (exit %d). Push origin/main first, or re-run with --force (keeps local branch).", ExitAheadUnknown)
	case hadWork && !r.Force && !hasOrigin:
		r.WouldRefuse = fmt.Sprintf("no origin remote (exit %d). Re-run with --force to remove anyway.", ExitNoOriginRemote)
	}
	wouldSnapshot := hadWork && !r.Force && aheadKnown
	r.WouldPushWip = wouldSnapshot && hasOrigin && r.WouldRefuse == ""

	if rmJSON {
		return emitRm(r)
	}
	fmt.Printf("DRY-RUN: rm %s\n", r.Slug)
	fmt.Printf("  project:       %s\n", r.Project)
	fmt.Printf("  worktree:      %s\n", r.WorktreePath)
	fmt.Printf("  branch:        %s\n", r.Branch)
	fmt.Printf("  uncommitted:   %d file(s)\n", r.UncommittedFiles)
	if aheadKnown {
		fmt.Printf("  ahead of main: %d commit(s)\n", ahead)
	} else {
		fmt.Printf("  ahead of main: ? (unknown — no main reference resolved)\n")
	}
	fmt.Printf("  force:         %v\n", r.Force)
	fmt.Printf("  has origin:    %v\n", hasOrigin)
	fmt.Println()
	if r.WouldRefuse != "" {
		fmt.Printf("WOULD REFUSE: %s\n", r.WouldRefuse)
		return emitRm(r)
	}
	fmt.Println("Would:")
	step := 1
	if r.WouldPushWip {
		if r.UncommittedFiles > 0 {
			fmt.Printf("  %d. commit %d file(s) as %q\n", step, r.UncommittedFiles, "WIP snapshot before worktree removal ...")
			step++
		}
		fmt.Printf("  %d. push to origin/%s (never-lose-WIP snapshot)\n", step, wipBranch)
		step++
	}
	if r.Force && hadWork {
		// Real rm KEEPS the branch when there's work AND --force AND the
		// snapshot wasn't pushed. Split the message so we don't claim
		// "committed work preserved" when only uncommitted edits existed.
		hasCommitted := ahead > 0 || !aheadKnown
		switch {
		case r.UncommittedFiles > 0 && hasCommitted:
			fmt.Printf("  %d. FORCE remove worktree %s (drops %d uncommitted file(s); committed work stays on local branch)\n", step, r.WorktreePath, r.UncommittedFiles)
		case r.UncommittedFiles > 0:
			fmt.Printf("  %d. FORCE remove worktree %s (DROPS %d uncommitted file(s); no committed work to preserve)\n", step, r.WorktreePath, r.UncommittedFiles)
		case hasCommitted:
			fmt.Printf("  %d. FORCE remove worktree %s (committed work stays on local branch)\n", step, r.WorktreePath)
		default:
			fmt.Printf("  %d. FORCE remove worktree %s\n", step, r.WorktreePath)
		}
	} else {
		fmt.Printf("  %d. remove worktree %s\n", step, r.WorktreePath)
	}
	step++
	if !r.Force || !hadWork || r.WouldPushWip {
		fmt.Printf("  %d. delete local branch %s\n", step, r.Branch)
	} else if ahead > 0 || !aheadKnown {
		fmt.Printf("  %d. KEEP local branch %s (committed work remains reachable via this branch)\n", step, r.Branch)
	} else {
		fmt.Printf("  %d. KEEP local branch %s (precaution: unknown work state)\n", step, r.Branch)
	}
	_ = mainPath
	return emitRm(r)
}

func rmf(format string, args ...interface{}) {
	if rmJSON {
		return
	}
	fmt.Printf("==> "+format+"\n", args...)
}

// computeAheadCascade tries to count commits on the current branch that would
// be lost if it were deleted. Returns (count, known).
//
// Two tiers only:
//  1. origin/<mainBranch>  (canonical published reference; preferred)
//  2. <mainBranch>         (local reference; works when no origin or
//     origin not yet populated)
//
// A HEAD-only rev-list was considered as a third tier and dropped: it counts
// the ENTIRE branch history (including commits already on main), which would
// falsely report a fresh fork as having thousands of commits to snapshot.
// Better to return known=false here and let the caller refuse than to lie.
func computeAheadCascade(wt, mainBranch string, hasOrigin bool) (int, bool) {
	if hasOrigin {
		if n, err := git.CommitsAhead(wt, "origin/"+mainBranch); err == nil {
			return n, true
		}
	}
	if n, err := git.CommitsAhead(wt, mainBranch); err == nil {
		return n, true
	}
	return 0, false
}
