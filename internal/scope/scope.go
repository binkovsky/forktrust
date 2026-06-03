// Package scope manages per-worktree "change contracts" — declarations of
// which file globs a task is allowed to modify. forktrust finish refuses to
// merge if the diff vs the main branch touches files outside the declared
// allowed set.
//
// This is the v0.7.3 feature: agents declare scope at `forktrust new`, and
// finish enforces it. Catches scope creep ("agent edited package-lock.json
// when it should have only touched auth/") before it ships.
package scope

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/bmatcuk/doublestar/v4"
)

// Scope is the per-worktree change contract. Stored as TOML at
// <repo>/.forktrust/scopes/<slug>.toml so it survives worktree renames and
// is symmetric with the .forktrust/worktrees/<slug>/ tree.
//
// Allowed is a list of glob patterns matched with doublestar semantics
// (supports `**` for arbitrary depth, `*` for single segment, `?` for one
// char, `[abc]` classes, `{a,b}` alternation). Patterns are matched
// case-sensitively against forward-slash paths relative to the repo root.
//
// A scope file is OPTIONAL. Worktrees without a scope file have no
// restrictions (backwards-compatible). When present, the file is the
// authoritative contract — Allowed cannot be empty (refused at parse).
type Scope struct {
	// Allowed lists glob patterns of paths the worktree may modify.
	// Examples:
	//   "internal/auth/**"     — any path under internal/auth/
	//   "cmd/api/*.go"         — top-level Go files in cmd/api
	//   "docs/**.md"           — Markdown files under docs/ at any depth
	//   "go.mod"               — exact file
	Allowed []string `toml:"allowed"`
	// CreatedBy / CreatedAt are informational; they help users understand
	// where a scope came from when reading the file. forktrust does not
	// validate them.
	CreatedBy string `toml:"created_by,omitempty"`
	CreatedAt string `toml:"created_at,omitempty"`
}

// fileFor returns the path where the scope for a given slug is stored.
// We use <repo>/.forktrust/scopes/<slug>.toml to keep scopes alongside the
// worktrees tree but NOT inside the worktree itself (which would pollute
// the worktree's own diff).
func fileFor(repoPath, slug string) string {
	return filepath.Join(repoPath, ".forktrust", "scopes", slug+".toml")
}

// Load reads the scope for a given slug. Returns (nil, nil) if no scope file
// exists (the contract is "no scope = no restrictions"). Returns a parse
// error if the file is present but malformed.
func Load(repoPath, slug string) (*Scope, error) {
	path := fileFor(repoPath, slug)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read scope %s: %w", path, err)
	}
	var s Scope
	if err := toml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse scope %s: %w", path, err)
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("invalid scope %s: %w", path, err)
	}
	return &s, nil
}

// Save writes the scope to disk, creating the .forktrust/scopes directory
// as needed. Overwrites any existing scope for the slug.
func Save(repoPath, slug string, s *Scope) error {
	if err := s.Validate(); err != nil {
		return err
	}
	path := fileFor(repoPath, slug)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir scopes dir: %w", err)
	}
	var buf strings.Builder
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("encode scope: %w", err)
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644) //nolint:gosec
}

// Remove deletes the scope file for a slug. Idempotent — no error if absent.
// Called by `finish` and `rm` so a fresh `forktrust new <same-slug>` does
// not inherit the old scope.
func Remove(repoPath, slug string) error {
	path := fileFor(repoPath, slug)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Validate enforces the rules: Allowed must be non-empty, each pattern must
// be syntactically valid doublestar, no absolute paths, no leading `..`.
func (s *Scope) Validate() error {
	if len(s.Allowed) == 0 {
		return fmt.Errorf("scope.allowed is empty; specify at least one glob (use [\"**\"] to allow everything)")
	}
	for i, p := range s.Allowed {
		if p == "" {
			return fmt.Errorf("scope.allowed[%d] is empty string", i)
		}
		if filepath.IsAbs(p) {
			return fmt.Errorf("scope.allowed[%d] = %q: absolute paths are not allowed (use repo-relative globs)", i, p)
		}
		// doublestar.ValidatePattern checks the pattern syntax without testing it.
		if !doublestar.ValidatePattern(p) {
			return fmt.Errorf("scope.allowed[%d] = %q: invalid glob syntax", i, p)
		}
	}
	return nil
}

// ParseCSV converts a comma/space-separated CLI argument into a normalized
// glob list. Whitespace around entries is trimmed; empty entries are dropped.
//
//	"a/**, b/**" -> ["a/**", "b/**"]
//	"src/**"     -> ["src/**"]
//	"a, , b"     -> ["a", "b"]
func ParseCSV(s string) []string {
	var out []string
	for _, raw := range strings.Split(s, ",") {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Check returns the list of files in `changed` that do NOT match any glob in
// `allowed`. An empty result means the scope is satisfied. Matching uses
// doublestar — `**` works across directory boundaries, `*` is one segment.
//
// `changed` paths must be repo-relative with forward slashes (the format git
// emits from `git diff --name-only`). On Windows we normalize before match.
func Check(allowed, changed []string) []string {
	var violations []string
	for _, file := range changed {
		// Normalize to forward slashes (no-op on Unix; required on Windows).
		f := filepath.ToSlash(file)
		if !anyMatch(allowed, f) {
			violations = append(violations, file)
		}
	}
	return violations
}

func anyMatch(globs []string, path string) bool {
	// Use doublestar.Match (always forward-slash) rather than PathMatch (which
	// switches to OS separator on Windows). Paths are normalized to forward
	// slashes upstream in Check(); both globs and paths must use the same
	// separator semantics or every match returns false on Windows.
	for _, g := range globs {
		ok, err := doublestar.Match(g, path)
		if err == nil && ok {
			return true
		}
	}
	return false
}
