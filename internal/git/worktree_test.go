package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestIsForktrustManaged covers the ownership-proof logic used by IgnoredCount
// to decide whether a .env.local is safe to delete. The rules are strict:
//   - Only returns true when the file starts with the EXACT envLocalManagedHeader
//     (full first line including the trailing newline).
//   - A file starting with a prefix of the header but with different continuation
//     must return false so user data is never silently dropped.
func TestIsForktrustManaged(t *testing.T) {
	tmp := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "exact managed header",
			content: envLocalManagedHeader + "PORT=3000\n",
			want:    true,
		},
		{
			name:    "exact header only (no body)",
			content: envLocalManagedHeader,
			want:    true,
		},
		{
			name:    "loose prefix — same words but different continuation",
			content: "# Managed by forktrust but actually user data\nSECRET=mine\n",
			want:    false,
		},
		{
			name:    "loose prefix — missing trailing newline after header",
			content: "# Managed by forktrust. Do not edit; values are overwritten on each `forktrust new`.PORT=3000\n",
			want:    false,
		},
		{
			name:    "user .env.local without any marker",
			content: "NEXT_PUBLIC_API=https://api.example.com\nSECRET=abc123\n",
			want:    false,
		},
		{
			name:    "empty file",
			content: "",
			want:    false,
		},
		{
			name:    "comment only, different text",
			content: "# My local config\nPORT=4000\n",
			want:    false,
		},
		{
			name: "full realistic managed file",
			content: envLocalManagedHeader +
				"# Block released automatically on `forktrust finish` / `forktrust rm`.\n" +
				"PORT=3000\nPORT_END=3009\nFORKTRUST_PORT_START=3000\nFORKTRUST_PORT_END=3009\nFORKTRUST_PORT_SIZE=10\n",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := write(tt.name+".env.local", tt.content)
			got := isForktrustManaged(path)
			if got != tt.want {
				t.Errorf("isForktrustManaged(%q) = %v, want %v\ncontent: %q",
					tt.name, got, tt.want, tt.content)
			}
		})
	}

	t.Run("missing file returns false", func(t *testing.T) {
		if isForktrustManaged(filepath.Join(tmp, "nonexistent.env.local")) {
			t.Error("expected false for missing file, got true")
		}
	})
}

// TestIgnoredCount_EnvLocalHandling tests the exact conditions under which
// IgnoredCount skips .env.local (forktrust-managed) vs counts it (user file).
// These are the regression cases for v0.6.6 (Base() too broad) and v0.6.8
// (prefix check instead of exact header).
func TestIgnoredCount_EnvLocalHandling(t *testing.T) {
	// Helper: create a temp git repo with .gitignore and optionally a worktree
	setup := func(t *testing.T, gitignoreLine string) (repoPath string) {
		t.Helper()
		tmp := t.TempDir()
		mustRun(t, tmp, "git", "init", "-b", "main")
		mustRun(t, tmp, "git", "config", "user.email", "t@t.com")
		mustRun(t, tmp, "git", "config", "user.name", "T")
		if err := os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte(gitignoreLine+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "x"), []byte("init\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, tmp, "git", "add", "-A")
		mustRun(t, tmp, "git", "commit", "-m", "init")
		return tmp
	}

	t.Run("nested foo/.env.local with exact header is counted (not skipped)", func(t *testing.T) {
		repo := setup(t, "foo/.env.local")
		if err := os.MkdirAll(filepath.Join(repo, "foo"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Exact managed header, but nested — should NOT be skipped
		nested := filepath.Join(repo, "foo", ".env.local")
		if err := os.WriteFile(nested, []byte(envLocalManagedHeader+"PORT=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		n, err := IgnoredCount(repo)
		if err != nil {
			t.Fatalf("IgnoredCount error: %v", err)
		}
		if n == 0 {
			t.Error("expected IgnoredCount > 0: nested foo/.env.local with managed header must be counted, not skipped")
		}
	})

	t.Run("root .env.local with loose prefix is counted (not skipped)", func(t *testing.T) {
		repo := setup(t, ".env.local")
		loosePrefixContent := "# Managed by forktrust but actually user data\nSECRET=lost\n"
		if err := os.WriteFile(filepath.Join(repo, ".env.local"), []byte(loosePrefixContent), 0o600); err != nil {
			t.Fatal(err)
		}
		n, err := IgnoredCount(repo)
		if err != nil {
			t.Fatalf("IgnoredCount error: %v", err)
		}
		if n == 0 {
			t.Error("expected IgnoredCount > 0: root .env.local with loose prefix must be counted, not skipped")
		}
	})

	t.Run("root .env.local with exact header is skipped (forktrust-managed)", func(t *testing.T) {
		repo := setup(t, ".env.local")
		if err := os.WriteFile(filepath.Join(repo, ".env.local"), []byte(envLocalManagedHeader+"PORT=3000\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		n, err := IgnoredCount(repo)
		if err != nil {
			t.Fatalf("IgnoredCount error: %v", err)
		}
		if n != 0 {
			t.Errorf("expected IgnoredCount = 0: root .env.local with exact header is forktrust-managed and safe to delete; got %d", n)
		}
	})

	t.Run("root .env.local with no marker is counted", func(t *testing.T) {
		repo := setup(t, ".env.local")
		if err := os.WriteFile(filepath.Join(repo, ".env.local"), []byte("NEXT_PUBLIC_API=https://api.example.com\nSECRET=abc\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		n, err := IgnoredCount(repo)
		if err != nil {
			t.Fatalf("IgnoredCount error: %v", err)
		}
		if n == 0 {
			t.Error("expected IgnoredCount > 0: user .env.local without marker must be counted")
		}
	})

	t.Run("empty worktree returns 0", func(t *testing.T) {
		repo := setup(t, ".env.local")
		n, err := IgnoredCount(repo)
		if err != nil {
			t.Fatalf("IgnoredCount error: %v", err)
		}
		if n != 0 {
			t.Errorf("expected 0 for clean worktree, got %d", n)
		}
	})

	t.Run("non-env-local ignored file is always counted", func(t *testing.T) {
		repo := setup(t, "secret.log")
		// Even if it starts with the managed header, it must be counted
		if err := os.WriteFile(filepath.Join(repo, "secret.log"), []byte(envLocalManagedHeader+"data\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		n, err := IgnoredCount(repo)
		if err != nil {
			t.Fatalf("IgnoredCount error: %v", err)
		}
		if n == 0 {
			t.Error("expected IgnoredCount > 0: secret.log with managed header must still be counted")
		}
	})
}

// mustRun runs a command in dir, failing the test on error.
func mustRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command %v in %s failed: %v\n%s", args, dir, err, out)
	}
}
