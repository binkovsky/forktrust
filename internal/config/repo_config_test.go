package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestLoadRepoConfig_Missing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadRepoConfig(dir)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil cfg for missing file, got %+v", cfg)
	}
}

func TestLoadRepoConfig_ValidAllHookTypes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, RepoConfigFile, `
[[hooks.post_create]]
type = "copy"
from = ".env"
to = ".env"

[[hooks.post_create]]
type = "symlink"
from = "node_modules"
to = "node_modules"

[[hooks.post_create]]
type = "command"
run = "echo {{.Slug}}"
work_dir = "sub"
env = { NODE_ENV = "development" }
`)
	cfg, err := LoadRepoConfig(dir)
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if got := len(cfg.Hooks.PostCreate); got != 3 {
		t.Fatalf("expected 3 hooks, got %d", got)
	}
	if got := cfg.Hooks.PostCreate[2].Env["NODE_ENV"]; got != "development" {
		t.Errorf("expected NODE_ENV=development, got %q", got)
	}
	if got := cfg.Hooks.PostCreate[2].WorkDir; got != "sub" {
		t.Errorf("expected work_dir=sub, got %q", got)
	}
}

func TestRepoConfigValidate_Errors(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		wantSub string
	}{
		{"copy needs from+to", `[[hooks.post_create]]
type = "copy"
from = "x"`, "from and to are required"},
		{"symlink rejects run", `[[hooks.post_create]]
type = "symlink"
from = "a"
to = "b"
run = "echo x"`, "run is not valid"},
		{"command needs run", `[[hooks.post_create]]
type = "command"`, "run is required"},
		{"command rejects from", `[[hooks.post_create]]
type = "command"
run = "echo x"
from = "y"`, "from/to are not valid"},
		{"empty type", `[[hooks.post_create]]
from = "a"`, "type is required"},
		{"unknown type", `[[hooks.post_create]]
type = "rebase"
run = "x"`, "unknown type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, RepoConfigFile, tc.toml)
			_, err := LoadRepoConfig(dir)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestRepoConfig_HasCommandHooks(t *testing.T) {
	cases := []struct {
		name string
		cfg  *RepoConfig
		want bool
	}{
		{"nil", nil, false},
		{"empty", &RepoConfig{}, false},
		{"only copy", &RepoConfig{Hooks: Hooks{PostCreate: []Hook{{Type: HookCopy, From: "a", To: "b"}}}}, false},
		{"only symlink", &RepoConfig{Hooks: Hooks{PostCreate: []Hook{{Type: HookSymlink, From: "a", To: "b"}}}}, false},
		{"mixed", &RepoConfig{Hooks: Hooks{PostCreate: []Hook{{Type: HookCopy, From: "a", To: "b"}, {Type: HookCommand, Run: "x"}}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.HasCommandHooks(); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSHA256RepoConfig_Stable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, RepoConfigFile, "content")
	a, err := SHA256RepoConfig(dir)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	b, _ := SHA256RepoConfig(dir)
	if a != b {
		t.Errorf("hash unstable: %s vs %s", a, b)
	}
	if len(a) != 64 {
		t.Errorf("expected 64-char hex, got %d", len(a))
	}
}

func TestSHA256RepoConfig_ChangesOnEdit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, RepoConfigFile, "v1")
	a, _ := SHA256RepoConfig(dir)
	writeFile(t, dir, RepoConfigFile, "v2")
	b, _ := SHA256RepoConfig(dir)
	if a == b {
		t.Errorf("expected hash to change after edit, both %s", a)
	}
}

func TestSHA256RepoConfig_Missing(t *testing.T) {
	dir := t.TempDir()
	sum, err := SHA256RepoConfig(dir)
	if err != nil {
		t.Errorf("expected nil error for missing file, got %v", err)
	}
	if sum != "" {
		t.Errorf("expected empty hash for missing, got %q", sum)
	}
}
