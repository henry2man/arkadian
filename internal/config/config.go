package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/henry2man/arkadian/internal/store"
)

type Config struct {
	Vaults       map[string]store.Vault `json:"vaults"`
	DefaultVault string                 `json:"default_vault"`
}

func Path() string {
	if value := os.Getenv("ARK_CONFIG"); value != "" {
		return Expand(value)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".arkadian", "config.json")
}

func InventoryPath() string { return filepath.Join(filepath.Dir(Path()), "models.json") }

func Expand(value string) string {
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, _ := os.UserHomeDir()
		if value == "~" {
			return home
		}
		return filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	return value
}

func HFCache() string {
	for _, name := range []string{"HF_HUB_CACHE", "HUGGINGFACE_HUB_CACHE"} {
		if value := os.Getenv(name); value != "" {
			return Expand(value)
		}
	}
	if value := os.Getenv("HF_HOME"); value != "" {
		return filepath.Join(Expand(value), "hub")
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = Expand("~/.cache")
	}
	return filepath.Join(Expand(cache), "huggingface", "hub")
}

func Default() *Config {
	return &Config{DefaultVault: "hfcache", Vaults: map[string]store.Vault{
		"hfcache": {Name: "hfcache", Type: "huggingface", Path: HFCache()},
	}}
}

func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		result := Default()
		if err := result.Validate(); err != nil {
			return nil, err
		}
		return result, result.Save()
	}
	if err != nil {
		return nil, err
	}
	var result Config
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return &result, nil
}

func (config *Config) Validate() error {
	if len(config.Vaults) == 0 {
		return fmt.Errorf("no vaults configured; register your HF cache")
	}
	for name, vault := range config.Vaults {
		if err := store.ValidateName(name); err != nil {
			return err
		}
		vault.Name = name
		if vault.Path == "" {
			return fmt.Errorf("vault %s has no path", name)
		}
		if !vault.Remote() {
			var err error
			vault.Path, err = filepath.Abs(Expand(vault.Path))
			if err != nil {
				return err
			}
		}
		if err := vault.Validate(); err != nil {
			return fmt.Errorf("vault %s: %w", name, err)
		}
		config.Vaults[name] = vault
	}
	if _, exists := config.Vaults[config.DefaultVault]; !exists {
		return fmt.Errorf("default_vault %q is not configured", config.DefaultVault)
	}
	if config.Vaults[config.DefaultVault].Remote() {
		return fmt.Errorf("default_vault must be accessible on this machine")
	}
	return nil
}

func (config *Config) Save() error {
	if err := config.Validate(); err != nil {
		return err
	}
	return store.AtomicJSON(Path(), config)
}

func (config *Config) Vault(name string) (store.Vault, error) {
	if name == "" {
		name = config.DefaultVault
	}
	vault, exists := config.Vaults[name]
	if !exists {
		return vault, fmt.Errorf("unknown vault %q; run: ark vault ls", name)
	}
	return vault, nil
}

func ParseLocation(name, location string) (store.Vault, error) {
	vault := store.Vault{Name: name, Type: "huggingface"}
	if strings.Contains(location, "://") {
		parsed, err := url.Parse(location)
		if err != nil || parsed.Scheme != "ssh" || parsed.Host == "" || parsed.Path == "" || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return vault, fmt.Errorf("use a mounted path, host:/path, or ssh://host/path; configure SSH ports in ~/.ssh/config")
		}
		if parsed.User != nil {
			if _, password := parsed.User.Password(); password {
				return vault, fmt.Errorf("configure SSH keys, not passwords in vault locations")
			}
			vault.Host = parsed.User.Username() + "@"
		}
		vault.Host += parsed.Host
		vault.Path = parsed.Path
	} else if host, remotePath, found := strings.Cut(location, ":"); found && !strings.HasPrefix(location, "/") {
		vault.Host, vault.Path = host, remotePath
	} else {
		if location == "" {
			return vault, fmt.Errorf("vault location is empty")
		}
		var err error
		vault.Path, err = filepath.Abs(Expand(location))
		if err != nil {
			return vault, err
		}
	}
	return vault, vault.Validate()
}
