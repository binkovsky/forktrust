package ports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteEnv_RefusesSymlinkTarget is the regression for the v0.6.1 P0 bug:
// a tracked .env.local symlinking outside the worktree caused os.WriteFile
// to follow the link and write a forktrust-managed file at an attacker-
// controlled location. WriteEnv must Lstat first and refuse symlinks.
func TestWriteEnv_RefusesSymlinkTarget(t *testing.T) {
	wt := t.TempDir()
	outside := filepath.Join(t.TempDir(), "pwned.txt")
	// Symlink the env target so a naive WriteFile would land outside.
	if err := os.Symlink(outside, filepath.Join(wt, EnvFileName)); err != nil {
		t.Fatal(err)
	}
	err := WriteEnv(wt, Block{Start: 3000, Size: 10}, []string{"PORT"})
	if err == nil {
		t.Fatal("expected refusal; got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got %q", err.Error())
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("WriteEnv wrote through symlink to %s — bypass succeeded", outside)
	}
}

func TestWriteEnv_RefusesSymlinkEvenIfTargetExists(t *testing.T) {
	// Same bug, more aggressive: target exists with a forktrust-marker so
	// the existing-file check could be tempted to allow overwrite. The
	// symlink check must run FIRST.
	wt := t.TempDir()
	outside := filepath.Join(t.TempDir(), "pwned.txt")
	if err := os.WriteFile(outside, []byte("# Managed by forktrust\nFAKE=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(wt, EnvFileName)); err != nil {
		t.Fatal(err)
	}
	err := WriteEnv(wt, Block{Start: 3000, Size: 10}, []string{"PORT"})
	if err == nil {
		t.Fatal("expected refusal even with marker; got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got %q", err.Error())
	}
	// Content of outside file must not be the new render.
	data, _ := os.ReadFile(outside)
	if strings.Contains(string(data), "PORT=3000") {
		t.Errorf("WriteEnv updated symlink target — bypass succeeded")
	}
}
