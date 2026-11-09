package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// DirName is the directory under the user home holding the config file.
	DirName = ".newapi-cli"
	// FileName is the config file name inside DirName.
	FileName = "config.json"
	// EnvProfile overrides the active profile name.
	EnvProfile = "NEWAPI_CLI_PROFILE"
	// DefaultProfile is used when neither --profile nor EnvProfile is set.
	DefaultProfile = "default"
)

// Profile holds the credentials of one server profile.
type Profile struct {
	Server    string `json:"server"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// Config is the on-disk config file structure.
type Config struct {
	Profiles map[string]*Profile `json:"profiles"`
}

// DefaultPath returns ~/.newapi-cli/config.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, DirName, FileName), nil
}

// Load reads the config at path. A missing file yields an empty config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Profiles: map[string]*Profile{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]*Profile{}
	}
	return &cfg, nil
}

// Save writes the config to path with 0600 permissions inside a 0700
// directory. Permissions are best-effort on Windows.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	// WriteFile applies the permission only on creation; enforce it for
	// pre-existing files too.
	_ = os.Chmod(path, 0o600)
	return nil
}

// Profile returns the named profile, or nil when absent.
func (c *Config) Profile(name string) *Profile {
	return c.Profiles[name]
}

// SetProfile upserts the named profile.
func (c *Config) SetProfile(name string, p *Profile) {
	c.Profiles[name] = p
}

// DeleteProfile removes the named profile if present.
func (c *Config) DeleteProfile(name string) {
	delete(c.Profiles, name)
}
