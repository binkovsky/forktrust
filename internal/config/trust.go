package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// TrustEntry records that a repo's .forktrustconfig has been explicitly trusted
// (via `forktrust trust`) to execute command hooks. The SHA pin means any
// change to the config file revokes trust until re-acknowledged, preventing
// a malicious commit from silently injecting shell commands.
type TrustEntry struct {
	Path         string `toml:"path"`
	ConfigSHA256 string `toml:"config_sha256"`
}

// TrustStore is the on-disk format for the trust list.
type TrustStore struct {
	Trusted []TrustEntry `toml:"trusted"`
}

func trustPath() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "forktrust", "trust.toml"), nil
}

// LoadTrust reads the trust store. Returns an empty store if the file is missing.
func LoadTrust() (*TrustStore, error) {
	p, err := trustPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &TrustStore{}, nil
		}
		return nil, err
	}
	var t TrustStore
	if err := toml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &t, nil
}

// Save writes the trust store to disk, creating the directory if needed.
func (t *TrustStore) Save() error {
	p, err := trustPath()
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
	return toml.NewEncoder(f).Encode(t)
}

// Trust records that the .forktrustconfig at repoRoot is trusted. The current
// file's SHA-256 is pinned; any future change to .forktrustconfig revokes
// trust until `forktrust trust` is re-run.
func (t *TrustStore) Trust(repoRoot string) error {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	sum, err := SHA256RepoConfig(abs)
	if err != nil {
		return err
	}
	if sum == "" {
		return fmt.Errorf("no %s found at %s", RepoConfigFile, abs)
	}
	for i := range t.Trusted {
		if t.Trusted[i].Path == abs {
			t.Trusted[i].ConfigSHA256 = sum
			return nil
		}
	}
	t.Trusted = append(t.Trusted, TrustEntry{Path: abs, ConfigSHA256: sum})
	return nil
}

// Revoke removes a repo from the trust store. Returns true if it was present.
func (t *TrustStore) Revoke(repoRoot string) bool {
	abs, _ := filepath.Abs(repoRoot)
	for i := range t.Trusted {
		if t.Trusted[i].Path == abs {
			t.Trusted = append(t.Trusted[:i], t.Trusted[i+1:]...)
			return true
		}
	}
	return false
}

// Check returns trust status for a repo:
//   - trusted=true,  reason=""       : config exists, trust pinned, SHA matches
//   - trusted=false, reason="never trusted"
//   - trusted=false, reason="config changed since trust"
//   - trusted=false, reason="no config"
func (t *TrustStore) Check(repoRoot string) (trusted bool, reason string) {
	abs, _ := filepath.Abs(repoRoot)
	sum, err := SHA256RepoConfig(abs)
	if err != nil || sum == "" {
		return false, "no config"
	}
	for _, e := range t.Trusted {
		if e.Path == abs {
			if e.ConfigSHA256 == sum {
				return true, ""
			}
			return false, "config changed since trust (re-run: forktrust trust)"
		}
	}
	return false, "never trusted (run: forktrust trust)"
}
