// Package pathsafe is the single canonical source for "operate on a user-
// supplied path inside a trusted root, refusing anything that escapes".
//
// Both .forktrustconfig hooks (file copies/symlinks) and the ports writer
// (.env.local) used to roll their own protection. v0.6.1 fixed hooks; the
// review for v0.6.2 found ports still rolled its own (and weaker) version.
// Pulling the logic into a shared package means future writers — and future
// bypass fixes — land in one place.
//
// Two surfaces:
//
//   - SafeJoin(root, rel): join a user-supplied relative path to a trusted
//     root, refusing lexical escape (../, absolute) AND runtime escape (any
//     existing component is a symlink whose resolved target leaves root).
//
//   - SafeWriteFile(root, rel, data, mode): SafeJoin + open the leaf with
//     O_NOFOLLOW. This protects the LEAF against a swap between SafeJoin's
//     Lstat sweep and the actual write. ANCESTOR components are checked
//     once by SafeJoin and are NOT re-checked at open time — a sibling
//     process that swaps an ancestor mid-call could still escape. Callers
//     that care about that race must serialize against ancestor mutations
//     externally (or pass a single-component rel, where there are no
//     ancestors to race).
//
//     The write is NOT atomic. A killed process leaves a truncated file.
//     If atomicity matters, the caller should write to a tmp path and
//     rename (which SafeJoin can validate just as well).
package pathsafe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SafeJoin joins root with rel, refusing escape.
//
// Lexical rules:
//  1. rel must not be absolute (no "/...", no Windows drive)
//  2. after filepath.Clean, rel must not start with ".."
//
// Runtime rule:
//
//	for each EXISTING component along root+rel, Lstat it; if it is a symlink,
//	EvalSymlinks it and verify the resolved target is inside EvalSymlinks(root).
//	Non-existent suffix components are allowed (callers may pass paths that
//	haven't been created yet — e.g. the destination of a fresh write).
//
// Dangling symlinks are refused (we cannot prove they don't escape).
func SafeJoin(root, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative (not absolute)", rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes root via parent traversal", rel)
	}
	if strings.HasPrefix(cleaned, `..\`) {
		return "", fmt.Errorf("path %q escapes root via parent traversal", rel)
	}
	joined := filepath.Join(root, cleaned)

	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		// If root itself can't be resolved (doesn't exist, etc.) the lexical
		// check above is all we can promise. Callers pass real existing roots.
		return joined, nil
	}

	cur := root
	for _, p := range strings.Split(cleaned, string(filepath.Separator)) {
		if p == "" || p == "." {
			continue
		}
		cur = filepath.Join(cur, p)
		info, err := os.Lstat(cur)
		if err != nil {
			break // doesn't exist; nothing more to check
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		real, err := filepath.EvalSymlinks(cur)
		if err != nil {
			return "", fmt.Errorf("symlink at %q is dangling or unresolvable: %w", cur, err)
		}
		relReal, err := filepath.Rel(rootReal, real)
		if err != nil || relReal == ".." || strings.HasPrefix(relReal, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path %q passes through symlink %q -> %q which escapes root %q",
				rel, cur, real, rootReal)
		}
	}
	return joined, nil
}

// WithinRoot reports whether p (after symlink resolution) is the same as or
// a descendant of root (after symlink resolution). Used by file-walk
// callbacks that need to filter per-file symlinks pointing outside.
func WithinRoot(root, p string) bool {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	pReal, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootReal, pReal)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// SafeWriteFile writes data to <root>/<rel>, refusing:
//   - any SafeJoin failure (lexical or runtime symlink escape)
//   - a leaf-level symlink at write time (O_NOFOLLOW), closing the LEAF
//     TOCTOU window between SafeJoin's Lstat sweep and the write
//
// The write is NOT atomic — partial writes are possible on signal / disk
// full. The ancestor TOCTOU window is also NOT closed (see package doc);
// single-component rel paths are recommended.
func SafeWriteFile(root, rel string, data []byte, mode os.FileMode) error {
	target, err := SafeJoin(root, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	// Open with O_NOFOLLOW so a symlink at the leaf (even one swapped in
	// after a Lstat) fails atomically at the kernel boundary with ELOOP.
	// noFollowFlag is platform-defined: real flag on unix, 0 on Windows
	// (we don't ship Windows binaries, so this is a build-only fallback).
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC | noFollowFlag
	f, err := os.OpenFile(target, flag, mode)
	if err != nil {
		return fmt.Errorf("safe open %s: %w", target, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return nil
}

// OpenLeafNoFollow opens path for writing with O_NOFOLLOW on unix (where
// supported) so a leaf-symlink swap between any upstream Lstat and this open
// fails ELOOP. The caller is responsible for SafeJoin-ing the ancestor chain
// first (e.g. hooks.copyFile uses secureJoin upstream then calls this for
// the actual write). Returns *os.File the caller must Close.
func OpenLeafNoFollow(path string, flag int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|noFollowFlag, mode)
}
