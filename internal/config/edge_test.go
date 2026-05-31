package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_CorruptTOML(t *testing.T) {
	defer withTempConfigDir(t)()
	p, _ := Path()
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("[[project]\n  name = "), 0o600) // malformed
	_, err := Load()
	if err == nil {
		t.Errorf("expected parse error for corrupt config.toml")
	}
}

func TestSave_CreatesParentDir(t *testing.T) {
	defer withTempConfigDir(t)()
	c := &Config{Projects: []Project{{Name: "x", Path: "/x"}}}
	if err := c.Save(); err != nil {
		t.Errorf("Save should mkdir parent: %v", err)
	}
}

func TestConfig_AddRejectsDuplicate(t *testing.T) {
	c := &Config{}
	if err := c.Add(Project{Name: "x", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(Project{Name: "x", Path: "/y"}); err == nil {
		t.Errorf("expected duplicate-name error")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error should say 'already registered', got %q", err)
	}
}

func TestConfig_AllProjects_WalksUpToGitRoot(t *testing.T) {
	// When config is empty but cwd is inside a git repo (deeper than root),
	// AllProjects should walk up and find it.
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	c := &Config{}
	projs := c.AllProjects()
	if len(projs) != 1 {
		t.Fatalf("expected 1 auto-detected project, got %d", len(projs))
	}
	// EvalSymlinks because /var <-> /private/var on macOS
	got, _ := filepath.EvalSymlinks(projs[0].Path)
	want, _ := filepath.EvalSymlinks(repo)
	if got != want {
		t.Errorf("auto-detect: got %s, want %s", got, want)
	}
}

func TestConfig_AllProjects_NoGitNothing(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	c := &Config{}
	projs := c.AllProjects()
	if projs != nil {
		t.Errorf("expected nil when not in a git repo, got %+v", projs)
	}
}

func TestRepoConfig_PortsSection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RepoConfigFile), []byte(`
[ports]
range = "4000-4099"
size = 5
vars = ["PORT", "SERVER_PORT"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadRepoConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ports == nil {
		t.Fatal("Ports section not parsed")
	}
	if c.Ports.Range != "4000-4099" || c.Ports.Size != 5 {
		t.Errorf("bad parse: %+v", c.Ports)
	}
	if len(c.Ports.Vars) != 2 || c.Ports.Vars[0] != "PORT" {
		t.Errorf("vars parse: %+v", c.Ports.Vars)
	}
}

func TestRepoConfig_PortsAbsent_NilNotEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RepoConfigFile), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c, _ := LoadRepoConfig(dir)
	if c.Ports != nil {
		t.Errorf("absent [ports] should yield nil, got %+v", c.Ports)
	}
}
