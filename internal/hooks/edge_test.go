package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binkovsky/forktrust/internal/config"
)

func TestRun_CommandEnvVarsExpanded(t *testing.T) {
	main, wt := tempWorktreePair(t)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{{
		Type: config.HookCommand,
		Run:  `echo $MY_VAR > out.txt`,
		Env:  map[string]string{"MY_VAR": "slug={{.Slug}}"},
	}}}}
	_, err := Run(cfg, Context{Slug: "abc", Path: wt, MainPath: main}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(wt, "out.txt"))
	got := strings.TrimSpace(string(data))
	if got != "slug=abc" {
		t.Errorf("env var template expansion: got %q want %q", got, "slug=abc")
	}
}

func TestRun_CopyPreservesFileMode(t *testing.T) {
	main, wt := tempWorktreePair(t)
	src := filepath.Join(main, "script.sh")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCopy, From: "script.sh", To: "script.sh"},
	}}}
	_, err := Run(cfg, Context{MainPath: main, Path: wt}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(wt, "script.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("copy did not preserve execute bit: mode=%v", info.Mode())
	}
}

func TestRun_SymlinkAbsoluteResolution(t *testing.T) {
	// The symlink we create must be an absolute path pointing back to the
	// main worktree — relative symlinks break when cd-ing around.
	main, wt := tempWorktreePair(t)
	src := filepath.Join(main, "node_modules")
	_ = os.MkdirAll(src, 0o755)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookSymlink, From: "node_modules", To: "node_modules"},
	}}}
	_, err := Run(cfg, Context{MainPath: main, Path: wt}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(wt, "node_modules"))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(target) {
		t.Errorf("symlink target should be absolute, got %q", target)
	}
}

func TestRun_CopyHandlesNestedDestDirs(t *testing.T) {
	main, wt := tempWorktreePair(t)
	src := filepath.Join(main, "deep", "config.yml")
	_ = os.MkdirAll(filepath.Dir(src), 0o755)
	_ = os.WriteFile(src, []byte("x: 1"), 0o644)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCopy, From: "deep/config.yml", To: "deep/config.yml"},
	}}}
	results, err := Run(cfg, Context{MainPath: main, Path: wt}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if results[0].Err != nil {
		t.Errorf("expected ok, got %+v", results[0])
	}
	if _, err := os.Stat(filepath.Join(wt, "deep", "config.yml")); err != nil {
		t.Errorf("nested dest not created: %v", err)
	}
}

func TestRun_CommandRelativeWorkDir(t *testing.T) {
	main, wt := tempWorktreePair(t)
	_ = os.MkdirAll(filepath.Join(wt, "a", "b"), 0o755)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{
		{Type: config.HookCommand, Run: "touch marker.txt", WorkDir: "a/b"},
	}}}
	_, err := Run(cfg, Context{Path: wt, MainPath: main}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "a", "b", "marker.txt")); err != nil {
		t.Errorf("command did not run in nested work_dir: %v", err)
	}
}

func TestRun_EmptyHooksOK(t *testing.T) {
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{}}}
	results, err := Run(cfg, Context{}, os.Stderr, os.Stderr)
	if err != nil {
		t.Errorf("Run on empty list errored: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

// TestRun_CommandAutoSourcesEnvLocal verifies the UX fix from bug-hunt R4-7:
// when .env.local exists in the worktree, command hooks see its vars in shell
// env without needing `source .env.local` themselves.
func TestRun_CommandAutoSourcesEnvLocal(t *testing.T) {
	main, wt := tempWorktreePair(t)
	envLocal := filepath.Join(wt, ".env.local")
	if err := os.WriteFile(envLocal, []byte("PORT=7777\nMY_VAR=hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{{
		Type: config.HookCommand,
		Run:  "echo PORT=$PORT MY_VAR=$MY_VAR > captured.txt",
	}}}}
	_, err := Run(cfg, Context{Path: wt, MainPath: main}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(wt, "captured.txt"))
	want := "PORT=7777 MY_VAR=hello\n"
	if string(got) != want {
		t.Errorf("auto-source failed: got %q want %q", string(got), want)
	}
}

func TestRun_CommandNoEnvLocalIsFine(t *testing.T) {
	// If .env.local doesn't exist, the preamble must not be added (or must
	// silently no-op). The command should still execute normally.
	main, wt := tempWorktreePair(t)
	cfg := &config.RepoConfig{Hooks: config.Hooks{PostCreate: []config.Hook{{
		Type: config.HookCommand,
		Run:  "echo ran > ok.txt",
	}}}}
	_, err := Run(cfg, Context{Path: wt, MainPath: main}, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(wt, "ok.txt")); strings.TrimSpace(string(data)) != "ran" {
		t.Errorf("command did not run without .env.local: %q", data)
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"with space", "'with space'"},
		{"with'quote", `'with'\''quote'`},
		{"/usr/local/bin", "/usr/local/bin"},
		{"a/b c/d", "'a/b c/d'"},
		{"weird$$\\\\", `'weird$$\\'`},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
