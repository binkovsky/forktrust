package ports

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// AllocOpts controls a single allocation request.
type AllocOpts struct {
	Repo string // registered repo absolute path
	Slug string // worktree slug
	Min  int    // first port to consider (inclusive); default 3000
	Max  int    // last port to consider (inclusive);  default 3999
	Size int    // block size; default 10
}

// DefaultOpts fills in unset fields with the documented defaults.
func DefaultOpts(repo, slug string) AllocOpts {
	return AllocOpts{Repo: repo, Slug: slug, Min: 3000, Max: 3999, Size: 10}
}

// applyDefaults fills any zero field with the documented default.
func (o *AllocOpts) applyDefaults() {
	if o.Min == 0 {
		o.Min = 3000
	}
	if o.Max == 0 {
		o.Max = 3999
	}
	if o.Size == 0 {
		o.Size = 10
	}
}

// Validate checks the request shape.
func (o AllocOpts) Validate() error {
	if o.Repo == "" {
		return fmt.Errorf("repo is required")
	}
	if o.Slug == "" {
		return fmt.Errorf("slug is required")
	}
	if o.Min < 1 || o.Min > 65535 || o.Max < 1 || o.Max > 65535 {
		return fmt.Errorf("port range out of bounds: %d-%d", o.Min, o.Max)
	}
	if o.Max < o.Min {
		return fmt.Errorf("max < min: %d < %d", o.Max, o.Min)
	}
	if o.Size < 1 {
		return fmt.Errorf("size < 1: %d", o.Size)
	}
	if o.Size > o.Max-o.Min+1 {
		return fmt.Errorf("size %d exceeds available range %d-%d", o.Size, o.Min, o.Max)
	}
	return nil
}

// ParseRange parses "3000-3999" into (3000, 3999). Empty string returns
// the defaults (3000, 3999).
func ParseRange(s string) (min, max int, err error) {
	if s == "" {
		return 3000, 3999, nil
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("range must be MIN-MAX, got %q", s)
	}
	mn, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("parse min: %w", err)
	}
	mx, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("parse max: %w", err)
	}
	return mn, mx, nil
}

// Allocate finds or reuses an aligned port block for (repo, slug) and
// persists the allocation. Idempotent: a second Allocate call for the same
// (repo, slug) returns the existing block unchanged.
//
// File access is serialized via a flock on storePath, so concurrent
// `forktrust new` invocations never hand out overlapping ports.
func Allocate(storePath string, opts AllocOpts) (Block, error) {
	opts.applyDefaults()
	if err := opts.Validate(); err != nil {
		return Block{}, err
	}

	var allocated Block
	err := withLockedFile(storePath, func() error {
		store, err := loadFrom(storePath)
		if err != nil {
			return err
		}
		// Sweep blocks whose worktree directories no longer exist. Keeps
		// ports.json from leaking when users remove worktrees externally
		// (e.g. `git worktree remove` bypassing `forktrust rm`).
		store.PruneOrphans()
		if i := store.findBlock(opts.Repo, opts.Slug); i >= 0 {
			allocated = store.Blocks[i]
			return nil
		}
		blk, ok := findFreeBlock(store, opts)
		if !ok {
			return fmt.Errorf("no free aligned block of size %d in range %d-%d", opts.Size, opts.Min, opts.Max)
		}
		store.Blocks = append(store.Blocks, blk)
		if err := store.saveTo(storePath); err != nil {
			return err
		}
		allocated = blk
		return nil
	})
	if err != nil {
		return Block{}, err
	}
	return allocated, nil
}

// Release removes the block owned by (repo, slug). No-op if no block exists.
// Idempotent and safe to call from finish/rm without checking first.
func Release(storePath, repo, slug string) error {
	return withLockedFile(storePath, func() error {
		store, err := loadFrom(storePath)
		if err != nil {
			return err
		}
		i := store.findBlock(repo, slug)
		if i < 0 {
			return nil
		}
		store.Blocks = append(store.Blocks[:i], store.Blocks[i+1:]...)
		return store.saveTo(storePath)
	})
}

// List returns a snapshot of all current allocations.
func List(storePath string) ([]Block, error) {
	var out []Block
	err := withLockedFile(storePath, func() error {
		store, err := loadFrom(storePath)
		if err != nil {
			return err
		}
		out = store.Sorted()
		return nil
	})
	return out, err
}

// findFreeBlock walks the range in aligned `Size` steps and returns the first
// block that does not overlap any existing allocation.
func findFreeBlock(store *Store, opts AllocOpts) (Block, bool) {
	for start := opts.Min; start+opts.Size-1 <= opts.Max; start += opts.Size {
		end := start + opts.Size - 1
		overlap := false
		for _, b := range store.Blocks {
			if b.Overlaps(start, end) {
				overlap = true
				break
			}
		}
		if !overlap {
			return Block{Repo: opts.Repo, Slug: opts.Slug, Start: start, Size: opts.Size}, true
		}
	}
	return Block{}, false
}

// EnsureLockDir makes sure the directory housing storePath exists before
// flock tries to create the lock file.
func EnsureLockDir(storePath string) error {
	dir := storePathDir(storePath)
	return os.MkdirAll(dir, 0o700)
}

func storePathDir(storePath string) string {
	for i := len(storePath) - 1; i >= 0; i-- {
		if storePath[i] == os.PathSeparator {
			return storePath[:i]
		}
	}
	return "."
}
