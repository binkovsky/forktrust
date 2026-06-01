package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// envLocalManagedHeader is the exact first line that forktrust writes into
// every .env.local it generates (see ports.ManagedHeader — must stay in sync).
// We duplicate it here rather than import internal/ports to avoid an import
// cycle (git is a leaf package; ports imports pathsafe but not git).
// If you change the header in ports/envfile.go, update this too.
const envLocalManagedHeader = "# Managed by forktrust. Do not edit; values are overwritten on each `forktrust new`.\n"

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
// NOTE: git status --porcelain does NOT include ignored files; use IgnoredCount
// separately before removing a worktree to avoid silently losing them.
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

// IgnoredCount returns the number of ignored files in the given worktree that
// would be silently deleted by `git worktree remove`. It skips files that are
// provably forktrust-managed (i.e. start with the forktrust marker line), so
// a user-authored .env.local that is also .gitignore'd is correctly counted.
//
// Uses `git ls-files --others --ignored --exclude-standard`. Returns (0, nil)
// on git errors for graceful degradation.
func IgnoredCount(wt string) (int, error) {
	out, err := Run(wt, "ls-files", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return 0, nil
	}
	if out == "" {
		return 0, nil
	}
	count := 0
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		// Skip only the root-level .env.local that forktrust itself generates.
		// Two conditions must both hold:
		//   1. The path must be exactly ".env.local" (not foo/.env.local or
		//      any nested variant) — forktrust only writes to the worktree root.
		//   2. The file content must begin with the exact ManagedHeader line
		//      (including trailing newline) — not just a matching prefix — so a
		//      user file that starts with "# Managed by forktrust" but continues
		//      differently is NOT treated as ours.
		if filepath.Clean(line) == ".env.local" && isForktrustManaged(filepath.Join(wt, line)) {
			continue
		}
		count++
	}
	return count, nil
}

// isForktrustManaged reports whether the file at path was written by forktrust.
// It returns true IFF the file's content begins with the EXACT envLocalManagedHeader
// string (the full first line including the trailing newline). A user file that
// merely starts with "# Managed by forktrust" but has a different continuation
// (e.g. "# Managed by forktrust but actually mine") returns false.
func isForktrustManaged(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(envLocalManagedHeader))
	n, _ := f.Read(buf)
	return string(buf[:n]) == envLocalManagedHeader
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

// ShortSHA returns the 7-character abbreviated commit SHA for HEAD in the
// given worktree/repo. Used to build unique wip/* branch names that survive
// same-second rm invocations: YYYYMMDD-HHMMSS alone is not unique when two
// consecutive rm runs happen within the same wall-clock second. The SHA is
// derived from the actual tip commit (after any WIP auto-commit in runRm),
// so it is guaranteed unique across all branch states.
// Returns empty string on any git error so callers can fall back gracefully.
func ShortSHA(repo string) string {
	out, err := Run(repo, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
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
