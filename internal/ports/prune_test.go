package ports

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPruneOrphans_DropsMissingWorktrees covers the orphan-port bug:
// if a user removes a worktree externally (e.g. `git worktree remove`
// instead of `forktrust rm`), the block stays in ports.json forever.
// PruneOrphans is called from Allocate to keep the store honest.
func TestPruneOrphans_DropsMissingWorktrees(t *testing.T) {
	repo := t.TempDir()
	// One real worktree directory, one stale.
	real := filepath.Join(repo, ".forktrust", "worktrees", "alive")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}

	s := &Store{Blocks: []Block{
		{Repo: repo, Slug: "alive", Start: 3000, Size: 10},
		{Repo: repo, Slug: "ghost", Start: 3010, Size: 10},
		{Repo: repo, Slug: "another-ghost", Start: 3020, Size: 10},
	}}
	dropped := s.PruneOrphans()
	if dropped != 2 {
		t.Errorf("expected to drop 2 ghosts, got %d", dropped)
	}
	if len(s.Blocks) != 1 || s.Blocks[0].Slug != "alive" {
		t.Errorf("expected only 'alive' to survive, got %+v", s.Blocks)
	}
}

func TestPruneOrphans_NoOpWhenAllAlive(t *testing.T) {
	repo := t.TempDir()
	for _, slug := range []string{"a", "b"} {
		_ = os.MkdirAll(filepath.Join(repo, ".forktrust", "worktrees", slug), 0o755)
	}
	s := &Store{Blocks: []Block{
		{Repo: repo, Slug: "a", Start: 3000, Size: 10},
		{Repo: repo, Slug: "b", Start: 3010, Size: 10},
	}}
	dropped := s.PruneOrphans()
	if dropped != 0 {
		t.Errorf("expected 0 drops, got %d", dropped)
	}
	if len(s.Blocks) != 2 {
		t.Errorf("expected both blocks to survive, got %+v", s.Blocks)
	}
}

func TestPruneOrphans_EmptyStore(t *testing.T) {
	s := &Store{}
	if dropped := s.PruneOrphans(); dropped != 0 {
		t.Errorf("expected 0 on empty store, got %d", dropped)
	}
}

// TestAllocate_AutoPrunesBeforeAllocating verifies the e2e fix: after an
// external removal, the next Allocate reclaims the freed range.
func TestAllocate_AutoPrunesBeforeAllocating(t *testing.T) {
	repo := t.TempDir()
	storeP := filepath.Join(t.TempDir(), "ports.json")

	// Allocate two blocks the normal way (creates dirs first).
	for _, s := range []string{"a", "b"} {
		_ = os.MkdirAll(filepath.Join(repo, ".forktrust", "worktrees", s), 0o755)
		if _, err := Allocate(storeP, AllocOpts{Repo: repo, Slug: s, Min: 3000, Max: 3019, Size: 10}); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate external removal of 'a'.
	_ = os.RemoveAll(filepath.Join(repo, ".forktrust", "worktrees", "a"))

	// Range is exhausted (b owns 3010-3019). Without auto-prune, this would
	// error. With auto-prune, 'a's block frees and 'c' allocates 3000.
	_ = os.MkdirAll(filepath.Join(repo, ".forktrust", "worktrees", "c"), 0o755)
	blk, err := Allocate(storeP, AllocOpts{Repo: repo, Slug: "c", Min: 3000, Max: 3019, Size: 10})
	if err != nil {
		t.Fatalf("Allocate after orphan: %v", err)
	}
	if blk.Start != 3000 {
		t.Errorf("expected to reclaim 3000 after orphan prune, got %d", blk.Start)
	}
}
