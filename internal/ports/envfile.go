package ports

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvFileName is the canonical sidecar file forktrust writes port assignments
// into. Most JS/TS frameworks read .env.local automatically; other stacks can
// `source` it.
const EnvFileName = ".env.local"

// RenderEnv builds the file body for a given block + var list. The single
// well-known variable PORT_END is always included so scripts can detect the
// upper bound of the assigned block.
//
// Each name in vars gets the start port. Use multiple names to satisfy several
// frameworks at once (e.g. ["PORT", "SERVER_PORT", "FLASK_RUN_PORT"]).
func RenderEnv(b Block, vars []string) string {
	if len(vars) == 0 {
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

// WriteEnv writes RenderEnv output to <worktreePath>/.env.local. If a
// .env.local already exists and was NOT written by forktrust (no marker
// header), the existing file is preserved untouched and an error is returned
// so the user sees the conflict.
func WriteEnv(worktreePath string, b Block, vars []string) error {
	target := filepath.Join(worktreePath, EnvFileName)
	if existing, err := os.ReadFile(target); err == nil {
		if !strings.HasPrefix(string(existing), "# Managed by forktrust") {
			return fmt.Errorf("%s already exists and was not written by forktrust; refusing to overwrite", target)
		}
	}
	body := RenderEnv(b, vars)
	return os.WriteFile(target, []byte(body), 0o600)
}
