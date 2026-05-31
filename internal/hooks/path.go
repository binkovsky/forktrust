package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// secureJoin joins root with a user-supplied relative path, refusing any
// input that would escape root via:
//
//  1. an absolute path (Linux: starts with "/"; Windows: drive or UNC)
//  2. parent-directory traversal (".." segments) after filepath.Clean
//  3. an EXISTING component along the path that is a symlink whose resolved
//     target falls outside root (runtime escape, not visible in the text)
//
// Non-existent suffix components are allowed (so we can compute destination
// paths that haven't been created yet). EXISTING components are validated.
//
// This is the entry point for sanitizing .forktrustconfig hook From/To paths
// so neither lexical "../../etc/passwd" nor runtime tricks like a tracked
// symlink "outside_link -> ../secret" can read or write outside the intended
// root (worktree for "to", main checkout for "from").
//
// Returns the joined path on success.
func secureJoin(root, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative (not absolute)", rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes root via parent traversal", rel)
	}
	if strings.HasPrefix(cleaned, `..\`) {
		return "", fmt.Errorf("path %q escapes root via parent traversal", rel)
	}
	joined := filepath.Join(root, cleaned)

	// Runtime symlink check. Resolve root once; walk each component of the
	// cleaned relative path, Lstat-ing it. If an existing component is a
	// symlink, its EvalSymlinks-ed target must still be inside the resolved
	// root.
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		// If root itself doesn't exist (e.g. dest dir not yet created), we
		// have nothing to walk against — the lexical check above is the
		// only protection we can offer. This is acceptable because callers
		// pass real existing roots (worktree path, main checkout path).
		return joined, nil
	}

	cur := root
	parts := strings.Split(cleaned, string(filepath.Separator))
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		cur = filepath.Join(cur, p)
		info, err := os.Lstat(cur)
		if err != nil {
			// Doesn't exist — subsequent components also won't exist;
			// nothing more to check.
			break
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		// It's a symlink. Resolve and confirm target stays inside root.
		real, err := filepath.EvalSymlinks(cur)
		if err != nil {
			return "", fmt.Errorf("symlink at %q is dangling or unresolvable: %w", cur, err)
		}
		relReal, err := filepath.Rel(rootReal, real)
		if err != nil || relReal == ".." || strings.HasPrefix(relReal, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path %q passes through symlink %q -> %q which escapes root %q",
				rel, cur, real, rootReal)
		}
	}
	return joined, nil
}

// withinRoot reports whether `p` (after symlink resolution) is the same as or
// a descendant of `root` (after symlink resolution). Used by copyDir's walk
// callback to skip per-file symlinks that escape the source directory.
func withinRoot(root, p string) bool {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	pReal, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootReal, pReal)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
