package pathsafe

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSafeJoin_AcceptsCleanRelative(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "config"), 0o755)
	cases := []string{".env", "config/local.json", "./a/b", "a/./b/../c", "node_modules"}
	for _, c := range cases {
		if _, err := SafeJoin(root, c); err != nil {
			t.Errorf("%q: unexpected error %v", c, err)
		}
	}
}

func TestSafeJoin_RejectsEscape(t *testing.T) {
	root := t.TempDir()
	for _, c := range []string{"/etc/passwd", "../escape", "../../../etc/passwd", "a/../../escape", "", ".."} {
		if _, err := SafeJoin(root, c); err == nil {
			t.Errorf("%q accepted; expected refusal", c)
		}
	}
}

func TestSafeJoin_RefusesSourceSymlinkEscapingRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(outside, []byte("x"), 0o600)
	_ = os.Symlink(outside, filepath.Join(root, "evil"))
	if _, err := SafeJoin(root, "evil"); err == nil {
		t.Errorf("expected refusal for outward-pointing symlink")
	}
}

func TestSafeJoin_RefusesAncestorSymlinkEscapingRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	_ = os.Symlink(outsideDir, filepath.Join(root, "bridge"))
	if _, err := SafeJoin(root, "bridge/target"); err == nil {
		t.Errorf("expected refusal for ancestor symlink escape")
	}
}

func TestSafeJoin_AllowsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "real"), []byte("ok"), 0o600)
	_ = os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias"))
	if _, err := SafeJoin(root, "alias"); err != nil {
		t.Errorf("internal symlink should be allowed: %v", err)
	}
}

func TestSafeJoin_RefusesDangling(t *testing.T) {
	root := t.TempDir()
	_ = os.Symlink(filepath.Join(t.TempDir(), "ghost"), filepath.Join(root, "dangle"))
	if _, err := SafeJoin(root, "dangle"); err == nil {
		t.Errorf("dangling symlink should be refused")
	}
}

func TestWithinRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "sub")
	_ = os.MkdirAll(inside, 0o755)
	if !WithinRoot(root, inside) {
		t.Error("inside dir should be within root")
	}
	escape := t.TempDir()
	link := filepath.Join(root, "outlink")
	_ = os.Symlink(escape, link)
	if WithinRoot(root, link) {
		t.Error("outward symlink should not be within root")
	}
}

// TestSafeWriteFile_RefusesLeafSymlink covers the O_NOFOLLOW protection
// that closes the TOCTOU window between an Lstat-style check and write.
func TestSafeWriteFile_RefusesLeafSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "captured.txt")
	if err := os.Symlink(outside, filepath.Join(root, "victim")); err != nil {
		t.Fatal(err)
	}
	err := SafeWriteFile(root, "victim", []byte("DATA"), 0o600)
	if err == nil {
		t.Fatal("expected refusal; got nil")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("SafeWriteFile wrote through symlink to %s", outside)
	}
}

func TestSafeWriteFile_RefusesAncestorEscape(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	_ = os.Symlink(outsideDir, filepath.Join(root, "bridge"))
	err := SafeWriteFile(root, "bridge/leaked.txt", []byte("X"), 0o600)
	if err == nil {
		t.Fatal("expected refusal via ancestor symlink")
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "leaked.txt")); err == nil {
		t.Error("wrote through ancestor symlink")
	}
}

func TestSafeWriteFile_HappyPath(t *testing.T) {
	root := t.TempDir()
	if err := SafeWriteFile(root, "x/y/z.txt", []byte("HI"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "x", "y", "z.txt"))
	if err != nil || string(data) != "HI" {
		t.Errorf("got %q err=%v", data, err)
	}
}

// TestSafeWriteFile_RaceAgainstSymlinkSwap simulates the TOCTOU the v0.6.1
// fix had. We hammer SafeWriteFile while a concurrent goroutine repeatedly
// swaps the target between a regular file and a symlink. O_NOFOLLOW must
// guarantee the write either lands in the file OR refuses — never follows
// the symlink to write outside.
func TestSafeWriteFile_RaceAgainstSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "must-stay-empty.txt")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(filepath.Join(root, "race"))
			_ = os.Symlink(outside, filepath.Join(root, "race"))
			_ = os.Remove(filepath.Join(root, "race"))
		}
	}()

	for i := 0; i < 200; i++ {
		_ = SafeWriteFile(root, "race", []byte("ok"), 0o600)
	}
	close(stop)
	wg.Wait()

	// Outside file MUST NOT have been written through a symlink at any point.
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("race produced a write outside root at %s — TOCTOU not closed", outside)
	}
}

// quick sanity that the lexical guard still catches things SafeWriteFile-side.
func TestSafeWriteFile_RefusesLexicalEscape(t *testing.T) {
	root := t.TempDir()
	if err := SafeWriteFile(root, "../escape.txt", []byte("X"), 0o600); err == nil {
		t.Errorf("lexical escape accepted by SafeWriteFile")
	}
	if !strings.HasSuffix(filepath.Clean(root), filepath.Base(root)) {
		t.Skip("temp dir unexpectedly normalized")
	}
}
