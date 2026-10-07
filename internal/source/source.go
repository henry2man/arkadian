package source

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/henry2man/arkadian/internal/store"
)

func Parse(uri string) (string, error) {
	if !strings.HasPrefix(uri, "hf://") {
		return "", fmt.Errorf("use a Hugging Face source: hf://org/model")
	}
	repo := strings.TrimPrefix(uri, "hf://")
	return repo, store.ValidateRepo(repo)
}

func Fetch(repo, revision string, vault store.Vault) error {
	if err := store.ValidateRepo(repo); err != nil {
		return err
	}
	if err := store.Prepare(vault); err != nil {
		return err
	}
	args := []string{"download", repo, "--cache-dir", vault.Path}
	if revision != "" {
		args = append(args, "--revision", revision)
	}
	_, err := store.Run(vault, true, "hf", args...)
	return err
}

func DownloadBytes(repo, revision string, vault store.Vault) (int64, error) {
	args := []string{"download", repo, "--cache-dir", vault.Path, "--dry-run"}
	if revision != "" {
		args = append(args, "--revision", revision)
	}
	output, err := store.Run(vault, false, "hf", args...)
	if err != nil {
		return 0, err
	}
	return ParseDownloadBytes(string(output))
}

func ParseDownloadBytes(output string) (int64, error) {
	match := regexp.MustCompile(`totall?ing ([0-9]+(?:\.[0-9]+)?)\s*([kKMGTPE]?)(?:B| bytes)?`).FindStringSubmatch(output)
	if match == nil {
		return 0, fmt.Errorf("HF dry-run output does not report download size; upgrade HF CLI")
	}
	amount, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, err
	}
	power := strings.Index(" KMGTPE", strings.ToUpper(match[2]))
	if match[2] == "" {
		power = 0
	}
	if power < 0 {
		return 0, fmt.Errorf("unknown HF download size unit")
	}
	unit := math.Pow(1000, float64(power))
	if amount == 0 {
		return 0, nil
	}
	return int64(math.Ceil((amount + 1) * unit)), nil
}
