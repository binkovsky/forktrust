package cli

import (
	"fmt"
	"strings"

	"github.com/binkovsky/forktrust/internal/git"
	"github.com/binkovsky/forktrust/internal/scope"
)

// truncateStrings returns the first n elements of s, used to cap how many
// scope violations appear in JSON output (full count stays in a separate field).
func truncateStrings(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	out := make([]string, n)
	copy(out, s[:n])
	return out
}

// scopeCheckResult is the structured outcome of evaluating a worktree's diff
// against its declared scope. Same shape across runFinish, previewFinish, and
// the standalone `forktrust scope <slug> --check` command.
type scopeCheckResult struct {
	Configured bool
	Ran        bool
	Passed     bool
	Allowed    []string
	Violations []string
	// Total count may exceed len(Violations) when JSON output truncates the list.
	ViolationCount int
}

// evalScope runs the change-contract check for a worktree:
//  1. Load <repo>/.forktrust/scopes/<slug>.toml. Absent → Configured=false, treated as "no restrictions" (backwards-compat).
//  2. Compute changed files = `git diff --name-only <aheadRef>` in the worktree.
//     This covers BOTH committed-ahead and uncommitted changes (working tree
//     vs aheadRef), so users get the full picture without first committing.
//  3. Match changed files against scope.Allowed via doublestar.
//
// Returns a populated scopeCheckResult. Errors only come from the git/scope
// load — a violation is not an error, it's signalled via Violations.
func evalScope(repoPath, wtPath, slug, aheadRef string) (scopeCheckResult, error) {
	r := scopeCheckResult{}

	sc, err := scope.Load(repoPath, slug)
	if err != nil {
		return r, fmt.Errorf("load scope: %w", err)
	}
	if sc == nil {
		// No scope file = backwards-compat "everything allowed".
		return r, nil
	}
	r.Configured = true
	r.Allowed = sc.Allowed
	r.Ran = true

	changed, err := changedFilesAgainst(wtPath, aheadRef)
	if err != nil {
		return r, fmt.Errorf("compute diff: %w", err)
	}

	r.Violations = scope.Check(sc.Allowed, changed)
	r.ViolationCount = len(r.Violations)
	r.Passed = r.ViolationCount == 0
	return r, nil
}

// changedFilesAgainst returns the union of:
//   - tracked files differing between aheadRef and the worktree's HEAD
//   - tracked files differing between HEAD and the working tree (uncommitted)
//   - untracked files (excluding ignored — those have their own guard)
//
// Repo-relative, forward-slash, deduplicated, in deterministic order.
func changedFilesAgainst(wtPath, aheadRef string) ([]string, error) {
	seen := map[string]struct{}{}
	var ordered []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		ordered = append(ordered, path)
	}

	// Committed-ahead diff vs the aheadRef target. Empty aheadRef ⇒ caller
	// should not be calling us; skip safely.
	if aheadRef != "" {
		out, err := git.Run(wtPath, "diff", "--name-only", aheadRef+"...HEAD")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(out, "\n") {
			add(line)
		}
	}

	// Uncommitted changes (staged + unstaged).
	out, err := git.Run(wtPath, "diff", "--name-only", "HEAD")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			add(line)
		}
	}

	// Untracked files NOT in .gitignore (ignored files have their own
	// exit-14 guard; we don't double-count them here).
	out, err = git.Run(wtPath, "ls-files", "--others", "--exclude-standard")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			add(line)
		}
	}

	return ordered, nil
}
