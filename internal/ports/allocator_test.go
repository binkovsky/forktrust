package ports

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// Some existing tests use fake repo paths like "/r". For those, the orphan
// pruner inside Allocate now drops blocks whose repo is unreachable. To keep
// the older tests valid without rewriting them all, we ensure /r exists as
// a real temp dir tree for the duration of the test process. This is only
// needed because the original tests precede the orphan-prune feature.

func storePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ports.json")
}

func TestAllocate_FirstBlock(t *testing.T) {
	p := storePath(t)
	blk, err := Allocate(p, DefaultOpts("/r", "a"))
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if blk.Start != 3000 || blk.Size != 10 || blk.End() != 3009 {
		t.Errorf("expected [3000, 3009] size 10, got %+v", blk)
	}
}

func TestAllocate_SequentialAlignedBlocks(t *testing.T) {
	p := storePath(t)
	want := []int{3000, 3010, 3020, 3030}
	for i, expected := range want {
		blk, err := Allocate(p, DefaultOpts("/r", fmt.Sprintf("task%d", i)))
		if err != nil {
			t.Fatalf("Allocate %d: %v", i, err)
		}
		if blk.Start != expected {
			t.Errorf("task%d: expected start %d, got %d", i, expected, blk.Start)
		}
	}
}

func TestAllocate_Idempotent(t *testing.T) {
	p := storePath(t)
	a, _ := Allocate(p, DefaultOpts("/r", "a"))
	b, _ := Allocate(p, DefaultOpts("/r", "a"))
	if a != b {
		t.Errorf("expected idempotent allocation, got %+v vs %+v", a, b)
	}
	blocks, _ := List(p)
	if len(blocks) != 1 {
		t.Errorf("expected 1 block after idempotent allocate, got %d", len(blocks))
	}
}

