package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecureJoin_Ok(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ rel string }{
		{".env"},
		{"config/local.json"},
		{"./a/b"},
		{"a/./b/../c"}, // ends as a/c
		{"node_modules"},
	}
	for _, c := range cases {
		got, err := secureJoin(root, c.rel)
		if err != nil {
			t.Errorf("secureJoin(%q, %q): unexpected error %v", root, c.rel, err)
		}
		if !strings.HasPrefix(got, root) {
			t.Errorf("secureJoin(%q, %q) = %q, expected prefix %q", root, c.rel, got, root)
		}
	}
}

func TestSecureJoin_Rejects(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name, rel string
	}{
		{"absolute unix", "/etc/passwd"},
		{"absolute root", "/"},
		{"parent traversal", "../secret"},
		{"deep parent traversal", "../../../etc/passwd"},
		{"cleaned to parent", "a/../../escape"},
		{"empty", ""},
		{"single dotdot", ".."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := secureJoin(root, c.rel)
			if err == nil {
				t.Errorf("secureJoin(%q, %q) accepted unsafe input", root, c.rel)
			}
		})
	}
}

func TestSecureJoin_AllowsInternalDotDot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := secureJoin(root, "a/b/../c")
	if err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	want := filepath.Join(root, "a", "c")
	if got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}

// REGRESSION (P0 round 2 #1): a tracked symlink inside the source root that
// points OUTSIDE must be rejected when used as a hook source path.
// Before fix: copyFile would follow it via os.ReadFile and leak the outside
// file. After fix: secureJoin rejects on Lstat -> symlink -> EvalSymlinks
// escapes root.
func TestSecureJoin_RejectsTrackedSymlinkEscapingRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside_link")); err != nil {
		t.Fatal(err)
	}
	_, err := secureJoin(root, "outside_link")
	if err == nil {
		t.Errorf("expected refusal: source symlink escapes root")
	}
	if err != nil && !strings.Contains(err.Error(), "escapes root") {
		t.Errorf("expected 'escapes root' in error, got %v", err)
	}
}

// REGRESSION (P0 round 2 #2): an ancestor of the destination path is a
// symlink pointing OUTSIDE root. Writing through it would land outside.
// Before fix: os.WriteFile silently followed the ancestor symlink.
// After fix: secureJoin walks ancestors and rejects symlinks escaping root.
func TestSecureJoin_RejectsAncestorSymlinkEscapingRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(root, "mainlink")); err != nil {
		t.Fatal(err)
	}
	_, err := secureJoin(root, "mainlink/PWNED.txt")
	if err == nil {
		t.Errorf("expected refusal: ancestor symlink escapes root")
	}
	if err != nil && !strings.Contains(err.Error(), "escapes root") {
		t.Errorf("expected 'escapes root' in error, got %v", err)
	}
}

func TestSecureJoin_AllowsInternalSymlink(t *testing.T) {
	// A symlink whose target is inside root is fine — we copy the resolved
	// content. This keeps legitimate refactor-symlinks working.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := secureJoin(root, "alias"); err != nil {
		t.Errorf("internal symlink should be allowed, got %v", err)
	}
}

func TestSecureJoin_DanglingSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(t.TempDir(), "does-not-exist"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if _, err := secureJoin(root, "dangling"); err == nil {
		t.Errorf("dangling symlink should be rejected")
	}
}

func TestWithinRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "sub")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if !withinRoot(root, inside) {
		t.Error("inside should be within root")
	}
	if !withinRoot(root, root) {
		t.Error("root should be within itself")
	}
	// Symlink escaping root.
	escapeTarget := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(escapeTarget, link); err != nil {
		t.Fatal(err)
	}
	if withinRoot(root, link) {
		t.Error("symlink escaping root should NOT be reported as within")
	}
}
