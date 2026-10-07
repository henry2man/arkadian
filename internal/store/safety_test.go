package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidInventory(test *testing.T) {
	path := filepath.Join(test.TempDir(), "models.json")
	for _, contents := range []string{
		`{"models":{"org/model":{"local":null}}}`,
		`{"models":{"org/model":{"local":{"model":null}}}}`,
		`{"models":{"org/model":{"local":{"model":{"repo":"other/model"}}}}}`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			test.Fatal(err)
		}
		if _, _, err := ReadInventory(path); err == nil {
			test.Fatal("invalid inventory accepted", contents)
		}
	}
}

func TestReferenceDeletionAndInventory(test *testing.T) {
	source := fixture(test, filepath.Join(test.TempDir(), "source"))
	reference := Vault{Name: "reference", Type: "huggingface", Path: filepath.Join(test.TempDir(), "reference")}
	if err := os.MkdirAll(reference.Path, 0o755); err != nil {
		test.Fatal(err)
	}
	if err := os.Symlink(source.ModelDir("org/model"), reference.ModelDir("org/model")); err != nil {
		test.Fatal(err)
	}
	if err := Delete(reference, "org/model", nil); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(source.ModelDir("org/model")); err != nil {
		test.Fatal("reference deletion lost target", err)
	}
	if _, err := EnsureManifest(source, "org/model"); err != nil {
		test.Fatal(err)
	}
	inventory, _, err := ReadInventory(filepath.Join(test.TempDir(), "models.json"))
	if err != nil {
		test.Fatal(err)
	}
	if err := inventory.Refresh(source); err != nil {
		test.Fatal(err)
	}
	if inventory.Models["org/model"][source.Name].State != "verified" {
		test.Fatal("manifest recovery did not retain verification")
	}
	if err := os.Rename(source.Path, source.Path+"-disconnected"); err != nil {
		test.Fatal(err)
	}
	if err := inventory.Refresh(source); err == nil {
		test.Fatal("disconnected vault accepted")
	}
	if inventory.Models["org/model"][source.Name].State != "unknown" {
		test.Fatal("offline copy was lost")
	}
	if err := os.MkdirAll(source.Path, 0o755); err != nil {
		test.Fatal(err)
	}
	if err := inventory.Refresh(source); err != nil {
		test.Fatal(err)
	}
	if inventory.Models["org/model"][source.Name].State != "missing" {
		test.Fatal("successful empty scan did not mark missing")
	}
}

func TestTransferResumesAndSourceChangeProtectsData(test *testing.T) {
	realRsync, err := exec.LookPath("rsync")
	if err != nil {
		test.Fatal(err)
	}
	source := fixture(test, filepath.Join(test.TempDir(), "source"))
	destination := Vault{Name: "destination", Type: "huggingface", Path: filepath.Join(test.TempDir(), "destination")}
	commands := test.TempDir()
	originalPath := os.Getenv("PATH")
	script := fmt.Sprintf("#!/bin/sh\n%s \"$@\"\nexit 7\n", Quote(realRsync))
	if err := os.WriteFile(filepath.Join(commands, "rsync"), []byte(script), 0o755); err != nil {
		test.Fatal(err)
	}
	test.Setenv("PATH", commands+string(os.PathListSeparator)+originalPath)
	if err := Transfer("org/model", source, destination, false, true); err == nil {
		test.Fatal("interruption accepted")
	}
	stage := filepath.Join(destination.Path, ".locks", "ark-staging", "models--org--model")
	if _, err := os.Stat(stage); err != nil {
		test.Fatal("staging not retained", err)
	}
	test.Setenv("PATH", originalPath)
	if err := Transfer("org/model", source, destination, false, true); err != nil {
		test.Fatal(err)
	}
	if err := Verify(destination, "org/model"); err != nil {
		test.Fatal(err)
	}
	second := Vault{Name: "second", Type: "huggingface", Path: filepath.Join(test.TempDir(), "second")}
	script = fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *--version*) exec %s --version ;; esac\n%s \"$@\" || exit\nprintf changed > %s\n", Quote(realRsync), Quote(realRsync), Quote(filepath.Join(source.ModelDir("org/model"), "blobs", "weights")))
	if err := os.WriteFile(filepath.Join(commands, "rsync"), []byte(script), 0o755); err != nil {
		test.Fatal(err)
	}
	test.Setenv("PATH", commands+string(os.PathListSeparator)+originalPath)
	if err := Transfer("org/model", source, second, true, true); err == nil {
		test.Fatal("changed source was deleted")
	}
	if _, err := os.Stat(source.ModelDir("org/model")); err != nil {
		test.Fatal("source lost", err)
	}
	if err := Verify(second, "org/model"); err != nil {
		test.Fatal("verified destination not retained", err)
	}
}

func TestRoomGuard(test *testing.T) {
	if err := RoomOK(1000, 200, 100, false); err != nil {
		test.Fatal(err)
	}
	if err := RoomOK(1000, 200, 101, false); err == nil {
		test.Fatal("90 percent guard ignored")
	}
	if err := RoomOK(1000, 200, 101, true); err != nil {
		test.Fatal(err)
	}
}

func TestTransferRejectsOpenrsync(test *testing.T) {
	source := fixture(test, filepath.Join(test.TempDir(), "source"))
	destination := Vault{Name: "destination", Type: "huggingface", Path: filepath.Join(test.TempDir(), "destination")}
	commands := test.TempDir()
	script := "#!/bin/sh\nprintf 'openrsync: protocol version 29\\nrsync version 2.6.9 compatible\\n'\n"
	if err := os.WriteFile(filepath.Join(commands, "rsync"), []byte(script), 0o755); err != nil {
		test.Fatal(err)
	}
	test.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := Transfer("org/model", source, destination, false, true); err == nil || !strings.Contains(err.Error(), "brew install rsync") {
		test.Fatalf("openrsync accepted: %v", err)
	}
}
