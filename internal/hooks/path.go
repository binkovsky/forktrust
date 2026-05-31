package hooks

import (
	"fmt"
	"path/filepath"
	"strings"
)

// secureJoin joins root with a user-supplied relative path, refusing any
// input that would escape root via:
//   - an absolute path (Linux: starts with "/"; Windows: drive or UNC)
//   - parent-directory traversal (".." segments) after filepath.Clean
//
// This is the entry point for sanitizing .forktrustconfig hook From/To paths
// so a malicious or accidental "../../etc/passwd" cannot read or write
// outside the intended root (worktree for "to", main checkout for "from").
//
// Returns the joined absolute-style path on success.
func secureJoin(root, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative (not absolute)", rel)
	}
	cleaned := filepath.Clean(rel)
	// After Clean, ".." escape is visible as ".." prefix or full equality.
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes root via parent traversal", rel)
	}
	// Windows separator safety (no-op on POSIX, harmless on Windows).
	if strings.HasPrefix(cleaned, `..\`) {
		return "", fmt.Errorf("path %q escapes root via parent traversal", rel)
	}
	return filepath.Join(root, cleaned), nil
}
