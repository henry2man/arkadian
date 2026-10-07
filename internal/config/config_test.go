package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHFCacheAndConfig(t *testing.T) {
	for _, name := range []string{"HF_HUB_CACHE", "HUGGINGFACE_HUB_CACHE", "HF_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(name, "")
	}
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg")
	if got := HFCache(); got != "/tmp/xdg/huggingface/hub" {
		t.Fatalf("XDG cache: %s", got)
	}
	t.Setenv("HF_HOME", "/tmp/hf")
	t.Setenv("HUGGINGFACE_HUB_CACHE", "/tmp/old")
	t.Setenv("HF_HUB_CACHE", "/tmp/current")
	if got := Default().Vaults["hfcache"]; got.Path != "/tmp/current" || got.Type != "huggingface" {
		t.Fatalf("default vault: %+v", got)
	}
	t.Setenv("ARK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefaultVault != "hfcache" || InventoryPath() != filepath.Join(filepath.Dir(Path()), "models.json") {
		t.Fatalf("unexpected config: %+v", loaded)
	}
	if err := os.WriteFile(Path(), []byte(`{"vaults":{"nas":{"path":"/data"}},"default_vault":"nas"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("missing vault type accepted")
	}
	if err := os.WriteFile(Path(), []byte(`{"vaults":{"hfcache":{"type":"huggingface","path":""}},"default_vault":"hfcache"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("empty cache path accepted")
	}
}

func TestLocations(t *testing.T) {
	for _, location := range []string{"/tmp/models", "user@nas:/data/models", "ssh://user@nas/data/models"} {
		vault, err := ParseLocation("nas", location)
		if err != nil || vault.Type != "huggingface" {
			t.Fatalf("%s: %+v %v", location, vault, err)
		}
	}
	for _, location := range []string{"smb://nas/models", "ssh://nas", "-host:/data", "ssh://nas:2222/data"} {
		if _, err := ParseLocation("nas", location); err == nil {
			t.Fatalf("unsafe or unsupported location accepted: %s", location)
		}
	}
}
