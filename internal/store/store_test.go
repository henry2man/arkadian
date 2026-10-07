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
	if models, _, err := Scan(source); err != nil || len(models) != 1 {
		t.Fatalf("scan: %+v %v", models, err)
	}
	if _, err := Transfer("org/model", source, destination, false, true); err != nil {
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
	if _, err := Transfer("org/model", source, destination, true, true); err == nil {
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

func TestSharedBlobStoreLinksTransfer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	source := fixture(t, root)
	repo := source.ModelDir("org/model")
	shared := filepath.Join(root, "blobs", "72")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(shared, strings.Repeat("7", 64))
	if err := os.WriteFile(blob, []byte("weight bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "blobs", "weights")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../blobs/72/"+strings.Repeat("7", 64), filepath.Join(repo, "blobs", "weights")); err != nil {
		t.Fatal(err)
	}
	destination := Vault{Name: "destination", Type: "huggingface", Path: filepath.Join(t.TempDir(), "destination")}
	if _, err := Transfer("org/model", source, destination, false, true); err != nil {
		t.Fatal(err)
	}
	if err := Verify(destination, "org/model"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(destination.ModelDir("org/model"), "blobs", "weights"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("shared blob stayed a link and points to nothing in the destination vault")
	}
	if _, err = os.Stat(filepath.Join(destination.ModelDir("org/model"), "snapshots", strings.Repeat("a", 40), "weights.bin")); err != nil {
		t.Fatal("intra-repository link lost", err)
	}
}

func TestScanIgnoresEntriesWithoutSnapshot(t *testing.T) {
	source := fixture(t, filepath.Join(t.TempDir(), "cache"))
	refs := filepath.Join(source.Path, "models--x--y", "refs")
	if err := os.MkdirAll(refs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refs, "main"), []byte(strings.Repeat("a", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	models, skipped, err := Scan(source)
	if err != nil || len(models) != 1 {
		t.Fatalf("one interrupted download failed the vault: %d %v", len(models), err)
	}
	if len(skipped) != 1 || skipped[0] != "x/y" {
		t.Fatalf("skipped: %v", skipped)
	}
	inventory := &Inventory{Models: map[string]map[string]*Location{}, Vaults: map[string]*VaultState{}}
	if err := inventory.Refresh(source); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Vaults["cache"].Skipped) != 1 {
		t.Fatalf("skipped entries not recorded: %+v", inventory.Vaults["cache"])
	}
}
