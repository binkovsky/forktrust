package ai

import (
	"sort"
	"testing"
)

func TestRegistry_Alphabetized(t *testing.T) {
	names := make([]string, len(Registry))
	for i, a := range Registry {
		names[i] = a.Name
	}
	sortedNames := make([]string, len(names))
	copy(sortedNames, names)
	sort.Strings(sortedNames)
	for i := range names {
		if names[i] != sortedNames[i] {
			t.Errorf("Registry must stay alphabetized; got %v, want %v", names, sortedNames)
			break
		}
	}
}

func TestRegistry_GtrParity(t *testing.T) {
	// gtr ships 9 adapters; we must match at minimum.
	// https://github.com/coderabbitai/git-worktree-runner README.
	required := []string{"aider", "auggie", "claude", "codex", "continue", "copilot", "cursor", "gemini", "opencode"}
	for _, r := range required {
		if Find(r) == nil {
			t.Errorf("missing adapter %q (required for gtr parity)", r)
		}
	}
}

func TestFind_CaseInsensitive(t *testing.T) {
	if Find("CLAUDE") == nil {
		t.Error("Find should be case-insensitive")
	}
	if Find("  claude  ") == nil {
		t.Error("Find should trim whitespace")
	}
	if Find("nonexistent-tool") != nil {
		t.Error("Find should return nil for unknown tool")
	}
}

func TestNames_StableOrder(t *testing.T) {
	a := Names()
	b := Names()
	if len(a) != len(b) {
		t.Fatalf("Names returned different lengths on repeated calls")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("Names not stable: pos %d differs (%q vs %q)", i, a[i], b[i])
		}
	}
	// Sorted.
	for i := 1; i < len(a); i++ {
		if a[i-1] > a[i] {
			t.Errorf("Names not sorted: %q > %q", a[i-1], a[i])
		}
	}
}

func TestAdapter_AllHaveBin(t *testing.T) {
	for _, a := range Registry {
		if a.Name == "" {
			t.Error("adapter with empty Name")
		}
		if a.Bin == "" {
			t.Errorf("adapter %q has empty Bin", a.Name)
		}
		if a.Description == "" {
			t.Errorf("adapter %q has empty Description", a.Name)
		}
	}
}

func TestAdapter_CursorUsesPathArg(t *testing.T) {
	a := Find("cursor")
	if a == nil {
		t.Fatal("cursor adapter missing")
	}
	found := false
	for _, arg := range a.Args {
		if arg == "{path}" {
			found = true
			break
		}
	}
	if !found {
		t.Error("cursor adapter must use {path} arg (it's an editor that opens a directory)")
	}
}

func TestAdapter_ClaudeDoesNotUsePathArg(t *testing.T) {
	a := Find("claude")
	if a == nil {
		t.Fatal("claude adapter missing")
	}
	for _, arg := range a.Args {
		if arg == "{path}" {
			t.Error("claude is a CLI tool that runs in cwd, not an editor opening a path")
		}
	}
}

func TestLaunch_UnknownBinary(t *testing.T) {
	a := Adapter{Name: "ghost", Bin: "definitely-not-installed-binary-xyz123"}
	err := a.Launch("/tmp")
	if err == nil {
		t.Error("expected error for missing binary")
	}
}
