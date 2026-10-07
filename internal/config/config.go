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
	defaultRsyncFlags = "-a --inplace --partial"
)

// Config is the ark CLI configuration.
type Config struct {
	Vaults     map[string]store.Vault `json:"vaults"`           // name -> vault
	DefaultTo  string                 `json:"default_to"`       // download destination vault
	Source     string                 `json:"source,omitempty"` // "" = auto-detect
	RsyncFlags string                 `json:"rsync_flags"`      // extra flags
}

// Path returns the config file path: ARK_CONFIG, else ~/.arkadian/config.json.
func Path() string {
	if p := os.Getenv("ARK_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".arkadian", "config.json")
}

// Load reads the config, bootstrapping a default file on first use.
func Load() (*Config, error) {
	b, err := os.ReadFile(Path())
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

// Default is the out-of-the-box config: one local vault. A cold vault is yours
// to name, so ark does not guess a host: ark vault add nas <user@host> <path>.
func Default() *Config {
	return &Config{
		Vaults: map[string]store.Vault{
			"spark": {Name: "spark", Kind: "local", Path: defaultLocalRoot},
		},
		DefaultTo:  "spark",
		RsyncFlags: defaultRsyncFlags,
	}
}

// Save writes the config to Path().
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
