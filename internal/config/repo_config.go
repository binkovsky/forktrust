package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

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
	Hooks Hooks        `toml:"hooks"`
	Ports *PortsConfig `toml:"ports,omitempty"`
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

// Validate checks all hooks have required fields for their type.
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
	return nil
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
