package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binkovsky/forktrust/internal/config"
)

func tempWorktreePair(t *testing.T) (mainPath, wtPath string) {
	t.Helper()
	mainPath = t.TempDir()
	wtPath = t.TempDir()
	return
}

func TestRun_Nil(t *testing.T) {
	results, err := Run(nil, Context{}, os.Stderr, os.Stderr)
	if err != nil {
		t.Errorf("Run(nil) errored: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestRun_CopyFile(t *testing.T) {
	main, wt := tempWorktreePair(t)
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("SECRET=42"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCopy, From: ".env", To: ".env"},
	}}}
	results, err := Run(cfg, Context{MainPath: main, Path: wt}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if results[0].Err != nil || results[0].Skipped {
		t.Errorf("expected success, got %+v", results[0])
	}
	data, err := os.ReadFile(filepath.Join(wt, ".env"))
	if err != nil {
		t.Fatalf("read copied: %v", err)
	}
	if string(data) != "SECRET=42" {
		t.Errorf("content mismatch: %q", data)
	}
}

func TestRun_CopyMissingSourceSkips(t *testing.T) {
	main, wt := tempWorktreePair(t)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCopy, From: "nope.txt", To: "nope.txt"},
	}}}
	results, err := Run(cfg, Context{MainPath: main, Path: wt}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !results[0].Skipped {
		t.Errorf("expected skipped for missing source, got %+v", results[0])
	}
}

func TestRun_CopyDir(t *testing.T) {
	main, wt := tempWorktreePair(t)
	subdir := filepath.Join(main, "shared")
	_ = os.MkdirAll(subdir, 0o755)
	_ = os.WriteFile(filepath.Join(subdir, "a.txt"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(subdir, "b.txt"), []byte("b"), 0o644)

	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCopy, From: "shared", To: "shared"},
	}}}
	_, err := Run(cfg, Context{MainPath: main, Path: wt}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(wt, "shared", name)); err != nil {
			t.Errorf("missing copied %s: %v", name, err)
		}
	}
}

func TestRun_SymlinkGitignoredDir(t *testing.T) {
	main, wt := tempWorktreePair(t)
	nm := filepath.Join(main, "node_modules", "lodash")
	_ = os.MkdirAll(nm, 0o755)
	_ = os.WriteFile(filepath.Join(nm, "index.js"), []byte("// stub"), 0o644)

	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookSymlink, From: "node_modules", To: "node_modules"},
	}}}
	_, err := Run(cfg, Context{MainPath: main, Path: wt}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	target := filepath.Join(wt, "node_modules")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink, got %v", info.Mode())
	}
	// Symlink should resolve and the file should be readable through it.
	if _, err := os.ReadFile(filepath.Join(target, "lodash", "index.js")); err != nil {
		t.Errorf("read through symlink: %v", err)
	}
}

func TestRun_SymlinkSkipsNonEmptyTrackedDir(t *testing.T) {
	main, wt := tempWorktreePair(t)
	_ = os.MkdirAll(filepath.Join(main, "shared"), 0o755)
	_ = os.WriteFile(filepath.Join(main, "shared", "x.txt"), []byte("x"), 0o644)
	// Simulate git worktree populating dst with the tracked content already.
	_ = os.MkdirAll(filepath.Join(wt, "shared"), 0o755)
	_ = os.WriteFile(filepath.Join(wt, "shared", "x.txt"), []byte("x"), 0o644)

	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookSymlink, From: "shared", To: "shared"},
	}}}
	results, err := Run(cfg, Context{MainPath: main, Path: wt}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run errored on non-empty target (should skip): %v", err)
	}
	if !results[0].Skipped {
		t.Errorf("expected skipped for non-empty target, got %+v", results[0])
	}
	// Tracked file must still be intact — we MUST NOT destroy it.
	data, err := os.ReadFile(filepath.Join(wt, "shared", "x.txt"))
	if err != nil || string(data) != "x" {
		t.Errorf("tracked file destroyed by symlink hook: %v, %q", err, data)
	}
}

func TestRun_CommandTemplateExpansion(t *testing.T) {
	main, wt := tempWorktreePair(t)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCommand, Run: "echo slug={{.Slug}} branch={{.Branch}} > out.txt"},
	}}}
	var stderr bytes.Buffer
	_, err := Run(cfg, Context{
		Branch: "fork/x", Slug: "x", Path: wt, MainPath: main, Project: "p",
	}, &stderr, &stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wt, "out.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != "slug=x branch=fork/x" {
		t.Errorf("template expansion: got %q", got)
	}
}

func TestRun_CommandMissingFieldErrors(t *testing.T) {
	main, wt := tempWorktreePair(t)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCommand, Run: "echo {{.DoesNotExist}}"},
	}}}
	results, err := Run(cfg, Context{Path: wt, MainPath: main}, os.Stderr, os.Stderr)
	if err == nil {
		t.Errorf("expected error for missing template field, got nil")
	}
	if len(results) == 0 || results[0].Err == nil {
		t.Errorf("expected hook result with error, got %+v", results)
	}
}

func TestRun_CommandWorkDir(t *testing.T) {
	main, wt := tempWorktreePair(t)
	_ = os.MkdirAll(filepath.Join(wt, "sub"), 0o755)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCommand, Run: "pwd > here.txt", WorkDir: "sub"},
	}}}
	_, err := Run(cfg, Context{Path: wt, MainPath: main}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wt, "sub", "here.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := strings.TrimSpace(string(data))
	// On macOS /private/var symlinks resolve, so just check suffix.
	if !strings.HasSuffix(got, "/sub") {
		t.Errorf("expected pwd to end in /sub, got %q", got)
	}
}

func TestRun_StopsAtFirstError(t *testing.T) {
	main, wt := tempWorktreePair(t)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCommand, Run: "exit 1"},                   // fails
		{Type: config.HookCommand, Run: "touch should-not-run.txt"}, // skipped
	}}}
	results, err := Run(cfg, Context{Path: wt, MainPath: main}, os.Stderr, os.Stderr)
	if err == nil {
		t.Errorf("expected error from failing first hook")
	}
	if len(results) != 1 {
		t.Errorf("expected only 1 result before stopping, got %d", len(results))
	}
	if _, err := os.Stat(filepath.Join(wt, "should-not-run.txt")); err == nil {
		t.Errorf("second hook ran after first failed")
	}
}
