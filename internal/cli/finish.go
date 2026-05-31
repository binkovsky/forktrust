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

	// 1. Commit any uncommitted WIP on the worktree branch.
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

	// 2. How many commits ahead of mainBranch? Use origin/<main> if origin
	// exists (matches what `git push` would compare against); else use local
	// <main> ref (no-origin / offline path).
	hasOrigin := git.HasOrigin(proj.Path)
	aheadRef := "origin/" + mainBranch
	if !hasOrigin {
		aheadRef = mainBranch
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
		_, _ = git.Run(proj.Path, "branch", "-D", branch)
		r.WorktreeRemoved = true
		r.BranchDeleted = true
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
	if _, err := git.Run(proj.Path, "branch", "-D", branch); err == nil {
		notef("deleted local branch %s", branch)
		r.BranchDeleted = true
	}

	// 8. Release any port block this slug owned (no-op if none).
	if storePath, perr := ports.DefaultPath(); perr == nil {
		_ = ports.Release(storePath, proj.Path, slug)
	}

	notef("finish done")
	return emitFinish(r)
}

func previewFinish(r finishResult, mainBranch, mainPath string) error {
	ahead, _ := git.CommitsAhead(r.WorktreePath, "origin/"+mainBranch)
	r.CommitsAhead = ahead
	mainDirty, _ := git.DirtyCount(mainPath)
	r.MainDirty = mainDirty
	// In --json mode emit ONLY the JSON document; the human-readable preview
	// would otherwise corrupt the stdout document.
	if finishJSON {
		return emitFinish(r)
	}
	fmt.Printf("DRY-RUN: %s\n", r.Slug)
	fmt.Printf("  project:        %s\n", r.Project)
	fmt.Printf("  worktree:       %s\n", r.WorktreePath)
	fmt.Printf("  branch:         %s\n", r.Branch)
	fmt.Printf("  main branch:    %s\n", r.MainBranch)
	fmt.Printf("  uncommitted:    %d file(s)\n", r.UncommittedFiles)
	fmt.Printf("  ahead of main:  %d commit(s)\n", r.CommitsAhead)
	fmt.Printf("  main dirty:     %d file(s)%s\n", mainDirty, dirtyWarn(mainDirty))
	fmt.Println()
	fmt.Println("Would:")
	if r.UncommittedFiles > 0 {
		msg := r.Message
		if msg == "" {
			msg = "WIP: " + r.Slug
		}
		fmt.Printf("  1. commit %d file(s) to %s as %q\n", r.UncommittedFiles, r.Branch, msg)
	}
	fmt.Printf("  2. pull --ff-only origin %s\n", mainBranch)
	fmt.Printf("  3. merge --no-ff %s into %s\n", r.Branch, mainBranch)
	fmt.Printf("  4. push %s to origin\n", mainBranch)
	fmt.Printf("  5. remove worktree %s\n", r.WorktreePath)
	fmt.Printf("  6. delete local branch %s\n", r.Branch)
	if mainDirty > 0 {
		fmt.Println()
		fmt.Println("WOULD REFUSE: main is dirty. Re-run after committing/stashing main's WIP.")
	}
	return emitFinish(r)
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
