package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Worktree is a single registered git worktree.
type Worktree struct {
	Path     string
	Branch   string // short branch name, or "" if detached
	Detached bool
}

// ListWorktrees parses `git worktree list --porcelain` for the given repo.
func ListWorktrees(repo string) ([]Worktree, error) {
	out, err := Run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var (
		wts []Worktree
		cur Worktree
	)
	flush := func() {
		if cur.Path != "" {
			wts = append(wts, cur)
		}
		cur = Worktree{}
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Detached = true
		}
	}
	flush()
	return wts, nil
}

// HasBranch returns true if the local branch exists in the given repo.
func HasBranch(repo, branch string) bool {
	_, err := Run(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// CurrentBranch returns the branch checked out in the given worktree, or "" if detached.
func CurrentBranch(wt string) (string, error) {
	return Run(wt, "branch", "--show-current")
}

// DirtyCount returns the number of changed + untracked files in the given worktree.
func DirtyCount(wt string) (int, error) {
	out, err := Run(wt, "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	if out == "" {
		return 0, nil
	}
	return strings.Count(out, "\n") + 1, nil
}

// AddWorktreeNewBranchFrom creates a new worktree at path on a new branch
// forked from baseRef (e.g. "main", "origin/main", a SHA). Callers always
// know which commit their work starts from instead of inheriting whatever
// the main checkout happens to be on.
//
// The unscoped "branch from current HEAD" variant was removed in v0.6.2 —
// it was a footgun that caused fork branches to inherit dev-only commits
// when the main checkout was on the wrong branch. If you really need
// "current HEAD" semantics, pass "HEAD" explicitly as baseRef.
func AddWorktreeNewBranchFrom(repo, path, branch, baseRef string) error {
	return RunStream(repo, "worktree", "add", "-b", branch, path, baseRef)
}

// HasRemoteBranch returns true if refs/remotes/<remote>/<branch> exists.
// Use this instead of bare HasRef("origin/main") — `git rev-parse --verify
// origin/main` would also match a tag named "origin/main" or a local branch
// named "origin/main" in some setups.
func HasRemoteBranch(repo, remote, branch string) bool {
	_, err := Run(repo, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	return err == nil
}

// AddWorktreeExistingBranch creates a new worktree at path checking out an existing branch.
func AddWorktreeExistingBranch(repo, path, branch string) error {
	return RunStream(repo, "worktree", "add", path, branch)
}

// RemoveWorktree removes a registered worktree.
func RemoveWorktree(repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	return RunStream(repo, args...)
}

// CommitsAhead returns the number of commits on HEAD that are ahead of the given refspec.
func CommitsAhead(wt, refspec string) (int, error) {
	out, err := Run(wt, "rev-list", "--count", refspec+"..HEAD")
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscanf(out, "%d", &n); err != nil {
		return 0, fmt.Errorf("parse rev-list count %q: %w", out, err)
	}
	return n, nil
}

// CommitsBehind returns the number of commits on the given refspec that are ahead of HEAD.
func CommitsBehind(wt, refspec string) (int, error) {
	out, err := Run(wt, "rev-list", "--count", "HEAD.."+refspec)
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscanf(out, "%d", &n); err != nil {
		return 0, fmt.Errorf("parse rev-list count %q: %w", out, err)
	}
	return n, nil
}

// ChangedFiles returns the union of:
//  1. files committed on HEAD that are ahead of refspec, and
//  2. files modified or untracked in the worktree (status --porcelain).
//
// Used by edit-prediction to surface which files are "in play" in a given worktree.
func ChangedFiles(wt, refspec string) ([]string, error) {
	set := map[string]struct{}{}
	// 1. committed-since-fork
	if out, err := Run(wt, "diff", "--name-only", refspec+"...HEAD"); err == nil && out != "" {
		for _, line := range strings.Split(out, "\n") {
			if line != "" {
				set[line] = struct{}{}
			}
		}
	}
	// 2. uncommitted (modified, added, untracked — exclude deleted)
	if out, err := Run(wt, "status", "--porcelain"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if len(line) < 4 {
				continue
			}
			// Format: "XY path" or "XY orig -> new"
			path := strings.TrimSpace(line[3:])
			if idx := strings.LastIndex(path, " -> "); idx >= 0 {
				path = strings.TrimSpace(path[idx+4:])
			}
			if path != "" {
				set[path] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out, nil
}

// HasOrigin returns true if an "origin" remote is configured in the repo.
func HasOrigin(repo string) bool {
	_, err := Run(repo, "remote", "get-url", "origin")
	return err == nil
}

// EnsureLocalExclude appends a pattern to .git/info/exclude if it is not
// already present. info/exclude is local-only (not committed), so we can
// silently keep the worktree directory out of `git status` without touching
// the project's tracked .gitignore.
func EnsureLocalExclude(repo, pattern string) error {
	gitDir, err := Run(repo, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repo, gitDir)
	}
	excludePath := filepath.Join(gitDir, "info", "exclude")
	data, _ := os.ReadFile(excludePath)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		data = append(data, '\n')
	}
	data = append(data, []byte(pattern+"\n")...)
	return os.WriteFile(excludePath, data, 0o644)
}
