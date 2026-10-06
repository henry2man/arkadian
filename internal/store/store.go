// Package store defines storage tiers (vaults) and the on-disk model layout.
//
// An ark "vault" is a directory holding models in Hugging Face local-dir
// layout:  <root>/models/<repo-slug>/... with .arkmeta.json describing the
// source repo, revision, size and file hashes.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Meta is the sidecar file (.arkmeta.json) written into each model dir.
type Meta struct {
	Repo       string            `json:"repo"`      // e.g. "Qwen/Qwen3-32B"
	RepoType   string            `json:"repo_type"` // "model" | "dataset"
	Revision   string            `json:"revision"`  // branch/tag/commit
	Downloaded time.Time         `json:"downloaded"`
	Source     string            `json:"source,omitempty"` // hf | hf-transfer | obscura | modelscope
	SizeBytes  int64             `json:"size_bytes"`
	Files      int               `json:"files"`
	Sha256     map[string]string `json:"sha256"` // relpath -> sha256 (may be empty)
}

// Vault is one storage location (usually the local Spark tier or a NAS tier).
type Vault struct {
	Name string `json:"name"`
	Kind string `json:"kind"`           // "local" | "remote"
	Host string `json:"host,omitempty"` // empty or "local" = this machine
	Path string `json:"path"`
}

// Remote reports whether the vault lives on another machine.
func (v Vault) Remote() bool { return v.Host != "" && v.Host != "local" }

// KindLabel names the vault kind for output: local, samba, or remote.
// A samba vault is a mounted path, so it behaves like a local vault.
func (v Vault) KindLabel() string {
	if v.Remote() {
		return "remote"
	}
	switch v.Kind {
	case "", "local":
		return "local"
	case "cifs", "mount", "smb":
		return "samba"
	default:
		return v.Kind
	}
}

// URL returns an rsync destination for the vault.
func (v Vault) URL() string {
	if v.Remote() {
		return fmt.Sprintf("%s:%s", v.Host, v.Path)
	}
	return v.Path
}

// ModelsDir returns <root>/models for this vault (local paths only here).
func (v Vault) ModelsDir() string { return filepath.Join(v.Path, "models") }

// ModelDir returns the local directory of a model slug inside a vault.
func (v Vault) ModelDir(slug string) string { return filepath.Join(v.ModelsDir(), slug) }

// Slug converts a repo id ("Qwen/Qwen3-32B") to a safe directory slug.
func Slug(repo string) string { return strings.ReplaceAll(repo, "/", "--") }

// RepoFromSlug is the inverse of Slug.
func RepoFromSlug(slug string) string { return strings.ReplaceAll(slug, "--", "/") }

// Model is a discovered model entry on a vault.
type Model struct {
	Vault Vault
	Slug  string
	Dir   string // local path if the vault is local; remote path string if remote
	Meta  *Meta
}

// ReadMeta loads .arkmeta.json from a model dir (nil, nil if absent).
func ReadMeta(modelDir string) (*Meta, error) {
	b, err := os.ReadFile(filepath.Join(modelDir, ".arkmeta.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// WriteMeta writes .arkmeta.json into a model dir.
func WriteMeta(modelDir string, m *Meta) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(modelDir, ".arkmeta.json"), b, 0o644)
}

// DirSize sums file sizes under a path.
func DirSize(path string) (int64, int, error) {
	var total int64
	var count int
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
			count++
		}
		return nil
	})
	return total, count, err
}

// ListLocal enumerates model dirs in a local vault.
func ListLocal(v Vault) ([]Model, error) {
	entries, err := os.ReadDir(v.ModelsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Model
	for _, e := range entries {
		dir := filepath.Join(v.ModelsDir(), e.Name())
		if !e.IsDir() {
			// a link move (ark mv --link) leaves a symlink here: that is a model dir too
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				continue
			}
		}
		m, _ := ReadMeta(dir)
		out = append(out, Model{Vault: v, Slug: e.Name(), Dir: dir, Meta: m})
	}
	return out, nil
}

// ListRemote enumerates model dirs on a remote vault via ssh ls.
func ListRemote(v Vault, ssh func(host, cmd string) (string, error)) ([]Model, error) {
	out, err := ssh(v.Host, fmt.Sprintf("ls -1 %q 2>/dev/null || true", v.ModelsDir()))
	if err != nil {
		return nil, err
	}
	var models []Model
	for _, slug := range strings.Split(strings.TrimSpace(out), "\n") {
		if slug == "" {
			continue
		}
		metaOut, _ := ssh(v.Host, fmt.Sprintf("cat %q 2>/dev/null || true",
			filepath.Join(v.ModelsDir(), slug, ".arkmeta.json")))
		m := &Meta{}
		json.Unmarshal([]byte(metaOut), m)
		models = append(models, Model{
			Vault: v, Slug: slug,
			Dir:  filepath.Join(v.ModelsDir(), slug),
			Meta: m,
		})
	}
	return models, nil
}

// CopyTree copies src to dst, hardlinking files when both sides sit on one
// filesystem, and copying bytes when they do not. It is a move: the source is
// safe to delete afterwards, the bytes live on in the destination.
func CopyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if link := os.Link(p, target); link == nil {
			return nil // same filesystem: one inode, no bytes copied
		}
		return copyFile(p, target)
	})
}

// RemoveModel deletes a model dir and its empty slug parent. It refuses to
// touch anything outside <vault>/models.
func RemoveModel(v Vault, slug string) error {
	dir := v.ModelDir(slug)
	models := v.ModelsDir()
	if !inside(models, dir) || dir == models {
		return fmt.Errorf("refusing to delete %s: not inside %s", dir, models)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return nil
}

// inside reports whether path sits under root.
func inside(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Sha256File computes the sha256 hex digest of a file.
func Sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
