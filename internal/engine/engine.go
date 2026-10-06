// Package engine wraps download backends (hf CLI, hf_transfer, obscura, ...).
package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Engine fetches a repo into a local dir (HF local-dir layout).
type Engine interface {
	Name() string
	Check() error
	Fetch(repo, rev, localDir string) error
}

// detect returns the first available engine.
func Detect(preferred string) (Engine, error) {
	order := []Engine{hfCLI{}, hfTransfer{}, obscura{}}
	if preferred != "" {
		for _, e := range order {
			if e.Name() == preferred {
				if err := e.Check(); err != nil {
					return nil, fmt.Errorf("engine %s: %w", preferred, err)
				}
				return e, nil
			}
		}
		return nil, fmt.Errorf("unknown engine %q", preferred)
	}
	for _, e := range order {
		if e.Check() == nil {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no download engine found — install one of: huggingface_hub CLI (pip install huggingface_hub), hf_transfer, obscura")
}

// hfCLI: `hf download <repo> [--revision r] --local-dir d` (huggingface_hub >= 0.26)
type hfCLI struct{}

func (hfCLI) Name() string { return "hf" }
func (hfCLI) Check() error {
	for _, bin := range []string{"hf", "huggingface-cli"} {
		if p, err := exec.LookPath(bin); err == nil {
			os.Setenv("ARK_HF_BIN", p)
			return nil
		}
	}
	return fmt.Errorf("hf / huggingface-cli not in PATH")
}
func (hfCLI) Fetch(repo, rev, localDir string) error {
	bin := os.Getenv("ARK_HF_BIN")
	if bin == "" {
		bin = "hf"
	}
	args := []string{"download", repo, "--local-dir", localDir}
	if rev != "" {
		args = append(args, "--revision", rev)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "HF_HUB_ENABLE_HF_TRANSFER=0")
	return cmd.Run()
}

// hfTransfer: same CLI but with hf_transfer fast path enabled.
type hfTransfer struct{}

func (hfTransfer) Name() string { return "hf-transfer" }
func (hfTransfer) Check() error {
	if err := (hfCLI{}).Check(); err != nil {
		return err
	}
	// hf_transfer is a python package the hub CLI uses when enabled.
	out, err := exec.Command("python3", "-c", "import hf_transfer").CombinedOutput()
	_ = out
	return err
}
func (hfTransfer) Fetch(repo, rev, localDir string) error {
	bin := os.Getenv("ARK_HF_BIN")
	if bin == "" {
		bin = "hf"
	}
	args := []string{"download", repo, "--local-dir", localDir}
	if rev != "" {
		args = append(args, "--revision", rev)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "HF_HUB_ENABLE_HF_TRANSFER=1")
	return cmd.Run()
}

// obscura: site-to-bot style bulk fetch (external tool).
type obscura struct{}

func (obscura) Name() string { return "obscura" }
func (obscura) Check() error {
	_, err := exec.LookPath("obscura")
	return err
}
func (obscura) Fetch(repo, rev, localDir string) error {
	// obscura mirrors a HF repo tree into localDir.
	cmd := exec.Command("obscura", "hf-mirror", repo, "--rev", rev, "--out", localDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ListFiles returns relative file paths under dir (for manifesting).
func ListFiles(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.HasSuffix(rel, ".tmp") {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	return out
}
