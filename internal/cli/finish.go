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
)

var (
	finishMessage string
	finishProject string
	finishDryRun  bool
	finishJSON    bool
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
  0  success
  2  merge conflict (refuse to auto-resolve)
  3  main worktree is dirty
  4  push to origin failed
  6  no worktree matching slug
  7  slug matches worktrees in multiple projects`,
	Args: cobra.ExactArgs(1),
	RunE: runFinish,
}

func init() {
	finishCmd.Flags().StringVarP(&finishMessage, "message", "m", "", "commit message for uncommitted WIP (default \"WIP: <slug>\")")
	finishCmd.Flags().StringVarP(&finishProject, "project", "p", "", "target project name (required if more than one is registered)")
	finishCmd.Flags().BoolVar(&finishDryRun, "dry-run", false, "print the plan without executing anything")
	finishCmd.Flags().BoolVar(&finishJSON, "json", false, "emit a structured JSON result on stdout (one object)")
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

	// Only fetch if we have origin; otherwise rev-list against origin/main
	// would crash with "ambiguous argument" downstream.
	if git.HasOrigin(proj.Path) {
		_, _ = git.Run(proj.Path, "fetch", "-q", "origin", mainBranch)
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

	// 2. NOW it's safe to commit uncommitted WIP on the worktree branch.
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
		return err
	}
	r.CommitsAhead = ahead
	if ahead == 0 {
		notef("branch %s has no commits ahead of %s, nothing to merge", branch, aheadRef)
		if err := removeWorktree(finishJSON, proj.Path, wtPath, false); err != nil {
			return err
		}
		r.WorktreeRemoved = true
		// Release port block BEFORE branch -D — same order as the post-merge
		// path — so a branch-delete failure doesn't block port cleanup.
		if storePath, perr := ports.DefaultPath(); perr == nil {
			_ = ports.Release(storePath, proj.Path, slug)
		}
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

	// 3. Main checkout must be ON mainBranch — otherwise we'd merge into
	// whatever HEAD happens to be (e.g. dev), pretending we shipped to main.
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

	// 4. Main checkout must be clean to safely merge into it.
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

	// 5. Fast-forward main to origin/main (only if we have origin).
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
	if err := removeWorktree(finishJSON, proj.Path, wtPath, false); err != nil {
		return err
	}
	r.WorktreeRemoved = true

	// 8. Release any port block this slug owned (no-op if none). Done BEFORE
	// branch -D so a branch-delete failure still leaves ports released.
	if storePath, perr := ports.DefaultPath(); perr == nil {
		_ = ports.Release(storePath, proj.Path, slug)
	}

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

	switch {
	case !aheadKnown:
		r.WouldRefuse = fmt.Sprintf("no main reference resolved (exit %d). Push origin/%s or create local %s first.", ExitAheadUnknown, mainBranch, mainBranch)
	case current != mainBranch:
		r.WouldRefuse = fmt.Sprintf("main checkout on %q, expected %q (exit %d)", current, mainBranch, ExitMainOnWrongBranch)
	case mainDirty > 0:
		r.WouldRefuse = fmt.Sprintf("main checkout is dirty (%d uncommitted file(s)) (exit %d)", mainDirty, ExitDirtyMain)
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
	fmt.Println()
	if r.WouldRefuse != "" {
		fmt.Printf("WOULD REFUSE: %s\n", r.WouldRefuse)
		return emitFinish(r)
	}
	fmt.Println("Would:")
	step := 1
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
