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
     to origin as wip/<branch>-YYYYMMDD (the never-lose-WIP guarantee)
  2. removes the worktree
  3. deletes the local branch only if WIP was safely pushed or never existed

Use --force only when you really want to throw the work away (skips wip-push,
forces removal).

Exit codes:
  0  success
  5  wip/* snapshot push failed (worktree NOT removed)
  6  no worktree matching slug
  7  slug matches worktrees in multiple projects
  9  no origin remote configured`,
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

	// Count commits the branch has that are NOT on origin/<mainBranch> (or
	// local mainBranch if no origin). These would be SILENTLY LOST by a
	// plain `git worktree remove` + `branch -D`, so we treat them as work
	// to snapshot just like uncommitted dirty files.
	mainBranch := proj.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}
	hasOrigin := git.HasOrigin(wtPath)
	aheadRef := "origin/" + mainBranch
	if !hasOrigin {
		aheadRef = mainBranch
	}
	ahead := 0
	if branch != "" {
		// Best-effort: ignore error (e.g. ref unknown means branch is
		// effectively isolated, treat as ahead=0 — user can use --force).
		if n, err := git.CommitsAhead(wtPath, aheadRef); err == nil {
			ahead = n
		}
	}

	stamp := time.Now().Format("20060102")
	wipBranch := fmt.Sprintf("wip/%s-%s", strings.TrimPrefix(branch, "fork/"), stamp)

	r := rmResult{
		Project:          proj.Name,
		Slug:             slug,
		WorktreePath:     wtPath,
		Branch:           branch,
		DryRun:           rmDryRun,
		Force:            rmForce,
		UncommittedFiles: dirty,
	}

	if rmDryRun {
		r.CommitsAhead = ahead
		return previewRm(r, wipBranch, proj.Path, ahead, hasOrigin)
	}

	rmf("target: %s (branch %s in %s)", wtPath, branch, proj.Name)

	// Snapshot path triggers on EITHER uncommitted changes OR committed-but-
	// -unpushed commits ahead of main. This closes the never-lose-WIP gap.
	if (dirty > 0 || ahead > 0) && !rmForce {
		if dirty > 0 {
			rmf("%d uncommitted change(s), committing + pushing as %s", dirty, wipBranch)
			if _, err := git.Run(wtPath, "add", "-A"); err != nil {
				return err
			}
			commitMsg := "WIP snapshot before worktree removal (" + time.Now().Format("2006-01-02") + ")"
			if err := gitStream(rmJSON, wtPath, "commit", "-m", commitMsg); err != nil {
				return coded(ExitHookFailed, fmt.Errorf("WIP commit failed (pre-commit hook?): %w. Use --force to drop the WIP", err))
			}
		} else {
			rmf("0 uncommitted but %d commit(s) ahead of %s — pushing as %s", ahead, aheadRef, wipBranch)
		}
		if !hasOrigin {
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

	if err := removeWorktree(rmJSON, proj.Path, wtPath, rmForce); err != nil {
		return err
	}
	r.WorktreeRemoved = true

	// Release any port block this slug owned (no-op if none).
	if storePath, perr := ports.DefaultPath(); perr == nil {
		_ = ports.Release(storePath, proj.Path, slug)
	}

	// Only delete the local branch if either:
	//   - clean (no dirty AND no unpushed commits) from the start, OR
	//   - WIP was safely pushed to wip/* on origin.
	// If we --force'd over real work, keep the branch so commits aren't orphaned.
	hadWork := dirty > 0 || ahead > 0
	if !hadWork || r.WipPushed {
		if branch != "" && git.HasBranch(proj.Path, branch) {
			if _, err := git.Run(proj.Path, "branch", "-D", branch); err == nil {
				rmf("deleted local branch %s", branch)
				r.BranchDeleted = true
			}
		}
	} else {
		rmf("KEEPING local branch %s (WIP commit only exists locally, don't lose it)", branch)
		r.BranchKept = true
	}

	rmf("done")
	return emitRm(r)
}

func previewRm(r rmResult, wipBranch, mainPath string, ahead int, hasOrigin bool) error {
	// Mirror actual rm logic: wip-snapshot fires when EITHER uncommitted
	// files OR commits-ahead exist (and not --force). No-origin without
	// --force refuses; --force always proceeds.
	hadWork := r.UncommittedFiles > 0 || ahead > 0
	wouldSnapshot := hadWork && !r.Force
	if wouldSnapshot && !hasOrigin {
		r.WouldRefuse = fmt.Sprintf("no origin remote (exit %d). Re-run with --force to remove anyway.", ExitNoOriginRemote)
	}
	r.WouldPushWip = wouldSnapshot && hasOrigin && r.WouldRefuse == ""

	if rmJSON {
		return emitRm(r)
	}
	fmt.Printf("DRY-RUN: rm %s\n", r.Slug)
	fmt.Printf("  project:       %s\n", r.Project)
	fmt.Printf("  worktree:      %s\n", r.WorktreePath)
	fmt.Printf("  branch:        %s\n", r.Branch)
	fmt.Printf("  uncommitted:   %d file(s)\n", r.UncommittedFiles)
	fmt.Printf("  ahead of main: %d commit(s)\n", ahead)
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
		fmt.Printf("  %d. FORCE remove worktree %s (drops %d uncommitted, %d ahead-commit(s))\n", step, r.WorktreePath, r.UncommittedFiles, ahead)
	} else {
		fmt.Printf("  %d. remove worktree %s\n", step, r.WorktreePath)
	}
	step++
	if !r.Force || !hadWork || r.WouldPushWip {
		fmt.Printf("  %d. delete local branch %s\n", step, r.Branch)
	} else {
		fmt.Printf("  %d. KEEP local branch %s (work exists locally only)\n", step, r.Branch)
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
