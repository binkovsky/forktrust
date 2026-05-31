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

var listJSON bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all worktrees across registered projects",
	Long: `List every worktree in every registered project, including the main checkout
and any non-forktrust worktrees. Shows project, branch, path, dirty status.

Use --json for structured output (stable schema for AI agents / scripts).`,
	RunE: runList,
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "emit a structured JSON object on stdout")
}

func runList(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	projects := cfg.AllProjects()
	if len(projects) == 0 {
		if listJSON {
			return emitList(listResult{Worktrees: []worktreeEntry{}})
		}
		fmt.Fprintln(os.Stderr, "no projects registered and cwd is not a git repo")
		return nil
	}

	var entries []worktreeEntry
	for _, proj := range projects {
		wts, err := git.ListWorktrees(proj.Path)
		if err != nil {
			if !listJSON {
				fmt.Fprintf(os.Stderr, "warn: list %s: %v\n", proj.Name, err)
			}
			continue
		}
		for _, wt := range wts {
			dirty, _ := git.DirtyCount(wt.Path)
			entries = append(entries, worktreeEntry{
				Project:  proj.Name,
				Path:     wt.Path,
				Branch:   wt.Branch,
				Detached: wt.Detached,
				Dirty:    dirty,
				IsMain:   wt.Path == proj.Path,
			})
		}
	}

	if listJSON {
		return emitList(listResult{Worktrees: entries})
	}

	home, _ := os.UserHomeDir()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tBRANCH\tPATH\tSTATUS")
	for _, e := range entries {
		branch := e.Branch
		if e.Detached {
			branch = "(detached)"
		}
		status := "clean"
		if e.Dirty > 0 {
			status = fmt.Sprintf("%d changed", e.Dirty)
		}
		path := e.Path
		if home != "" && strings.HasPrefix(path, home) {
			path = "~" + path[len(home):]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Project, branch, path, status)
	}
	return w.Flush()
}
