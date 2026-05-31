package config

import (
	"path/filepath"
	"testing"
)

// withTempConfigDir redirects os.UserConfigDir() to a fresh tempdir so trust.toml
// writes are scoped to the test. On macOS, os.UserConfigDir() ignores
// XDG_CONFIG_HOME and uses $HOME/Library/Application Support, so we override
// HOME too. t.Setenv handles automatic restore; the returned func is a no-op
// kept for call-site symmetry.
func withTempConfigDir(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	return func() {}
}

func TestTrustStore_RoundTrip(t *testing.T) {
	defer withTempConfigDir(t)()
	repo := t.TempDir()
	writeFile(t, repo, RepoConfigFile, "version 1")

	store, err := LoadTrust()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(store.Trusted) != 0 {
		t.Fatalf("expected empty initial store, got %d", len(store.Trusted))
	}

	if err := store.Trust(repo); err != nil {
		t.Fatalf("trust: %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	store2, err := LoadTrust()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(store2.Trusted) != 1 {
		t.Fatalf("expected 1 entry after reload, got %d", len(store2.Trusted))
	}
	abs, _ := filepath.Abs(repo)
	if store2.Trusted[0].Path != abs {
		t.Errorf("path mismatch: got %q, want %q", store2.Trusted[0].Path, abs)
	}
	if len(store2.Trusted[0].ConfigSHA256) != 64 {
		t.Errorf("expected 64-char sha256, got %d", len(store2.Trusted[0].ConfigSHA256))
	}
}

func TestTrustStore_CheckScenarios(t *testing.T) {
	defer withTempConfigDir(t)()
	repo := t.TempDir()

	store, _ := LoadTrust()

	// 1. No config file at all.
	ok, reason := store.Check(repo)
	if ok {
		t.Errorf("expected untrusted for no config, got trusted")
	}
	if reason != "no config" {
		t.Errorf("expected reason=no config, got %q", reason)
	}

	// 2. Config exists but never trusted.
	writeFile(t, repo, RepoConfigFile, "v1")
	ok, reason = store.Check(repo)
	if ok {
		t.Errorf("expected untrusted for new config, got trusted")
	}
	if reason == "" || reason == "no config" {
		t.Errorf("expected helpful reason, got %q", reason)
	}

	// 3. Trust + check → trusted.
	if err := store.Trust(repo); err != nil {
		t.Fatalf("trust: %v", err)
	}
	ok, reason = store.Check(repo)
	if !ok {
		t.Errorf("expected trusted after Trust(), got reason=%q", reason)
	}

	// 4. Edit config → trust auto-revoked (sha mismatch).
	writeFile(t, repo, RepoConfigFile, "v2-injected")
	ok, reason = store.Check(repo)
	if ok {
		t.Errorf("expected untrusted after config edit, got trusted")
	}
	if reason == "" {
		t.Errorf("expected reason for sha mismatch, got empty")
	}

	// 5. Re-trust → trusted again.
	if err := store.Trust(repo); err != nil {
		t.Fatalf("re-trust: %v", err)
	}
	ok, _ = store.Check(repo)
	if !ok {
		t.Errorf("expected trusted after re-trust, got untrusted")
	}
}

func TestTrustStore_Revoke(t *testing.T) {
	defer withTempConfigDir(t)()
	repo := t.TempDir()
	writeFile(t, repo, RepoConfigFile, "v1")

	store, _ := LoadTrust()
	_ = store.Trust(repo)
	if !store.Revoke(repo) {
		t.Errorf("Revoke returned false for trusted repo")
	}
	ok, _ := store.Check(repo)
	if ok {
		t.Errorf("expected untrusted after Revoke")
	}
	if store.Revoke(repo) {
		t.Errorf("Revoke returned true for already-revoked")
	}
}

func TestTrustStore_NoConfigErrorOnTrust(t *testing.T) {
	defer withTempConfigDir(t)()
	repo := t.TempDir()
	store, _ := LoadTrust()
	if err := store.Trust(repo); err == nil {
		t.Errorf("expected error trusting repo without .forktrustconfig")
	}
}
