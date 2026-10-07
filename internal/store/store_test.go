package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, root string) Vault {
	t.Helper()
	vault := Vault{Name: filepath.Base(root), Type: "huggingface", Path: root}
	dir := vault.ModelDir("org/model")
	for _, child := range []string{"blobs", "refs", "snapshots/" + strings.Repeat("a", 40), "snapshots/" + strings.Repeat("b", 40)} {
		if err := os.MkdirAll(filepath.Join(dir, child), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{"blobs/weights": "weight bytes", "refs/main": strings.Repeat("a", 40)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, revision := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		if err := os.Symlink("../../blobs/weights", filepath.Join(dir, "snapshots", revision, "weights.bin")); err != nil {
			t.Fatal(err)
		}
	}
	return vault
}

func TestNativeCacheTransfer(t *testing.T) {
	source := fixture(t, filepath.Join(t.TempDir(), "source"))
	destination := Vault{Name: "destination", Type: "huggingface", Path: filepath.Join(t.TempDir(), "destination")}
	model, err := Inspect(source, "org/model")
	if err != nil || len(model.Revisions) != 2 {
		t.Fatalf("inspect: %+v %v", model, err)
	}
	if models, err := Scan(source); err != nil || len(models) != 1 {
		t.Fatalf("scan: %+v %v", models, err)
	}
	if err := Transfer("org/model", source, destination, false, true); err != nil {
		t.Fatal(err)
	}
	sourceInfo, _ := os.Stat(filepath.Join(source.ModelDir("org/model"), "blobs/weights"))
	destinationInfo, _ := os.Stat(filepath.Join(destination.ModelDir("org/model"), "blobs/weights"))
	if os.SameFile(sourceInfo, destinationInfo) {
		t.Fatal("copy shares a weight inode")
	}
	if err := Verify(destination, "org/model"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination.ModelDir("org/model"), "blobs/weights"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Transfer("org/model", source, destination, true, true); err == nil {
		t.Fatal("move accepted a corrupt destination")
	}
	if _, err := os.Stat(source.ModelDir("org/model")); err != nil {
		t.Fatal("failed move removed the source")
	}
}

func TestReferencesAndBoundaries(t *testing.T) {
	source := fixture(t, filepath.Join(t.TempDir(), "source"))
	if err := os.Symlink("/etc/passwd", filepath.Join(source.ModelDir("org/model"), "snapshots", strings.Repeat("a", 40), "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManifest(source, "org/model"); err == nil {
		t.Fatal("accepted a snapshot link outside its repository")
	}
	for _, repo := range []string{"../outside", "org/../model", "org/model/extra", "-option/model", "org/a--b"} {
		if err := ValidateRepo(repo); err == nil {
			t.Fatalf("unsafe repo accepted: %s", repo)
		}
	}
}
