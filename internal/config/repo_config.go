package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/BurntSushi/toml"
)

// envVarNameRE is the POSIX env var name pattern: a letter or underscore
// followed by letters/digits/underscores. Used to reject names that would
// produce broken (or injection-prone) lines in .env.local — e.g. names
// containing newlines, "=", spaces, or shell metacharacters.
var envVarNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedEnvVarNames are produced by forktrust itself; users may not
// override them to keep the .env.local schema stable for tooling.
var reservedEnvVarNames = map[string]struct{}{
	"PORT_END":             {},
	"FORKTRUST_PORT_START": {},
	"FORKTRUST_PORT_END":   {},
	"FORKTRUST_PORT_SIZE":  {},
}

// RepoConfigFile is the canonical filename for the per-repo config that lives
// at the repository root (committed to the repo).
const RepoConfigFile = ".forktrustconfig"

// RepoConfig is the schema for .forktrustconfig.
//
// Modeled after gwq's [[repository_settings]] / wtp's .wtp.yml: a list of
// post_create hooks that fire after `forktrust new <slug>` creates the
// worktree. Hooks run in declared order; if one fails, subsequent hooks are
// skipped and the worktree is left in place for inspection.
type RepoConfig struct {
	Hooks  Hooks         `toml:"hooks"`
	Ports  *PortsConfig  `toml:"ports,omitempty"`
	Verify *VerifyConfig `toml:"verify,omitempty"`
}

// VerifyConfig declares commands that MUST exit zero before `forktrust finish`
// is allowed to merge a worktree into main. When present, the verify gate runs
// in the finish pre-flight (before any git mutation). If any command exits
// non-zero, finish refuses with exit 15 (ExitVerifyFailed) and no commit,
// merge, push, or branch operation happens.
//
// Bypass with `forktrust finish --no-verify` (prints a warning to stderr).
//
// Example:
//
//	[verify]
//	commands      = ["go build ./...", "go test ./...", "go vet ./..."]
//	require_clean = true
//
// require_clean: if true, after all commands pass, the worktree must still be
// clean (`git status --porcelain` returns no entries). This catches verify
// commands that write build artifacts or generated files that were not in
// .gitignore — the merge would otherwise carry uncommitted byproducts.
type VerifyConfig struct {
	// Commands is the list of shell commands to run in order. Each runs via
	// `sh -c <command>` with cwd set to the worktree root and the worktree's
	// .env.local pre-parsed into env (KEY=VALUE; no shell eval — same as
	// command hooks).
	Commands []string `toml:"commands"`
	// RequireClean: if true, refuse if the worktree is dirty after verify runs.
	RequireClean bool `toml:"require_clean,omitempty"`
	// TimeoutSeconds bounds EACH command's wall-clock duration. Default 600
	// (10 minutes per command). Set to 0 explicitly to disable the timeout
	// (not recommended — a hung verify command blocks finish indefinitely).
	TimeoutSeconds int `toml:"timeout_seconds,omitempty"`
}

// PortsConfig declares an aligned port-block allocation policy for this repo.
// When present, `forktrust new` allocates a block and writes a .env.local file
// in the worktree exposing the assigned port(s) to the dev process. The block
// is auto-released on `forktrust finish` / `forktrust rm`.
type PortsConfig struct {
	// Range is "MIN-MAX" inclusive; default "3000-3999".
	Range string `toml:"range,omitempty"`
	// Size is the number of ports per worktree block; default 10.
	Size int `toml:"size,omitempty"`
	// Vars is the list of environment variable names to receive the start
	// port. PORT_END always gets end+1's predecessor. Default ["PORT"].
	Vars []string `toml:"vars,omitempty"`
}

// Hooks groups hook lifecycles. Currently only post_create is supported;
// pre_create / post_remove are reserved for future versions.
type Hooks struct {
	PostCreate []Hook `toml:"post_create"`
}

// Hook is a single declarative action.
type Hook struct {
	// Type is one of: "copy", "symlink", "command".
	Type string `toml:"type"`

	// For type=copy / type=symlink: source path relative to the MAIN
	// worktree (repo root), destination path relative to the NEW worktree.
	From string `toml:"from,omitempty"`
	To   string `toml:"to,omitempty"`

	// For type=command: the shell command to run via `sh -c`.
	// Templates expand: {{.Branch}} {{.Slug}} {{.Path}} {{.MainPath}} {{.Project}}
	Run string `toml:"run,omitempty"`

	// For type=command: optional working directory (relative to new worktree).
	WorkDir string `toml:"work_dir,omitempty"`

	// For type=command: optional environment variable overrides.
	Env map[string]string `toml:"env,omitempty"`
}

// HookType constants for stable comparison.
const (
	HookCopy    = "copy"
	HookSymlink = "symlink"
	HookCommand = "command"
)

