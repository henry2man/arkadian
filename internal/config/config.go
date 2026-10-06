// Package config loads the ark config (~/.ark/config.json by default,
// override with ARK_CONFIG).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/henry2man/ark/internal/store"
)

const (
	defaultLocalRoot  = "~/ark/spark"
	defaultRemoteRoot = "/volume1/ark"
	defaultRsyncFlags = "-a --inplace --partial"
)

// Config is the ark CLI configuration.
type Config struct {
	Vaults      map[string]store.Vault `json:"vaults"`       // name -> vault
	DefaultFrom string                 `json:"default_from"` // download source vault
	DefaultTo   string                 `json:"default_to"`   // download destination vault
	Engine      string                 `json:"engine"`       // "" = auto-detect
	RsyncFlags  string                 `json:"rsync_flags"`  // extra flags
}

// Path returns the config file path.
func Path() string {
	if p := os.Getenv("ARK_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ark", "config.json")
}

// Load reads the config, bootstrapping a default one on first use.
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
	return &c, nil
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

// Save writes the config to disk.
func (c *Config) Save() error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o644)
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
