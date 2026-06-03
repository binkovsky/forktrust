package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
	"github.com/binkovsky/forktrust/internal/scope"
)

var (
	scopeProject string
	scopeSet     string
	scopeClear   bool
	scopeCheck   bool
	scopeJSON    bool
)

var scopeCmd = &cobra.Command{
	Use:   "scope <slug>",
	Short: "Show, set, or check the change-contract scope for a worktree",
	Long: `Manage the per-worktree change contract — a list of glob patterns the task
is allowed to modify. forktrust finish refuses to merge if the diff touches
files outside these globs (exit 16).

The scope is stored at <repo>/.forktrust/scopes/<slug>.toml.

Modes (mutually exclusive):
  forktrust scope <slug>                 show current scope (or "no scope set")
  forktrust scope <slug> --set "a/**, b/**"   replace the scope
  forktrust scope <slug> --clear                remove the scope (no restrictions)
  forktrust scope <slug> --check                evaluate diff vs scope; exit 16 if any violation

Exit codes:
  0   success (show / set / clear / clean check)
  6   no worktree matching slug
  7   slug matches worktrees in multiple projects
  16  --check found out-of-scope edits`,
	Args: cobra.ExactArgs(1),
	RunE: runScope,
}

func init() {
	scopeCmd.Flags().StringVarP(&scopeProject, "project", "p", "", "target project name (required if more than one is registered)")
	scopeCmd.Flags().StringVar(&scopeSet, "set", "", "replace the scope with this comma-separated glob list")
	scopeCmd.Flags().BoolVar(&scopeClear, "clear", false, "remove the scope file (worktree has no restrictions)")
	scopeCmd.Flags().BoolVar(&scopeCheck, "check", false, "evaluate the worktree diff against the scope and exit 16 on violation")
	scopeCmd.Flags().BoolVar(&scopeJSON, "json", false, "emit a structured JSON object on stdout")
}

// scopeShowResult is the JSON shape for the show / set / check modes.
type scopeShowResult struct {
	Project             string   `json:"project"`
	Slug                string   `json:"slug"`
	Configured          bool     `json:"configured"`
	Allowed             []string `json:"allowed,omitempty"`
	CreatedBy           string   `json:"created_by,omitempty"`
	CreatedAt           string   `json:"created_at,omitempty"`
	Checked             bool     `json:"checked"`
	Passed              bool     `json:"passed"`
	Violations          []string `json:"violations,omitempty"`
	ViolationCount      int      `json:"violation_count,omitempty"`
	Action              string   `json:"action,omitempty"` // "show" | "set" | "clear" | "check"
}

func runScope(_ *cobra.Command, args []string) error {
	slug := args[0]

	// Enforce mutually exclusive modes.
	modes := 0
	if scopeSet != "" {
		modes++
	}
	if scopeClear {
		modes++
	}
	if scopeCheck {
		modes++
	}
	if modes > 1 {
		return fmt.Errorf("--set, --clear, --check are mutually exclusive")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	proj, wtPath, err := resolveWorktree(cfg, scopeProject, slug)
	if err != nil {
		return err
	}

	r := scopeShowResult{Project: proj.Name, Slug: slug}

	switch {
	case scopeSet != "":
		r.Action = "set"
		globs := scope.ParseCSV(scopeSet)
		s := &scope.Scope{
			Allowed:   globs,
			CreatedBy: "forktrust scope " + slug + " --set " + scopeSet,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := scope.Save(proj.Path, slug, s); err != nil {
			return err
		}
		r.Configured = true
		r.Allowed = globs
		r.CreatedBy = s.CreatedBy
		r.CreatedAt = s.CreatedAt
		return emitScope(r, "scope updated")

	case scopeClear:
		r.Action = "clear"
		if err := scope.Remove(proj.Path, slug); err != nil {
			return err
		}
		r.Configured = false
		return emitScope(r, "scope cleared")

	case scopeCheck:
		r.Action = "check"
		mainBranch := proj.MainBranch
		if mainBranch == "" {
			mainBranch = "main"
		}
		hasOrigin := git.HasOrigin(proj.Path)
		aheadRef := ""
		switch {
		case hasOrigin && git.HasRemoteBranch(proj.Path, "origin", mainBranch):
			aheadRef = "origin/" + mainBranch
		case git.HasBranch(proj.Path, mainBranch):
			aheadRef = mainBranch
		}
		if aheadRef == "" {
			return coded(ExitAheadUnknown, fmt.Errorf("no main reference resolved; cannot compute diff for scope check"))
		}
		scopeR, err := evalScope(proj.Path, wtPath, slug, aheadRef)
		if err != nil {
			return err
		}
		r.Configured = scopeR.Configured
		r.Allowed = scopeR.Allowed
		r.Checked = scopeR.Ran
		r.Passed = scopeR.Passed
		r.Violations = truncateStrings(scopeR.Violations, 100)
		r.ViolationCount = scopeR.ViolationCount
		if !scopeR.Configured {
			return emitScope(r, "no scope set; nothing to check")
		}
		if !scopeR.Passed {
			_ = emitScope(r, "")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "REFUSE: %d file(s) outside declared scope:\n", scopeR.ViolationCount)
			for _, v := range r.Violations {
				fmt.Fprintf(os.Stderr, "  - %s\n", v)
			}
			if scopeR.ViolationCount > len(r.Violations) {
				fmt.Fprintf(os.Stderr, "  ... and %d more\n", scopeR.ViolationCount-len(r.Violations))
			}
			return coded(ExitScopeViolation, fmt.Errorf("%d file(s) outside scope", scopeR.ViolationCount))
		}
		return emitScope(r, "scope satisfied")

	default:
		// Show.
		r.Action = "show"
		s, err := scope.Load(proj.Path, slug)
		if err != nil {
			return err
		}
		if s == nil {
			return emitScope(r, "no scope set (worktree has no restrictions)")
		}
		r.Configured = true
		r.Allowed = s.Allowed
		r.CreatedBy = s.CreatedBy
		r.CreatedAt = s.CreatedAt
		return emitScope(r, "")
	}
}

// emitScope writes either JSON (when --json) or a human-friendly description.
// The optional headline is printed above the human details (and ignored in JSON mode).
func emitScope(r scopeShowResult, headline string) error {
	if scopeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	if headline != "" {
		fmt.Println(headline)
	}
	fmt.Printf("project: %s\n", r.Project)
	fmt.Printf("slug:    %s\n", r.Slug)
	if !r.Configured {
		return nil
	}
	fmt.Println("scope:")
	for _, g := range r.Allowed {
		fmt.Printf("  - %s\n", g)
	}
	if r.CreatedBy != "" || r.CreatedAt != "" {
		fmt.Println("metadata:")
		if r.CreatedBy != "" {
			fmt.Printf("  created_by: %s\n", r.CreatedBy)
		}
		if r.CreatedAt != "" {
			fmt.Printf("  created_at: %s\n", r.CreatedAt)
		}
	}
	if r.Checked {
		if r.Passed {
			fmt.Println("check:   PASS — diff stays within scope")
		} else {
			fmt.Printf("check:   FAIL — %d file(s) outside scope\n", r.ViolationCount)
			if len(r.Violations) > 0 && !scopeJSON {
				// Already printed to stderr by the caller; mirror first few on stdout for readability.
				maxShow := len(r.Violations)
				if maxShow > 5 {
					maxShow = 5
				}
				for _, v := range r.Violations[:maxShow] {
					fmt.Printf("           - %s\n", v)
				}
			}
		}
	}
	_ = strings.TrimSpace // keep import used in case of future formatting
	return nil
}
