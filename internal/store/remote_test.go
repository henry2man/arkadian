package store

import (
	"os"
	"path/filepath"
	"testing"
)

func mockSSH(test *testing.T) {
	test.Helper()
	commands := test.TempDir()
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) shift 2 ;;
    --) shift; break ;;
    -*) shift ;;
    *) break ;;
  esac
done
shift
exec sh -c "$*"
`
	if err := os.WriteFile(filepath.Join(commands, "ssh"), []byte(script), 0o755); err != nil {
		test.Fatal(err)
	}
	test.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRemoteTransfersAndPhysicalAliases(test *testing.T) {
	mockSSH(test)
	source := fixture(test, filepath.Join(test.TempDir(), "source with 'quotes'"))
	remote := Vault{Name: "remote", Type: "huggingface", Host: "remote-host", Path: filepath.Join(test.TempDir(), "remote with spaces")}
	if err := Transfer("org/model", source, remote, false, true); err != nil {
		test.Fatal(err)
	}
	if models, err := Scan(remote); err != nil || len(models) != 1 {
		test.Fatalf("remote scan: %+v %v", models, err)
	}
	second := Vault{Name: "second", Type: "huggingface", Host: "second-host", Path: filepath.Join(test.TempDir(), "second")}
	if err := Transfer("org/model", remote, second, false, true); err != nil {
		test.Fatal(err)
	}
	local := Vault{Name: "local", Type: "huggingface", Path: filepath.Join(test.TempDir(), "local")}
	if err := Transfer("org/model", second, local, true, true); err != nil {
		test.Fatal(err)
	}
	if err := Verify(local, "org/model"); err != nil {
		test.Fatal(err)
	}
	alias := remote
	alias.Host = "another-alias"
	if err := Transfer("org/model", remote, alias, true, true); err == nil {
		test.Fatal("same physical remote model accepted under two host aliases")
	}
	if _, err := os.Stat(remote.ModelDir("org/model")); err != nil {
		test.Fatal("alias move deleted both copies", err)
	}
}

func TestRemoteFailurePreservesInventory(test *testing.T) {
	source := fixture(test, filepath.Join(test.TempDir(), "source"))
	inventory, _, err := ReadInventory(filepath.Join(test.TempDir(), "models.json"))
	if err != nil {
		test.Fatal(err)
	}
	if err := inventory.Refresh(source); err != nil {
		test.Fatal(err)
	}
	commands := test.TempDir()
	if err := os.WriteFile(filepath.Join(commands, "ssh"), []byte("#!/bin/sh\necho 'connection refused' >&2\nexit 255\n"), 0o755); err != nil {
		test.Fatal(err)
	}
	test.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
	source.Host = "unreachable"
	if err := inventory.Refresh(source); err == nil {
		test.Fatal("SSH failure accepted")
	}
	if inventory.Models["org/model"][source.Name].State != "unknown" {
		test.Fatal("failed SSH scan lost known copy")
	}
}