// LoadRepoConfig reads .forktrustconfig from the repo root. Returns
// (nil, nil) if the file is absent (zero-config repos are first-class).
func LoadRepoConfig(repoRoot string) (*RepoConfig, error) {
	path := filepath.Join(repoRoot, RepoConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var c RepoConfig
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	return &c, nil
}

// Validate checks all hooks have required fields for their type, and that
// [ports].vars contains only well-formed, non-duplicate, non-reserved POSIX
// env var names.
func (c *RepoConfig) Validate() error {
	for i, h := range c.Hooks.PostCreate {
		switch h.Type {
		case HookCopy, HookSymlink:
			if h.From == "" || h.To == "" {
				return fmt.Errorf("hook %d (%s): from and to are required", i, h.Type)
			}
			if h.Run != "" {
				return fmt.Errorf("hook %d (%s): run is not valid for this type", i, h.Type)
			}
		case HookCommand:
			if h.Run == "" {
				return fmt.Errorf("hook %d (command): run is required", i)
			}
			if h.From != "" || h.To != "" {
				return fmt.Errorf("hook %d (command): from/to are not valid for this type", i)
			}
		case "":
			return fmt.Errorf("hook %d: type is required (one of: copy, symlink, command)", i)
		default:
			return fmt.Errorf("hook %d: unknown type %q (one of: copy, symlink, command)", i, h.Type)
		}
	}
	if c.Ports != nil {
		// Hard-fail on the actual security issue (regex violations =
		// newline/equals/space injection into .env.local). Soft-skip
		// duplicates and reserved names — those were silently accepted
		// before v0.6.2 R1, so existing repos must not break on upgrade.
		// SanitizedVars() is what writers should use to get the filtered
		// list at runtime.
		for i, v := range c.Ports.Vars {
			if !envVarNameRE.MatchString(v) {
				return fmt.Errorf("[ports].vars[%d] = %q: must match %s (POSIX env var name; newline/equals/space rejected to prevent .env.local injection)", i, v, envVarNameRE.String())
			}
		}
	}
	if c.Verify != nil {
		if len(c.Verify.Commands) == 0 {
			return fmt.Errorf("[verify] section present but commands is empty; either remove the section or list at least one command")
		}
		for i, cmd := range c.Verify.Commands {
			if cmd == "" {
				return fmt.Errorf("[verify].commands[%d] is empty; remove it or replace with a real shell command", i)
			}
		}
		if c.Verify.TimeoutSeconds < 0 {
			return fmt.Errorf("[verify].timeout_seconds must be >= 0 (got %d); set to 0 to disable, or a positive number of seconds", c.Verify.TimeoutSeconds)
		}
	}
	return nil
}

// SanitizedPortsVars returns the user's [ports].vars list with duplicates
// removed and reserved names dropped, alongside a list of warnings the
// caller should surface to the user. The regex check ran at parse time, so
// any entry here is already a well-formed env var name.
//
// This is a softer fallback than rejecting at parse time so repos that had
// `vars=["PORT_END"]` from before v0.6.2 R1 keep working after upgrade.
func (c *RepoConfig) SanitizedPortsVars() (vars []string, warnings []string) {
	if c == nil || c.Ports == nil {
		return nil, nil
	}
	// IMPORTANT: when the user SET the vars field at all (even to []),
	// return an EMPTY slice (not nil). RenderEnv distinguishes nil ("user
	// didn't specify, use default PORT") from []string{} ("user explicitly
	// opted out via filter or empty list; emit nothing user-named").
	//
	// R5 fix: previously gated on `len(c.Ports.Vars) > 0`, so the natural
	// opt-out syntax `vars = []` (TOML empty array) returned nil and
	// stealth-injected PORT. Now: if user wrote any `vars` field at all
	// (BurntSushi/toml gives us non-nil [] for that), respect the opt-out.
	if c.Ports.Vars != nil {
		vars = []string{}
	}
	seen := map[string]struct{}{}
	for _, v := range c.Ports.Vars {
		if _, reserved := reservedEnvVarNames[v]; reserved {
			warnings = append(warnings, fmt.Sprintf("[ports].vars: %q is reserved (forktrust always writes this); skipping the duplicate user entry", v))
			continue
		}
		if _, dup := seen[v]; dup {
			warnings = append(warnings, fmt.Sprintf("[ports].vars: %q appears more than once; keeping the first occurrence", v))
			continue
		}
		seen[v] = struct{}{}
		vars = append(vars, v)
	}
	return vars, warnings
}

// HasCommandHooks reports whether any post_create hook executes a shell command.
// Used by the trust gate: copy/symlink hooks are safe (file ops scoped to the
// worktree), command hooks require explicit trust.
func (c *RepoConfig) HasCommandHooks() bool {
	if c == nil {
		return false
	}
	for _, h := range c.Hooks.PostCreate {
		if h.Type == HookCommand {
			return true
		}
	}
	return false
}

// SHA256 computes the SHA-256 of the .forktrustconfig file at the given repo
// root. Returns empty string and nil error if the file does not exist.
func SHA256RepoConfig(repoRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, RepoConfigFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
