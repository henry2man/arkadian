// Path of the config: ~/.arkadian/config.json (ARK_CONFIG overrides the path).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/henry2man/arkadian/internal/store"
)

const (
	defaultLocalRoot  = "~/ark/spark"
	defaultRemoteRoot = "/volume1/ark"
	defaultRsyncFlags = "-a --inplace --partial"
)

// Config is the ark CLI configuration.
type Config struct {
	Vaults      map[string]store.Vault `json:"vaults"`           // name -> vault
	DefaultFrom string                 `json:"default_from"`     // download source vault
	DefaultTo   string                 `json:"default_to"`       // download destination vault
	Engine      string                 `json:"engine,omitempty"` // old name of Source, still read
	Source      string                 `json:"source,omitempty"` // "" = auto-detect
	RsyncFlags  string                 `json:"rsync_flags"`      // extra flags
}

// SourceName returns the preferred download source ("" = auto-detect).
func (c *Config) SourceName() string {
	if c.Source != "" {
		return c.Source
	}
	return c.Engine
}

// Path returns the config file path. ARK_CONFIG wins. Otherwise use
// ~/.arkadian/config.json, or the old ~/.ark/config.json while it exists.
func Path() string {
	if p := os.Getenv("ARK_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".arkadian", "config.json")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	legacy := filepath.Join(home, ".ark", "config.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return p
}

// Load reads the config. On first use it bootstraps a default file. An old
// ~/.ark/config.json moves to ~/.arkadian/config.json.
func Load() (*Config, error) {
	p := Path()
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		c := Default()
		_ = c.Save()
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.applyDefaults()
	if legacyConfig(p) {
		_ = c.Save() // move to the new home
	}
	return &c, nil
}

// legacyConfig reports whether p is the old ~/.ark/config.json path.
func legacyConfig(p string) bool {
	home, _ := os.UserHomeDir()
	return p == filepath.Join(home, ".ark", "config.json")
}

// Default is the out-of-the-box config: a local tier and one remote tier.
func Default() *Config {
	return &Config{
		Vaults: map[string]store.Vault{
			"spark": {Name: "spark", Kind: "local", Path: defaultLocalRoot},
			"nas":   {Name: "nas", Kind: "remote", Host: "user@nas", Path: defaultRemoteRoot},
		},
		DefaultFrom: "hf",
		DefaultTo:   "nas",
		RsyncFlags:  defaultRsyncFlags,
	}
}

// Save writes the config to the new path, then removes the old file.
func (c *Config) Save() error {
	p := c.path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return err
	}
	if legacy := legacyPath(); legacy != "" && legacy != p {
		_ = os.Remove(legacy) // one move, no split-brain
	}
	return nil
}

// path is where this config should live now: always the new home,
// unless ARK_CONFIG points elsewhere.
func (c *Config) path() string {
	if p := os.Getenv("ARK_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".arkadian", "config.json")
}

// legacyPath returns the old config path when that file exists.
func legacyPath() string {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".ark", "config.json")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func (c *Config) applyDefaults() {
	if c.Vaults == nil {
		c.Vaults = map[string]store.Vault{}
	}
	for n, v := range c.Vaults {
		v.Name = n
		if v.Kind == "" {
			v.Kind = "local"
		}
		v.Path = Expand(v.Path)
		c.Vaults[n] = v
	}
	if c.RsyncFlags == "" {
		c.RsyncFlags = defaultRsyncFlags
	}
}

// Vault resolves a vault by name ("" = defaultTo).
func (c *Config) Vault(name string) (store.Vault, error) {
	if name == "" {
		name = c.DefaultTo
	}
	v, ok := c.Vaults[name]
	if !ok {
		return store.Vault{}, errNotFound(name)
	}
	return v, nil
}

// EnsureRemote prepares a remote vault: creates its models dir over ssh.
func EnsureRemote(v store.Vault, ssh func(host, cmd string) (string, error)) error {
	if !v.Remote() {
		return os.MkdirAll(v.ModelsDir(), 0o755)
	}
	_, err := ssh(v.Host, "mkdir -p "+v.ModelsDir())
	return err
}

// Expand expands a leading ~ in paths.
func Expand(p string) string {
	if len(p) > 1 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

type notFound string

func (e notFound) Error() string { return "vault not found: " + string(e) }

func errNotFound(name string) error { return notFound(name) }
