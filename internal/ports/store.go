// Package ports allocates aligned port blocks per worktree and persists the
// assignments to ~/.config/forktrust/ports.json. Blocks are auto-released on
// finish/rm so port numbers don't drift over time.
package ports

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Block is a contiguous port range owned by one (repo, slug) pair.
type Block struct {
	Repo  string `json:"repo"`  // absolute path of the registered repo
	Slug  string `json:"slug"`  // worktree slug
	Start int    `json:"start"` // first port, inclusive
	Size  int    `json:"size"`  // number of ports in the block
}

// End returns the last port in the block (inclusive).
func (b Block) End() int { return b.Start + b.Size - 1 }

// Overlaps reports whether this block intersects [start, end].
func (b Block) Overlaps(start, end int) bool {
	return b.Start <= end && b.End() >= start
}

// Store is the persisted set of allocations.
type Store struct {
	Blocks []Block `json:"blocks"`
}

// DefaultPath returns ~/.config/forktrust/ports.json (XDG-aware).
func DefaultPath() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "forktrust", "ports.json"), nil
}

// loadFrom reads the store from the given file path. Returns an empty store
// if the file doesn't exist (first-run is free).
func loadFrom(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return &Store{}, nil
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

// saveTo writes atomically via a temp-and-rename. Concurrent writers MUST
// hold the file lock first (see allocator.go).
func (s *Store) saveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// findBlock returns the index of the block matching (repo, slug), or -1.
func (s *Store) findBlock(repo, slug string) int {
	for i := range s.Blocks {
		if s.Blocks[i].Repo == repo && s.Blocks[i].Slug == slug {
			return i
		}
	}
	return -1
}

// Sorted returns a stable copy ordered by Start.
func (s *Store) Sorted() []Block {
	out := make([]Block, len(s.Blocks))
	copy(out, s.Blocks)
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// PruneOrphans drops any block whose worktree directory is provably gone.
// Returns the count removed. Used by Allocate to keep the store from
// accumulating dead allocations after `git worktree remove` (or any other
// external removal that bypasses `forktrust rm`).
//
// Defensive: only drops on os.ErrNotExist. Permission errors, transient IO
// errors, and other Stat failures KEEP the block — we'd rather leak a port
// than orphan a user's allocation because of a flaky disk. Also keeps blocks
// whose repo root itself is unreachable (the parent missing covers e.g. an
// unmounted volume, where the user expects their allocations to come back
// when the disk is back).
func (s *Store) PruneOrphans() int {
	kept := s.Blocks[:0]
	dropped := 0
	for _, b := range s.Blocks {
		// If the repo root itself is unreachable, keep the block (probably
		// a mounted volume that's offline, not a real removal).
		if _, err := os.Stat(b.Repo); err != nil {
			kept = append(kept, b)
			continue
		}
		path := filepath.Join(b.Repo, ".forktrust", "worktrees", b.Slug)
		_, err := os.Stat(path)
		switch {
		case err == nil:
			kept = append(kept, b)
		case os.IsNotExist(err):
			dropped++
		default:
			// Any other error: be safe, keep the block.
			kept = append(kept, b)
		}
	}
	s.Blocks = kept
	return dropped
}
