package ports

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/binkovsky/forktrust/internal/pathsafe"
)

// EnvFileName is the canonical sidecar file forktrust writes port assignments
// into. Most JS/TS frameworks read .env.local automatically; other stacks can
// `source` it.
const EnvFileName = ".env.local"

// ManagedHeader is the EXACT first line forktrust writes into every .env.local
// it generates. Used as ownership proof: a file is forktrust-managed IFF its
// very first line (including the trailing newline) matches this string exactly.
//
// This constant is the single source of truth shared by:
//   - RenderEnv (writes the header)
//   - WriteEnv (reads back to detect forktrust-owned files before overwrite)
//   - git.IgnoredCount / git.isForktrustManaged (detects ownership to skip
//     the ignored-file deletion guard)
//
// Do NOT change this string without also rotating the worktrees that already
// have .env.local files written with the old header.
const ManagedHeader = "# Managed by forktrust. Do not edit; values are overwritten on each `forktrust new`.\n"

// RenderEnv builds the file body for a given block + var list. The single
// well-known variable PORT_END is always included so scripts can detect the
// upper bound of the assigned block.
//
// Each name in vars gets the start port. Use multiple names to satisfy several
// frameworks at once (e.g. ["PORT", "SERVER_PORT", "FLASK_RUN_PORT"]).
//
// vars is interpreted positionally: nil/missing means "use the default PORT",
// non-nil with len==0 means "user explicitly opted out of writing any
// user-named port variable — only emit PORT_END + FORKTRUST_*". This lets
// SanitizedPortsVars return [] without stealth-injecting PORT (R4 fix).
func RenderEnv(b Block, vars []string) string {
	if vars == nil {
		vars = []string{"PORT"}
	}
	var sb strings.Builder
	sb.WriteString("# Managed by forktrust. Do not edit; values are overwritten on each `forktrust new`.\n")
	sb.WriteString("# Block released automatically on `forktrust finish` / `forktrust rm`.\n")
	for _, v := range vars {
		fmt.Fprintf(&sb, "%s=%d\n", v, b.Start)
	}
	fmt.Fprintf(&sb, "PORT_END=%d\n", b.End())
	fmt.Fprintf(&sb, "FORKTRUST_PORT_START=%d\n", b.Start)
	fmt.Fprintf(&sb, "FORKTRUST_PORT_END=%d\n", b.End())
	fmt.Fprintf(&sb, "FORKTRUST_PORT_SIZE=%d\n", b.Size)
	return sb.String()
}

// WriteEnv writes RenderEnv output to <worktreePath>/.env.local. Refuses if:
//   - SafeJoin would escape worktreePath (lexical or symlink ancestor)
//   - The path exists as a regular file NOT written by forktrust (preserves
//     user-authored env files)
//   - At write time, the target is a symlink (O_NOFOLLOW; closes the TOCTOU
//     window between any earlier check and the actual write)
//
// The pre-check (existing-content marker) uses Lstat to avoid following a
// symlink; the actual write uses pathsafe.SafeWriteFile which opens with
// O_NOFOLLOW for atomic symlink refusal at the syscall boundary.
func WriteEnv(worktreePath string, b Block, vars []string) error {
	target := filepath.Join(worktreePath, EnvFileName)

	// Pre-flight: preserve a user-authored .env.local. Use Lstat so a
	// symlinked .env.local is treated as "exists but not ours" without
	// dereferencing it (ReadFile would follow). The post-check via
	// SafeWriteFile's O_NOFOLLOW is the actual security guarantee.
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; refusing to write (would escape worktree)", target)
		}
		// Regular file: verify ownership by checking the exact first line.
		// We use strings.HasPrefix with the full ManagedHeader (including the
		// trailing newline) rather than a bare prefix, so a user file whose
		// content happens to start with "# Managed by forktrust" but continues
		// with different text is NOT treated as ours and is preserved.
		if data, err := os.ReadFile(target); err == nil {
			if !strings.HasPrefix(string(data), ManagedHeader) {
				return fmt.Errorf("%s already exists and was not written by forktrust; refusing to overwrite", target)
			}
		}
	}
	body := RenderEnv(b, vars)
	return pathsafe.SafeWriteFile(worktreePath, EnvFileName, []byte(body), 0o600)
}
