// Package source wraps the places Arkadian brings models from: the Hugging Face
// Hub, ModelScope, or a bulk mirror tool.
//
// A source only writes into a local directory. Arkadian moves the result into a
// vault afterwards, so an interrupted fetch never pollutes a vault.
package source

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Source fetches one repo into a local dir (Hugging Face local-dir layout).
type Source interface {
	Name() string
	Check() error
	Fetch(repo, rev, localDir string) error
}

// auto lists the sources ark tries on its own, in order.
func auto() []Source { return []Source{hfCLI{}, hfTransfer{}, obscura{}} }

// All lists every source ark knows about, automatic or opt-in.
func All() []Source { return []Source{hfCLI{}, hfTransfer{}, obscura{}, modelscope{}} }

// Detect returns the preferred source, or the first available automatic one.
func Detect(preferred string) (Source, error) {
	if preferred != "" {
		for _, s := range All() {
			if s.Name() == preferred {
				if err := s.Check(); err != nil {
					return nil, fmt.Errorf("source %s: %w", preferred, err)
				}
				return s, nil
			}
		}
		return nil, fmt.Errorf("unknown source %q — ark knows: %s",
			preferred, names(All()))
	}
	for _, s := range auto() {
		if s.Check() == nil {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no download source found — install one of: " +
		"huggingface_hub CLI (pip install huggingface_hub), hf_transfer, obscura, " +
		"or pick one with --source (see: ark doctor)")
}

// Available returns the sources that work on this machine.
func Available() []Source {
	var out []Source
	for _, s := range All() {
		if s.Check() == nil {
			out = append(out, s)
		}
	}
	return out
}

func names(list []Source) string {
	var out []string
	for _, s := range list {
		out = append(out, s.Name())
	}
	return strings.Join(out, ", ")
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
	args := []string{"download", repo, "--local-dir", localDir}
	if rev != "" {
		args = append(args, "--revision", rev)
	}
	return run(hfBin(), args, "HF_HUB_ENABLE_HF_TRANSFER=0")
}

// hfTransfer: same CLI but with the hf_transfer fast path enabled.
type hfTransfer struct{}

func (hfTransfer) Name() string { return "hf-transfer" }
func (hfTransfer) Check() error {
	if err := (hfCLI{}).Check(); err != nil {
		return err
	}
	// hf_transfer is a python package the hub CLI uses when enabled.
	return exec.Command("python3", "-c", "import hf_transfer").Run()
}
func (hfTransfer) Fetch(repo, rev, localDir string) error {
	args := []string{"download", repo, "--local-dir", localDir}
	if rev != "" {
		args = append(args, "--revision", rev)
	}
	return run(hfBin(), args, "HF_HUB_ENABLE_HF_TRANSFER=1")
}

// modelscope: `modelscope download --model <repo> --local_dir <dir>`.
// Opt-in: use --source modelscope. ModelScope defaults to the "master"
// revision, HF to "main", so pass --rev when it matters.
type modelscope struct{}

func (modelscope) Name() string { return "modelscope" }
func (modelscope) Check() error {
	if _, err := exec.LookPath("modelscope"); err == nil {
		return nil
	}
	return fmt.Errorf("modelscope not in PATH (pip install modelscope)")
}
func (modelscope) Fetch(repo, rev, localDir string) error {
	args := []string{"download", "--model", repo, "--local_dir", localDir}
	if rev != "" {
		args = append(args, "--revision", rev)
	}
	return run("modelscope", args)
}

// obscura: site-to-bot style bulk fetch (external tool).
type obscura struct{}

func (obscura) Name() string { return "obscura" }
func (obscura) Check() error {
	_, err := exec.LookPath("obscura")
	return err
}
func (obscura) Fetch(repo, rev, localDir string) error {
	return run("obscura", []string{"hf-mirror", repo, "--rev", rev, "--out", localDir})
}

// hfBin returns the hub CLI found in PATH.
func hfBin() string {
	if p := os.Getenv("ARK_HF_BIN"); p != "" {
		return p
	}
	return "hf"
}

// run starts one fetch command and shows its output on stderr.
func run(bin string, args []string, env ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), env...)
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
