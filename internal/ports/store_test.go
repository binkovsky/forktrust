package ports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFrom_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ports.json")
	if err := os.WriteFile(p, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := loadFrom(p)
	if err != nil {
		t.Errorf("loadFrom empty file should not error: %v", err)
	}
	if s == nil || len(s.Blocks) != 0 {
		t.Errorf("expected empty store, got %+v", s)
	}
}

func TestLoadFrom_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ports.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadFrom(p)
	if err == nil {
		t.Errorf("expected parse error for corrupt JSON")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse, got %q", err.Error())
	}
}

func TestBlock_EndAndOverlaps(t *testing.T) {
	b := Block{Start: 3010, Size: 10}
	if b.End() != 3019 {
		t.Errorf("End: want 3019, got %d", b.End())
	}
	cases := []struct {
		s, e int
		want bool
	}{
		{3000, 3009, false}, // before
		{3000, 3010, true},  // touches start
		{3015, 3015, true},  // single in middle
		{3019, 3019, true},  // touches end
		{3019, 3025, true},  // overlap right edge
		{3020, 3025, false}, // just after
	}
	for _, c := range cases {
		if got := b.Overlaps(c.s, c.e); got != c.want {
			t.Errorf("Overlaps(%d-%d): want %v got %v", c.s, c.e, c.want, got)
		}
	}
}

func TestSorted_StableByStart(t *testing.T) {
	s := &Store{Blocks: []Block{
		{Repo: "/r", Slug: "c", Start: 3020, Size: 10},
		{Repo: "/r", Slug: "a", Start: 3000, Size: 10},
		{Repo: "/r", Slug: "b", Start: 3010, Size: 10},
	}}
	got := s.Sorted()
	want := []int{3000, 3010, 3020}
	for i, b := range got {
		if b.Start != want[i] {
			t.Errorf("pos %d: want start %d, got %d", i, want[i], b.Start)
		}
	}
	// original must not be mutated
	if s.Blocks[0].Start != 3020 {
		t.Errorf("Sorted mutated original slice")
	}
}

func TestSaveTo_AtomicViaRename(t *testing.T) {
	// Ensure we write to .tmp and rename, so an aborted write doesn't
	// leave a half-written file.
	dir := t.TempDir()
	p := filepath.Join(dir, "ports.json")
	s := &Store{Blocks: []Block{{Repo: "/r", Slug: "a", Start: 3000, Size: 10}}}
	if err := s.saveTo(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Errorf(".tmp file should have been renamed away, but exists at %s", p+".tmp")
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("target file should exist: %v", err)
	}
}

func TestSaveTo_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "nested", "ports.json")
	s := &Store{}
	if err := s.saveTo(p); err != nil {
		t.Errorf("saveTo should mkdir parent: %v", err)
	}
}
