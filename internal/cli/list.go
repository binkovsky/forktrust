package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all worktrees across registered projects",
	RunE:  runList,
}

func runList(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	projects := cfg.AllProjects()
	if len(projects) == 0 {
		fmt.Fprintln(os.Stderr, "no projects registered and cwd is not a git repo")
		return nil
	}

	home, _ := os.UserHomeDir()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tBRANCH\tPATH\tSTATUS")
	for _, proj := range projects {
		wts, err := git.ListWorktrees(proj.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: list %s: %v\n", proj.Name, err)
			continue
		}
		for _, wt := range wts {
			branch := wt.Branch
			if wt.Detached {
				branch = "(detached)"
			}
			dirty, _ := git.DirtyCount(wt.Path)
			status := "clean"
			if dirty > 0 {
				status = fmt.Sprintf("%d changed", dirty)
			}
			path := wt.Path
			if home != "" && strings.HasPrefix(path, home) {
				path = "~" + path[len(home):]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", proj.Name, branch, path, status)
		}
	}
	return w.Flush()
}
