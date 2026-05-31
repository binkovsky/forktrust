// Package predict surfaces cross-worktree edit overlap so users learn early
// (at `forktrust new` time) which files are also being touched in other
// active worktrees of the same repo. The aim is to head off avoidable merge
// conflicts at `finish` time.
package predict

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/binkovsky/forktrust/internal/git"
)

// Overlap describes which other worktrees are touching a given file.
type Overlap struct {
	File  string   // path relative to repo root
	Slugs []string // worktree slugs currently editing this file (sorted)
}

// ignoredFiles is the set of paths that are forktrust-managed and therefore
// uninteresting for cross-worktree overlap warnings (every worktree will have
// its own, so they always overlap and the warning is noise).
var ignoredFiles = map[string]struct{}{
	".env.local":             {},
	".forktrust-history.log": {},
}

// Active inspects every worktree under `repoRoot` (except the main checkout
// and any slug matching `excludeSlug`) and returns the union of files they
// have either:
//   - committed on top of refspec (default "origin/<mainBranch>"), or
//   - modified or added in the working tree
//
// Files known to be forktrust-managed (.env.local, etc) are filtered out.
// Returned overlaps are sorted by file path for stable output.
func Active(repoRoot, mainBranch, excludeSlug string) ([]Overlap, error) {
	if mainBranch == "" {
		mainBranch = "main"
	}
	refspec := "origin/" + mainBranch

	wts, err := git.ListWorktrees(repoRoot)
	if err != nil {
		return nil, err
	}

	canon := canonicalPath(repoRoot)

	fileToSlugs := map[string]map[string]struct{}{}
	for _, wt := range wts {
		if canonicalPath(wt.Path) == canon {
			continue
		}
		slug := filepath.Base(wt.Path)
		if slug == excludeSlug {
			continue
		}
		files, err := git.ChangedFiles(wt.Path, refspec)
		if err != nil {
			continue
		}
		for _, f := range files {
			if _, skip := ignoredFiles[f]; skip {
				continue
			}
			if _, ok := fileToSlugs[f]; !ok {
				fileToSlugs[f] = map[string]struct{}{}
			}
			fileToSlugs[f][slug] = struct{}{}
		}
	}

	out := make([]Overlap, 0, len(fileToSlugs))
	for file, slugs := range fileToSlugs {
		ss := make([]string, 0, len(slugs))
		for s := range slugs {
			ss = append(ss, s)
		}
		sort.Strings(ss)
		out = append(out, Overlap{File: file, Slugs: ss})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
}

// canonicalPath returns p with all symlinks resolved, or the cleaned path on
// error. Used to compare paths reliably across macOS's /var -> /private/var
// (and similar) indirections.
func canonicalPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			return resolved
		}
		return abs
	}
	return filepath.Clean(p)
}

// FormatWarning renders a short human-readable banner from overlap data.
// Returns empty string when there's nothing to warn about.
func FormatWarning(overlaps []Overlap, max int) string {
	if len(overlaps) == 0 {
		return ""
	}
	if max <= 0 {
		max = 5
	}
	slugSet := map[string]struct{}{}
	for _, o := range overlaps {
		for _, s := range o.Slugs {
			slugSet[s] = struct{}{}
		}
	}

	var sb strings.Builder
	plural := "s"
	if len(overlaps) == 1 {
		plural = ""
	}
	worktreesNoun := "worktree"
	if len(slugSet) != 1 {
		worktreesNoun = "worktrees"
	}
	sb.WriteString("\n")
	sb.WriteString("!  ")
	sb.WriteString(formatCount(len(overlaps), "file"+plural))
	sb.WriteString(" also being edited in ")
	sb.WriteString(formatCount(len(slugSet), worktreesNoun))
	sb.WriteString(" — `forktrust finish` may conflict:\n")
	for i, o := range overlaps {
		if i >= max {
			sb.WriteString("   ... ")
			sb.WriteString(formatCount(len(overlaps)-max, "more"))
			sb.WriteString(" (run: forktrust status)\n")
			break
		}
		sb.WriteString("   ")
		sb.WriteString(o.File)
		sb.WriteString("  (in ")
		sb.WriteString(strings.Join(o.Slugs, ", "))
		sb.WriteString(")\n")
	}
	return sb.String()
}

func formatCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return intoa(n) + " " + noun
}

func intoa(n int) string {
	// avoid importing strconv just for this; n is always non-negative here
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
