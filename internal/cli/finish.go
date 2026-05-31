package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
)

var (
	finishMessage string
	finishProject string
)

var finishCmd = &cobra.Command{
	Use:   "finish <slug>",
	Short: "Commit WIP, merge to main, push, remove worktree (refuses on conflict)",
	Long: `Canonical end-of-chat command:

  1. commits any uncommitted WIP on the worktree branch
  2. fetches origin/main, fast-forward-pulls the main checkout
  3. merges --no-ff the worktree branch into main
  4. pushes main to origin
  5. removes the worktree and branch

Refuses on merge conflict (never auto-resolves) and refuses if the main
checkout has uncommitted changes (would risk losing the owner's WIP).`,
	Args: cobra.ExactArgs(1),
	RunE: runFinish,
}

func init() {
	finishCmd.Flags().StringVarP(&finishMessage, "message", "m", "", "commit message for uncommitted WIP (default \"WIP: <slug>\")")
	finishCmd.Flags().StringVarP(&finishProject, "project", "p", "", "target project name (required if more than one is registered)")
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
		return fmt.Errorf("worktree at %s is detached — finish needs a branch", wtPath)
	}

	fmt.Printf("==> finish target: %s (branch %s in %s)\n", wtPath, branch, proj.Name)

	// 1. Commit any uncommitted WIP on the worktree branch.
	dirty, err := git.DirtyCount(wtPath)
	if err != nil {
		return err
	}
	if dirty > 0 {
		msg := finishMessage
		if msg == "" {
			msg = "WIP: " + slug
		}
		fmt.Printf("==> %d uncommitted change(s) — committing to %s (%q)\n", dirty, branch, msg)
		if _, err := git.Run(wtPath, "add", "-A"); err != nil {
			return err
		}
		if err := git.RunStream(wtPath, "commit", "-m", msg); err != nil {
			return fmt.Errorf("commit failed (pre-commit hook?): %w", err)
		}
	}

	// 2. Resolve main branch name (defaults to "main").
	mainBranch := proj.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}

	// 3. How many commits ahead of origin/<main>?
	_, _ = git.Run(proj.Path, "fetch", "-q", "origin", mainBranch)
	ahead, err := git.CommitsAhead(wtPath, "origin/"+mainBranch)
	if err != nil {
		return err
	}
	if ahead == 0 {
		fmt.Printf("==> branch %s has no commits ahead of origin/%s — nothing to merge\n", branch, mainBranch)
		if err := git.RemoveWorktree(proj.Path, wtPath, false); err != nil {
			return err
		}
		_, _ = git.Run(proj.Path, "branch", "-D", branch)
		return nil
	}
	fmt.Printf("==> branch is %d commit(s) ahead of origin/%s — merging\n", ahead, mainBranch)

	// 4. Main checkout must be clean to safely merge into it.
	mainDirty, err := git.DirtyCount(proj.Path)
	if err != nil {
		return err
	}
	if mainDirty > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "ASK OWNER: main working tree in %s has %d uncommitted change(s).\n", proj.Path, mainDirty)
		fmt.Fprintln(os.Stderr, "Cannot safely merge without overwriting these. Commit/stash them first, then re-run.")
		return fmt.Errorf("main worktree is dirty")
	}

	// 5. Fast-forward main to origin/main.
	if err := git.RunStream(proj.Path, "pull", "--ff-only", "origin", mainBranch); err != nil {
		return fmt.Errorf("pull --ff-only failed: %w", err)
	}

	// 6. Merge the worktree branch. --no-ff keeps it visible in history.
	fmt.Printf("==> merging %s into %s\n", branch, mainBranch)
	if err := git.RunStream(proj.Path, "merge", "--no-ff", "--no-edit", branch); err != nil {
		_, _ = git.Run(proj.Path, "merge", "--abort")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "ASK OWNER: merge of %s into %s produced conflicts. Merge aborted to leave main clean.\n", branch, mainBranch)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Inspect: cd %s && git merge %s\n", proj.Path, branch)
		fmt.Fprintf(os.Stderr, "Or:      forktrust rm %s   (abandon and snapshot to wip/*)\n", slug)
		return fmt.Errorf("merge conflict — refuse to auto-resolve")
	}

	// 7. Push main.
	if git.HasOrigin(proj.Path) {
		fmt.Printf("==> pushing %s to origin\n", mainBranch)
		if err := git.RunStream(proj.Path, "push", "origin", mainBranch); err != nil {
			return fmt.Errorf("push failed (auth? non-fast-forward?): %w", err)
		}
	} else {
		fmt.Printf("==> no origin remote — %s is up-to-date locally only\n", mainBranch)
	}

	// 8. Remove the worktree + branch.
	if err := git.RemoveWorktree(proj.Path, wtPath, false); err != nil {
		return err
	}
	if _, err := git.Run(proj.Path, "branch", "-D", branch); err == nil {
		fmt.Printf("==> deleted local branch %s\n", branch)
	}

	fmt.Println("==> finish done")
	return nil
}

// resolveWorktree finds a worktree by slug, optionally filtered to one project.
// Returns the project, the worktree path, and an error if there's no match or
// multiple matches across projects.
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
		return nil, "", fmt.Errorf("no worktree matching %q%s", slug, extra)
	}
	if len(hits) > 1 {
		names := make([]string, len(hits))
		for i, h := range hits {
			names[i] = h.proj.Name
		}
		return nil, "", fmt.Errorf("multiple matches — disambiguate with --project (one of: %s)", strings.Join(names, ", "))
	}
	return hits[0].proj, hits[0].path, nil
}
