// Package config handles the forktrust project registry stored at
// $XDG_CONFIG_HOME/forktrust/config.toml (~/.config/forktrust/config.toml on Linux/macOS).
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Project is a single registered git repository.
type Project struct {
	Name       string `toml:"name"`
	Path       string `toml:"path"`
	MainBranch string `toml:"main_branch,omitempty"` // defaults to "main"
	InstallCmd string `toml:"install_cmd,omitempty"` // defaults to "npm install" when --install is passed
}

// Config is the full registry.
type Config struct {
	Projects []Project `toml:"project"`
	AI       AIConfig  `toml:"ai,omitempty"`
}

// AIConfig holds user defaults for the AI adapter system.
// Lives at top level so `forktrust config set ai.default claude` is intuitive.
type AIConfig struct {
	Default string `toml:"default,omitempty"`
}

// Path returns the canonical config file path.
func Path() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "forktrust", "config.toml"), nil
}

// Load reads the config file. Returns an empty Config if the file is missing
// (so the tool works out-of-the-box with no setup).
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &c, nil
}

// Save writes the config to disk, creating the directory if needed.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// FindByName returns the project with the given name, or nil.
func (c *Config) FindByName(name string) *Project {
	for i := range c.Projects {
		if c.Projects[i].Name == name {
			return &c.Projects[i]
		}
	}
	return nil
}

// AllProjects returns the registered projects, or a single anonymous entry for
// the current working directory if no registry exists (so commands work in a
// single repo without any setup).
func (c *Config) AllProjects() []Project {
	if len(c.Projects) > 0 {
		return c.Projects
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	// Walk up to find a .git dir so commands work from subdirectories.
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return []Project{{Name: filepath.Base(dir), Path: dir}}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

// Add appends a project, deduping by name.
func (c *Config) Add(p Project) error {
	if c.FindByName(p.Name) != nil {
		return fmt.Errorf("project %q already registered", p.Name)
	}
	c.Projects = append(c.Projects, p)
	return nil
}

// Remove drops a project by name. Returns true if removed.
func (c *Config) Remove(name string) bool {
	for i := range c.Projects {
		if c.Projects[i].Name == name {
			c.Projects = append(c.Projects[:i], c.Projects[i+1:]...)
			return true
		}
	}
	return false
}