func TestAllocate_FillsGapsAfterRelease(t *testing.T) {
	p := storePath(t)
	a, _ := Allocate(p, DefaultOpts("/r", "a")) // 3000
	b, _ := Allocate(p, DefaultOpts("/r", "b")) // 3010
	c, _ := Allocate(p, DefaultOpts("/r", "c")) // 3020

	if a.Start != 3000 || b.Start != 3010 || c.Start != 3020 {
		t.Fatalf("setup wrong: %+v %+v %+v", a, b, c)
	}

	// Release the middle block.
	if err := Release(p, "/r", "b"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Next allocation should reuse 3010.
	d, _ := Allocate(p, DefaultOpts("/r", "d"))
	if d.Start != 3010 {
		t.Errorf("expected gap reuse at 3010, got %d", d.Start)
	}
}

func TestAllocate_ExhaustsRange(t *testing.T) {
	p := storePath(t)
	opts := AllocOpts{Min: 3000, Max: 3019, Size: 10} // exactly 2 blocks
	for _, slug := range []string{"a", "b"} {
		o := opts
		o.Repo = "/r"
		o.Slug = slug
		if _, err := Allocate(p, o); err != nil {
			t.Fatalf("Allocate %s: %v", slug, err)
		}
	}
	o := opts
	o.Repo = "/r"
	o.Slug = "c"
	if _, err := Allocate(p, o); err == nil {
		t.Errorf("expected exhaustion error on 3rd allocation, got nil")
	}
}

func TestRelease_NoOpForUnknown(t *testing.T) {
	p := storePath(t)
	if err := Release(p, "/r", "never-allocated"); err != nil {
		t.Errorf("Release of unknown should be no-op, got %v", err)
	}
}

func TestAllocate_PersistAcrossLoad(t *testing.T) {
	p := storePath(t)
	a, _ := Allocate(p, DefaultOpts("/r", "a"))

	// Simulate a fresh process by loading from disk directly.
	store, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if len(store.Blocks) != 1 || store.Blocks[0] != a {
		t.Errorf("expected persisted block %+v, got %+v", a, store.Blocks)
	}
}

func TestAllocate_ConcurrentNoOverlap(t *testing.T) {
	// Race-safety: N goroutines each allocate for unique slugs against the
	// same store. With flock working, no two blocks should overlap.
	p := storePath(t)
	// Create real worktree dirs so PruneOrphans (called from Allocate)
	// keeps each block alive instead of treating them as ghosts.
	repo := t.TempDir()
	const n = 20
	for i := 0; i < n; i++ {
		if err := os.MkdirAll(filepath.Join(repo, ".forktrust", "worktrees", fmt.Sprintf("slug-%d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	blocks := make([]Block, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			blocks[i], errs[i] = Allocate(p, DefaultOpts(repo, fmt.Sprintf("slug-%d", i)))
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("Allocate[%d]: %v", i, e)
		}
	}

	// Sort and verify no two consecutive blocks overlap.
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Start < blocks[j].Start })
	for i := 1; i < n; i++ {
		if blocks[i].Start <= blocks[i-1].End() {
			t.Errorf("overlap detected: %+v vs %+v", blocks[i-1], blocks[i])
		}
	}
	// And verify aligned (each Start divisible by 10 within the default range).
	for i, b := range blocks {
		if (b.Start-3000)%10 != 0 {
			t.Errorf("block[%d] not aligned to 10: %+v", i, b)
		}
	}
}

func TestAllocate_Validation(t *testing.T) {
	p := storePath(t)
	cases := []struct {
		name string
		opts AllocOpts
	}{
		{"no repo", AllocOpts{Slug: "a"}},
		{"no slug", AllocOpts{Repo: "/r"}},
		{"max<min", AllocOpts{Repo: "/r", Slug: "a", Min: 4000, Max: 3000, Size: 10}},
		{"size 0 over-range", AllocOpts{Repo: "/r", Slug: "a", Min: 3000, Max: 3000, Size: 10}},
		{"port out of bounds", AllocOpts{Repo: "/r", Slug: "a", Min: 0, Max: 10, Size: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Allocate(p, tc.opts); err == nil {
				t.Errorf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		in      string
		wantMin int
		wantMax int
		wantErr bool
	}{
		{"", 3000, 3999, false},
		{"3000-3999", 3000, 3999, false},
		{"5000-5100", 5000, 5100, false},
		{" 4000 - 4099 ", 4000, 4099, false},
		{"nope", 0, 0, true},
		{"3000", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			mn, mx, err := ParseRange(tc.in)
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if mn != tc.wantMin || mx != tc.wantMax {
					t.Errorf("got %d-%d, want %d-%d", mn, mx, tc.wantMin, tc.wantMax)
				}
			}
		})
	}
}

func TestRenderEnv_DefaultsAndExtras(t *testing.T) {
	b := Block{Start: 3010, Size: 10}
	body := RenderEnv(b, nil)
	for _, line := range []string{"PORT=3010", "PORT_END=3019", "FORKTRUST_PORT_START=3010", "FORKTRUST_PORT_SIZE=10"} {
		if !contains(body, line) {
			t.Errorf("missing %q in:\n%s", line, body)
		}
	}
}

func TestRenderEnv_MultipleVars(t *testing.T) {
	b := Block{Start: 4020, Size: 10}
	body := RenderEnv(b, []string{"PORT", "FLASK_RUN_PORT", "SERVER_PORT"})
	for _, line := range []string{"PORT=4020", "FLASK_RUN_PORT=4020", "SERVER_PORT=4020", "PORT_END=4029"} {
		if !contains(body, line) {
			t.Errorf("missing %q in:\n%s", line, body)
		}
	}
}

func TestWriteEnv_RefusesToOverwriteUserFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, EnvFileName)
	if err := os.WriteFile(target, []byte("USER_KEY=manually-set\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteEnv(dir, Block{Start: 3000, Size: 10}, nil)
	if err == nil {
		t.Errorf("expected refusal to overwrite user-authored file")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "USER_KEY=manually-set\n" {
		t.Errorf("user file mutated: %q", data)
	}
}

func TestWriteEnv_OverwritesOwnFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEnv(dir, Block{Start: 3000, Size: 10}, nil); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteEnv(dir, Block{Start: 3020, Size: 10}, nil); err != nil {
		t.Errorf("second write of own file failed: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, EnvFileName))
	if !contains(string(data), "PORT=3020") {
		t.Errorf("expected PORT=3020 after second write, got:\n%s", data)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || stringsIndex(haystack, needle) >= 0)
}

func stringsIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
