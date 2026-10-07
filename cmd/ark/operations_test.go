package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/henry2man/arkadian/internal/config"
	"github.com/henry2man/arkadian/internal/store"
)

func TestSyncIsAdditiveAndReportsConflicts(test *testing.T) {
	local, nas := cliFixture(test)
	call(test, 0, "cp", "org/model", "hfcache", "nas")
	other := nas.ModelDir("org/history")
	if err := os.Rename(nas.ModelDir("org/model"), other); err != nil {
		test.Fatal(err)
	}
	call(test, 0, "sync", "hfcache", "nas")
	if _, err := os.Stat(other); err != nil {
		test.Fatal("destination history removed", err)
	}
	if _, err := os.Stat(local.ModelDir("org/history")); !os.IsNotExist(err) {
		test.Fatal("history copied backward")
	}
	if err := os.WriteFile(filepath.Join(nas.ModelDir("org/model"), "blobs", "weights"), []byte("conflict"), 0o644); err != nil {
		test.Fatal(err)
	}
	call(test, 1, "sync", "hfcache", "nas")
	data, err := os.ReadFile(filepath.Join(nas.ModelDir("org/model"), "blobs", "weights"))
	if err != nil || string(data) != "conflict" {
		test.Fatal("conflict overwritten", err)
	}
	if _, err := os.Stat(local.ModelDir("org/model")); err != nil {
		test.Fatal("sync removed source", err)
	}
}

func TestConfigLockAndJSONOutput(test *testing.T) {
	cliFixture(test)
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(config.Path()), ".ark.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		test.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		test.Fatal(err)
	}
	call(test, 1, "refresh")
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		test.Fatal(err)
	}
	output, diagnostics := call(test, 0, "list", "org/model", "--vault", "hfcache", "--json")
	if strings.Contains(output, "free /") || strings.Contains(output, "usage:") {
		test.Fatal("JSON polluted by display", output, diagnostics)
	}
	var inventory store.Inventory
	if err := json.Unmarshal([]byte(output), &inventory); err != nil {
		test.Fatal(err)
	}
	if len(inventory.Models["org/model"]) != 1 {
		test.Fatal("vault filter not applied")
	}
}

func TestRemotePullExecutesAtDestination(test *testing.T) {
	local, nas := cliFixture(test)
	configuration, err := config.Load()
	if err != nil {
		test.Fatal(err)
	}
	nas.Host = "mock-host"
	configuration.Vaults[nas.Name] = nas
	if err := configuration.Save(); err != nil {
		test.Fatal(err)
	}
	commands := test.TempDir()
	ssh := `#!/bin/sh
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
	if err := os.WriteFile(filepath.Join(commands, "ssh"), []byte(ssh), 0o755); err != nil {
		test.Fatal(err)
	}
	realHF, err := exec.LookPath("hf")
	if err != nil {
		test.Fatal(err)
	}
	hf := "#!/bin/sh\nif [ \"$1\" != download ]; then exec " + store.Quote(realHF) + " \"$@\"; fi\n" +
		"shift\nmodel=$1\nshift\ncache=\ndry=no\nwhile [ \"$#\" -gt 0 ]; do\ncase \"$1\" in\n--cache-dir) cache=$2; shift 2 ;;\n--dry-run) dry=yes; shift ;;\n*) shift ;;\nesac\ndone\n" +
		"if [ \"$dry\" = yes ]; then printf '[dry-run] Will download 1 files totalling 5.\\n'; exit 0; fi\n" +
		"exec python3 -c " + store.Quote("import pathlib,shutil,sys; target=pathlib.Path(sys.argv[2])/('models--'+sys.argv[3].replace('/','--')); shutil.copytree(sys.argv[1],target,symlinks=True)") + " " + store.Quote(local.ModelDir("org/model")) + " \"$cache\" \"$model\"\n"
	if err := os.WriteFile(filepath.Join(commands, "hf"), []byte(hf), 0o755); err != nil {
		test.Fatal(err)
	}
	test.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
	call(test, 0, "pull", "hf://org/archive", "nas")
	if _, err := os.Stat(nas.ModelDir("org/archive")); err != nil {
		test.Fatal("no remote download", err)
	}
	if _, err := os.Stat(local.ModelDir("org/archive")); !os.IsNotExist(err) {
		test.Fatal("remote pull staged weights in local cache")
	}
	call(test, 0, "pull", "hf://org/archive", "nas")
	call(test, 1, "pull", "hf://org/archive", "nas", "--rev", strings.Repeat("b", 40))
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"get", "org/archive"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		test.Fatalf("get remote: %d %s", code, stderr.String())
	}
}
