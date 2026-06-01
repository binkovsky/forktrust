package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
	"github.com/binkovsky/forktrust/internal/ports"
)

var (
	statusWatch    bool
	statusInterval time.Duration
	statusJSON     bool
	statusProject  string
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Rich dashboard of all forktrust worktrees (commits ahead/behind, ports, age)",
	Long: `Like ` + "`list`" + ` but enriched: per worktree shows commits ahead and behind the
main branch, dirty file count, allocated port range (if any), and age since
creation.

With --watch, refreshes every --interval (default 5s) until Ctrl-C.`,
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().BoolVarP(&statusWatch, "watch", "w", false, "auto-refresh until interrupted")
	statusCmd.Flags().DurationVar(&statusInterval, "interval", 5*time.Second, "refresh interval for --watch")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "emit a structured JSON object on stdout")
	statusCmd.Flags().StringVarP(&statusProject, "project", "p", "", "limit to one registered project")
}

// statusRow is the JSON schema (and table row) for a single worktree's status.
type statusRow struct {
	Project    string `json:"project"`
	Slug       string `json:"slug"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	IsMain     bool   `json:"is_main"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
	AheadKnown bool   `json:"ahead_known"` // false means Ahead/Behind are meaningless (no main ref resolved)
	Dirty      int    `json:"dirty"`
	PortStart  int    `json:"port_start,omitempty"`
	PortEnd    int    `json:"port_end,omitempty"`
	AgeSeconds int64  `json:"age_seconds"`
}

type statusResult struct {
	Worktrees []statusRow `json:"worktrees"`
}

func runStatus(_ *cobra.Command, _ []string) error {
	if !statusWatch {
		return renderStatus()
	}
	if statusInterval < 100*time.Millisecond {
		return fmt.Errorf("interval too small: %s (min 100ms)", statusInterval)
	}

	// Watch mode: clear screen + redraw on tick. Exit on SIGINT/SIGTERM.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tick := time.NewTicker(statusInterval)
	defer tick.Stop()
	if err := redrawStatus(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			fmt.Println()
			return nil
		case <-tick.C:
			if err := redrawStatus(); err != nil {
				return err
			}
		}
	}
}

func redrawStatus() error {
	// ANSI clear screen + cursor home. Skipped in JSON mode (watch+json is
	// streaming JSON-lines; print one document per tick).
	if !statusJSON {
		fmt.Print("\033[2J\033[H")
		fmt.Printf("forktrust status — %s (Ctrl-C to exit)\n\n", time.Now().Format("15:04:05"))
	}
	return renderStatus()
}

func renderStatus() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	projects := cfg.AllProjects()
	if len(projects) == 0 {
		if statusJSON {
			return emitJSON(true, statusResult{Worktrees: []statusRow{}})
		}
		fmt.Fprintln(os.Stderr, "no projects registered and cwd is not a git repo")
		return nil
	}

	storePath, _ := ports.DefaultPath()
	allBlocks, _ := ports.List(storePath)
	blockByKey := map[string]ports.Block{}
	for _, b := range allBlocks {
		blockByKey[b.Repo+"|"+b.Slug] = b
	}

	var rows []statusRow
	for _, proj := range projects {
		if statusProject != "" && proj.Name != statusProject {
			continue
		}
		wts, err := git.ListWorktrees(proj.Path)
		if err != nil {
			continue
		}
		mainBranch := proj.MainBranch
		if mainBranch == "" {
			mainBranch = "main"
		}
		// Same cascade as rm/finish: prefer origin/<main> only when the
		// remote-tracking ref actually exists. Without this guard, CommitsAhead
		// against a missing "origin/<main>" errors and we silently report 0,
		// telling the user every worktree is in sync when it isn't.
		hasOrigin := git.HasOrigin(proj.Path)
		var aheadRef string
		switch {
		case hasOrigin && git.HasRemoteBranch(proj.Path, "origin", mainBranch):
			aheadRef = "origin/" + mainBranch
		case git.HasBranch(proj.Path, mainBranch):
			aheadRef = mainBranch
		}
		for _, wt := range wts {
			isMain := samePath(wt.Path, proj.Path)
			slug := ""
			if !isMain {
				slug = filepath.Base(wt.Path)
			}
			dirty, _ := git.DirtyCount(wt.Path)
			ahead := 0
			behind := 0
			aheadKnown := aheadRef != ""
			if !isMain && aheadKnown {
				ahead, _ = git.CommitsAhead(wt.Path, aheadRef)
				behind, _ = git.CommitsBehind(wt.Path, aheadRef)
			}
			_ = aheadKnown
			age := int64(0)
			if info, err := os.Stat(wt.Path); err == nil {
				age = int64(time.Since(info.ModTime()).Seconds())
			}
			row := statusRow{
				Project:    proj.Name,
				Slug:       slug,
				Path:       wt.Path,
				Branch:     wt.Branch,
				IsMain:     isMain,
				Ahead:      ahead,
				Behind:     behind,
				AheadKnown: aheadKnown,
				Dirty:      dirty,
				AgeSeconds: age,
			}
			if blk, ok := blockByKey[proj.Path+"|"+slug]; ok {
				row.PortStart = blk.Start
				row.PortEnd = blk.End()
			}
			rows = append(rows, row)
		}
	}

	if statusJSON {
		return emitJSON(true, statusResult{Worktrees: rows})
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tSLUG\tBRANCH\tAHEAD\tBEHIND\tDIRTY\tPORTS\tAGE")
	for _, r := range rows {
		slug := r.Slug
		if r.IsMain {
			slug = "(main)"
		}
		ports := "-"
		if r.PortStart > 0 {
			ports = fmt.Sprintf("%d-%d", r.PortStart, r.PortEnd)
		}
		// Mark ahead/behind as "?" when no main reference resolved — the
		// R4 fix for "status silently reports 0/0 and lies".
		aheadCol := fmt.Sprintf("%d", r.Ahead)
		behindCol := fmt.Sprintf("%d", r.Behind)
		if !r.IsMain && !r.AheadKnown {
			aheadCol = "?"
			behindCol = "?"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			r.Project, slug, r.Branch, aheadCol, behindCol, r.Dirty, ports, humanizeAge(r.AgeSeconds))
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stdout, "no worktrees")
	}
	return w.Flush()
}

// samePath compares two paths after resolving symlinks so that macOS's
// /var -> /private/var (and similar) does not cause false-negative matches.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	if ea == nil && eb == nil {
		return ra == rb
	}
	return false
}

// humanizeAge turns a seconds count into a short string (1s, 5m, 3h, 2d).
func humanizeAge(s int64) string {
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm", s/60)
	}
	if s < 86400 {
		return fmt.Sprintf("%dh", s/3600)
	}
	return fmt.Sprintf("%dd", s/86400)
}

// suppress unused-import warning for strings on platforms where every used
// function happens to live elsewhere.
var _ = strings.Builder{}
