package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
)

var (
	rmForce   bool
	rmProject string
)

var rmCmd = &cobra.Command{
	Use:   "rm <slug>",
	Short: "Abandon a worktree (snapshots WIP as wip/* on origin, then removes)",
	Long: `Abandon a worktree without merging to main:

  1. if there are uncommitted changes — commits them and pushes the branch
     to origin as wip/<branch>-YYYYMMDD (so the work survives somewhere)
  2. removes the worktree
  3. deletes the local branch only if WIP was safely pushed (or never existed)

This is the "never-lose-WIP" path. Use --force to skip the wip-push and
force-remove (only when you really want to throw the work away).`,
	Args: cobra.ExactArgs(1),
	RunE: runRm,
}

func init() {
	rmCmd.Flags().BoolVar(&rmForce, "force", false, "force-remove (skips wip-push, drops uncommitted work)")
	rmCmd.Flags().StringVarP(&rmProject, "project", "p", "", "target project name (required if more than one is registered)")
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

	fmt.Printf("==> target: %s (branch %s in %s)\n", wtPath, branch, proj.Name)

	dirty, err := git.DirtyCount(wtPath)
	if err != nil {
		return err
	}

	wipPushed := false
	if dirty > 0 && !rmForce {
		stamp := time.Now().Format("20060102")
		wipBranch := fmt.Sprintf("wip/%s-%s", strings.TrimPrefix(branch, "fork/"), stamp)
		fmt.Printf("==> %d uncommitted change(s) — committing + pushing as %s\n", dirty, wipBranch)
		if _, err := git.Run(wtPath, "add", "-A"); err != nil {
			return err
		}
		commitMsg := "WIP snapshot before worktree removal (" + time.Now().Format("2006-01-02") + ")"
		if err := git.RunStream(wtPath, "commit", "-m", commitMsg); err != nil {
			return fmt.Errorf("WIP commit failed (pre-commit hook?): %w — re-run with --force to drop the WIP", err)
		}
		if !git.HasOrigin(wtPath) {
			fmt.Fprintln(os.Stderr, "ASK OWNER: no origin remote — WIP commit would only be local.")
			return fmt.Errorf("no origin remote — re-run with --force to remove anyway (keeps local branch)")
		}
		pushRef := fmt.Sprintf("HEAD:refs/heads/%s", wipBranch)
		if err := git.RunStream(wtPath, "push", "origin", pushRef); err != nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "ASK OWNER: push of WIP snapshot to origin/%s failed.\n", wipBranch)
			fmt.Fprintln(os.Stderr, "NOT removing worktree to avoid losing the commit.")
			return fmt.Errorf("WIP push failed — re-run with --force to remove anyway (keeps local branch)")
		}
		wipPushed = true
	}

	if err := git.RemoveWorktree(proj.Path, wtPath, rmForce); err != nil {
		return err
	}

	// Only delete the local branch if either: clean from the start, OR WIP was
	// safely pushed. If we --force'd over a dirty tree, keep the branch so the
	// pre-removal commit isn't orphaned.
	if dirty == 0 || wipPushed {
		if branch != "" && git.HasBranch(proj.Path, branch) {
			if _, err := git.Run(proj.Path, "branch", "-D", branch); err == nil {
				fmt.Printf("==> deleted local branch %s\n", branch)
			}
		}
	} else {
		fmt.Printf("==> KEEPING local branch %s (WIP commit only exists locally — don't lose it)\n", branch)
	}

	fmt.Println("==> done")
	return nil
}
