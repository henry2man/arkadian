package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/henry2man/arkadian/internal/config"
	"github.com/henry2man/arkadian/internal/store"
)

func cliFixture(t *testing.T) (store.Vault, store.Vault) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("ARK_CONFIG", filepath.Join(root, "config.json"))
	local := store.Vault{Name: "hfcache", Type: "huggingface", Path: filepath.Join(root, "hf")}
	nas := store.Vault{Name: "nas", Type: "huggingface", Path: filepath.Join(root, "nas")}
	for _, vault := range []store.Vault{local, nas} {
		if err := os.MkdirAll(vault.Path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := local.ModelDir("org/model")
	for _, child := range []string{"blobs", "refs", "snapshots/" + strings.Repeat("a", 40)} {
		if err := os.MkdirAll(filepath.Join(dir, child), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"blobs/weights": "bytes", "refs/main": strings.Repeat("a", 40)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../../blobs/weights", filepath.Join(dir, "snapshots", strings.Repeat("a", 40), "model.bin")); err != nil {
		t.Fatal(err)
	}
	setup := &config.Config{DefaultVault: "hfcache", Vaults: map[string]store.Vault{"hfcache": local, "nas": nas}}
	if err := setup.Save(); err != nil {
		t.Fatal(err)
	}
	return local, nas
}

func call(t *testing.T, expected int, args ...string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(args, strings.NewReader(""), &stdout, &stderr)
	if code != expected {
		t.Fatalf("%v exit %d, want %d; stdout=%s stderr=%s", args, code, expected, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func TestCLIWorkflow(t *testing.T) {
	local, nas := cliFixture(t)
	output, diagnostics := call(t, 0, "list", "org/model")
	if !strings.Contains(output, "hfcache") || !strings.Contains(output, "org/model") {
		t.Fatal(output, diagnostics)
	}
	call(t, 2, "cp", "org/model", "--from", "hfcache", "--to", "nas")
	call(t, 2, "pull", "hf://org/model")
	call(t, 1, "rm", "org/model", "hfcache", "--yes")
	call(t, 0, "evict", "org/model", "nas", "--yes")
	if _, err := os.Stat(local.ModelDir("org/model")); !os.IsNotExist(err) {
		t.Fatalf("evict retained source: %v", err)
	}
	call(t, 0, "get", "org/model")
	path, _ := call(t, 0, "path", "org/model")
	if strings.TrimSpace(path) != filepath.Join(local.ModelDir("org/model"), "snapshots", strings.Repeat("a", 40)) {
		t.Fatal(path)
	}
	call(t, 0, "verify", "org/model")
	if err := os.WriteFile(filepath.Join(nas.ModelDir("org/model"), "blobs/weights"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, 1, "evict", "org/model", "nas", "--yes")
	if _, err := os.Stat(local.ModelDir("org/model")); err != nil {
		t.Fatal("corrupt target lost local model")
	}
}

func TestCLIHelpAndFlags(t *testing.T) {
	for _, command := range []string{"list", "refresh", "pull", "cp", "mv", "get", "evict", "rm", "sync", "verify", "path", "vault", "version"} {
		call(t, 0, command, "--help")
	}
	for _, args := range [][]string{{"get"}, {"sync", "one"}, {"list", "--json=maybe"}, {"cp", "org/model", "one", "two", "--vault", "three"}, {"get", "org/model", "--copy"}} {
		call(t, 2, args...)
	}
}

func TestGetMaterializesReference(test *testing.T) {
	local, nas := cliFixture(test)
	if err := os.Rename(local.ModelDir("org/model"), nas.ModelDir("org/model")); err != nil {
		test.Fatal(err)
	}
	if err := os.Symlink(nas.ModelDir("org/model"), local.ModelDir("org/model")); err != nil {
		test.Fatal(err)
	}
	call(test, 0, "get", "org/model")
	localInfo, err := os.Lstat(local.ModelDir("org/model"))
	if err != nil {
		test.Fatal(err)
	}
	if localInfo.Mode()&os.ModeSymlink != 0 {
		test.Fatal("get retained a reference instead of copying weights")
	}
	if _, err := os.Stat(nas.ModelDir("org/model")); err != nil {
		test.Fatal("get removed source", err)
	}
}
