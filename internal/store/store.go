package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Vault struct {
	Name string `json:"-"`
	Type string `json:"type"`
	Host string `json:"host,omitempty"`
	Path string `json:"path"`
}

type Revision struct {
	Revision  string   `json:"revision"`
	Refs      []string `json:"refs"`
	SizeBytes int64    `json:"size_bytes"`
}

type Model struct {
	Repo        string     `json:"repo"`
	Dir         string     `json:"path"`
	Identity    string     `json:"identity"`
	Reference   bool       `json:"reference"`
	Fingerprint string     `json:"fingerprint"`
	SizeBytes   int64      `json:"size_bytes"`
	Revisions   []Revision `json:"revisions"`
}

type File struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Meta struct {
	Repo                string            `json:"repo"`
	Revisions           []string          `json:"revisions"`
	Files               map[string]File   `json:"files"`
	Links               map[string]string `json:"links"`
	Digest              string            `json:"digest"`
	VerifiedAt          string            `json:"verified_at,omitempty"`
	VerifiedFingerprint string            `json:"verified_fingerprint,omitempty"`
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]*$`)

func ValidateName(name string) error {
	if !namePattern.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("invalid vault name %q", name)
	}
	return nil
}

func ValidateRepo(repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) < 1 || len(parts) > 2 {
		return fmt.Errorf("use the exact HF repository identifier, such as org/model")
	}
	for _, part := range parts {
		if !namePattern.MatchString(part) || strings.Contains(part, "--") || strings.Contains(part, "..") || strings.HasSuffix(part, ".") || len(part) > 96 {
			return fmt.Errorf("invalid model identifier %q", repo)
		}
	}
	return nil
}

func (vault Vault) Remote() bool { return vault.Host != "" }
func (vault Vault) ModelDir(repo string) string {
	return filepath.Join(vault.Path, "models--"+strings.ReplaceAll(repo, "/", "--"))
}
func (vault Vault) URL() string {
	if vault.Remote() {
		return vault.Host + ":" + vault.Path
	}
	return vault.Path
}
func (vault Vault) Validate() error {
	if err := ValidateName(vault.Name); err != nil {
		return err
	}
	if vault.Type != "huggingface" {
		return fmt.Errorf("type must be huggingface; old Arkadian vaults are not native HF caches")
	}
	if vault.Path == "" || !filepath.IsAbs(vault.Path) || strings.ContainsAny(vault.Path, "\x00\r\n") {
		return fmt.Errorf("vault path must be absolute")
	}
	if vault.Remote() && !hostPattern.MatchString(vault.Host) {
		return fmt.Errorf("invalid SSH host; use an SSH config alias")
	}
	return nil
}

func Quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func Command(vault Vault, name string, args ...string) *exec.Cmd {
	if !vault.Remote() {
		return exec.Command(name, args...)
	}
	words := []string{Quote(name)}
	for _, arg := range args {
		words = append(words, Quote(arg))
	}
	return exec.Command("ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", vault.Host, strings.Join(words, " "))
}

func Run(vault Vault, progress bool, name string, args ...string) ([]byte, error) {
	command := Command(vault, name, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	if progress {
		command.Stdout, command.Stderr = os.Stderr, os.Stderr
	} else {
		command.Stderr = &stderr
	}
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%s on %s: %w: %s (check installed tools, permissions, and SSH keys)", name, vault.Name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func helper(vault Vault, action, path string, input any, output any) error {
	command := Command(vault, "python3", "-c", cacheHelper, action, path)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	command.Stdin = bytes.NewReader(data)
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s on %s: %w: %s", action, vault.Name, err, strings.TrimSpace(stderr.String()))
	}
	if output != nil {
		return json.Unmarshal(stdout.Bytes(), output)
	}
	return nil
}

func Prepare(vault Vault) error { return helper(vault, "prepare", vault.Path, nil, nil) }

func Inspect(vault Vault, repo string) (*Model, error) {
	if err := ValidateRepo(repo); err != nil {
		return nil, err
	}
	var model *Model
	if err := helper(vault, "inspect", vault.ModelDir(repo), nil, &model); err != nil {
		return nil, err
	}
	if model != nil {
		model.Repo = repo
	}
	return model, nil
}

func Manifest(vault Vault, repo string) (*Meta, error) {
	var meta Meta
	if err := helper(vault, "hash", vault.ModelDir(repo), nil, &meta); err != nil {
		return nil, err
	}
	meta.Repo = repo
	return &meta, nil
}

func ReadMeta(vault Vault, repo string) (*Meta, error) {
	var meta *Meta
	err := helper(vault, "meta", vault.ModelDir(repo), nil, &meta)
	return meta, err
}

func EnsureManifest(vault Vault, repo string) (*Meta, error) {
	current, err := Manifest(vault, repo)
	if err != nil {
		return nil, err
	}
	expected, err := ReadMeta(vault, repo)
	if err != nil {
		return nil, err
	}
	if expected != nil && expected.Digest != current.Digest {
		return nil, fmt.Errorf("corrupt or changed model %s in %s; manifest mismatch", repo, vault.Name)
	}
	if expected == nil {
		model, err := Inspect(vault, repo)
		if err != nil {
			return nil, err
		}
		if !model.Reference {
			if err := helper(vault, "save", vault.ModelDir(repo), current, nil); err != nil {
				return nil, err
			}
		}
	}
	return current, nil
}

func Verify(vault Vault, repo string) error {
	expected, err := ReadMeta(vault, repo)
	if err != nil {
		return err
	}
	if expected == nil {
		return fmt.Errorf("%s in %s has no manifest; copy it with ark cp to establish one", repo, vault.Name)
	}
	current, err := Manifest(vault, repo)
	if err != nil {
		return err
	}
	if current.Digest != expected.Digest {
		return fmt.Errorf("manifest mismatch for %s in %s", repo, vault.Name)
	}
	model, err := Inspect(vault, repo)
	if err != nil {
		return err
	}
	if !model.Reference {
		return helper(vault, "save", vault.ModelDir(repo), current, nil)
	}
	return nil
}

func Scan(vault Vault) ([]*Model, error) {
	var names []string
	if err := helper(vault, "catalog", vault.Path, nil, &names); err != nil {
		return nil, err
	}
	data, err := Run(vault, false, "hf", "cache", "ls", "--revisions", "--format", "json", "--cache-dir", vault.Path)
	if err != nil {
		return nil, fmt.Errorf("%w; HF CLI must support cache ls --revisions --format json", err)
	}
	var entries []struct {
		Repo     string `json:"repo_id"`
		Type     string `json:"repo_type"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("invalid HF cache JSON: %w", err)
	}
	observed := map[string]map[string]bool{}
	for _, entry := range entries {
		if entry.Type != "model" {
			continue
		}
		if observed[entry.Repo] == nil {
			observed[entry.Repo] = map[string]bool{}
		}
		observed[entry.Repo][entry.Revision] = true
	}
	models := []*Model{}
	for _, name := range names {
		repo := strings.ReplaceAll(strings.TrimPrefix(name, "models--"), "--", "/")
		model, err := Inspect(vault, repo)
		if err != nil {
			return nil, fmt.Errorf("incomplete scan; %s: %w", repo, err)
		}
		for _, revision := range model.Revisions {
			if !observed[repo][revision.Revision] {
				return nil, fmt.Errorf("HF omitted %s@%s; cache may be corrupt; no copies were marked missing", repo, revision.Revision)
			}
		}
		models = append(models, model)
	}
	return models, nil
}

func Snapshot(model *Model, revision string) (string, error) {
	if model == nil {
		return "", fmt.Errorf("model is missing; run ark refresh")
	}
	if revision == "" {
		for _, candidate := range model.Revisions {
			if slices.Contains(candidate.Refs, "main") {
				revision = candidate.Revision
				break
			}
		}
		if revision == "" && len(model.Revisions) == 1 {
			revision = model.Revisions[0].Revision
		}
	}
	for _, candidate := range model.Revisions {
		if candidate.Revision == revision || slices.Contains(candidate.Refs, revision) {
			return filepath.Join(model.Dir, "snapshots", candidate.Revision), nil
		}
	}
	return "", fmt.Errorf("revision is ambiguous or absent; use --rev <cached-commit-or-ref>")
}

type Location struct {
	Model      *Model    `json:"model"`
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observed_at"`
	VerifiedAt string    `json:"verified_at,omitempty"`
}

type VaultState struct {
	State      string    `json:"state"`
	Error      string    `json:"error,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

type Inventory struct {
	Models map[string]map[string]*Location `json:"models"`
	Vaults map[string]*VaultState          `json:"vaults"`
}

func ReadInventory(path string) (*Inventory, bool, error) {
	result := &Inventory{Models: map[string]map[string]*Location{}, Vaults: map[string]*VaultState{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return result, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return nil, false, err
	}
	if result.Models == nil {
		result.Models = map[string]map[string]*Location{}
	}
	if result.Vaults == nil {
		result.Vaults = map[string]*VaultState{}
	}
	for repo, copies := range result.Models {
		if err := ValidateRepo(repo); err != nil {
			return nil, false, err
		}
		for name, location := range copies {
			if err := ValidateName(name); err != nil {
				return nil, false, err
			}
			if location == nil || location.Model == nil || location.Model.Repo != repo {
				return nil, false, fmt.Errorf("invalid inventory entry for %s in %s; restore models.json or rebuild it", repo, name)
			}
		}
	}
	return result, true, nil
}

func (inventory *Inventory) Refresh(vault Vault) error {
	models, err := Scan(vault)
	if err != nil {
		inventory.Vaults[vault.Name] = &VaultState{State: "unknown", Error: err.Error()}
		for _, copies := range inventory.Models {
			if location := copies[vault.Name]; location != nil {
				location.State = "unknown"
			}
		}
		return err
	}
	now := time.Now().UTC()
	for _, copies := range inventory.Models {
		if location := copies[vault.Name]; location != nil {
			location.State = "missing"
			location.ObservedAt = now
		}
	}
	for _, model := range models {
		if inventory.Models[model.Repo] == nil {
			inventory.Models[model.Repo] = map[string]*Location{}
		}
		location := &Location{Model: model, State: "present", ObservedAt: now}
		meta, err := ReadMeta(vault, model.Repo)
		if err != nil {
			location.State = "corrupt"
		} else if meta != nil && meta.VerifiedFingerprint == model.Fingerprint && meta.VerifiedAt != "" && !model.Reference {
			location.State, location.VerifiedAt = "verified", meta.VerifiedAt
		}
		if model.Reference {
			location.State = "reference"
		}
		inventory.Models[model.Repo][vault.Name] = location
	}
	inventory.Vaults[vault.Name] = &VaultState{State: "available", ObservedAt: now}
	return nil
}

func Space(vault Vault) (total, free int64, err error) {
	if !vault.Remote() {
		return Free(vault.Path)
	}
	output, err := Run(vault, false, "df", "-Pk", vault.Path)
	if err != nil {
		return 0, 0, err
	}
	total, free, valid := DfSpaces(string(output))
	if !valid {
		return 0, 0, fmt.Errorf("cannot read disk space in %s", vault.Name)
	}
	return total, free, nil
}

func RoomOK(total, free, need int64, force bool) error {
	if !force && (total <= 0 || need < 0 || total-free+need > total*9/10) {
		return fmt.Errorf("copy needs %d bytes and would pass the 90%% space limit; free space or use --force", need)
	}
	return nil
}

func Transfer(repo string, source, destination Vault, move, force bool) error {
	if err := ValidateRepo(repo); err != nil {
		return err
	}
	if err := Prepare(destination); err != nil {
		return err
	}
	sourceModel, err := Inspect(source, repo)
	if err != nil {
		return err
	}
	if sourceModel == nil {
		return fmt.Errorf("%s is not in %s", repo, source.Name)
	}
	var destinationIdentity string
	if err := helper(destination, "identity", destination.ModelDir(repo), nil, &destinationIdentity); err != nil {
		return err
	}
	destinationModel, err := Inspect(destination, repo)
	if err != nil {
		return err
	}
	if sourceModel.Identity == destinationIdentity && (destinationModel == nil || !destinationModel.Reference) {
		return fmt.Errorf("source and destination refer to the same physical repository")
	}
	meta, err := EnsureManifest(source, repo)
	if err != nil {
		return err
	}
	if destinationModel != nil && !destinationModel.Reference {
		if err := Verify(destination, repo); err != nil {
			expected, metaErr := ReadMeta(destination, repo)
			if metaErr != nil || expected != nil {
				return err
			}
		}
		actual, err := Manifest(destination, repo)
		if err != nil {
			return err
		}
		if actual.Digest != meta.Digest {
			return fmt.Errorf("conflicting artifact in %s; destination was not overwritten", destination.Name)
		}
		if err := helper(destination, "save", destination.ModelDir(repo), meta, nil); err != nil {
			return err
		}
	} else {
		total, free, err := Space(destination)
		if err != nil {
			return err
		}
		if err := RoomOK(total, free, sourceModel.SizeBytes, force); err != nil {
			return err
		}
		stageRoot := filepath.Join(destination.Path, ".locks", "ark-staging")
		stageVault := destination
		stageVault.Path = stageRoot
		if err := helper(destination, "mkdir", stageVault.ModelDir(repo), nil, nil); err != nil {
			return err
		}
		args := []string{"-a", "--checksum", "--partial", "--delete", "--exclude=.arkmeta.json", "--exclude=*.incomplete", "--exclude=*.lock", "--exclude=*.tmp", "--exclude=.locks", "--", source.ModelDir(repo) + "/", stageVault.ModelDir(repo) + "/"}
		if source.Remote() && destination.Remote() {
			args[len(args)-1] = destination.Host + ":" + Quote(stageVault.ModelDir(repo)+"/")
			args = append([]string{"-e", "ssh -o BatchMode=yes -o ConnectTimeout=10"}, args...)
			_, err = Run(source, true, "rsync", args...)
		} else {
			if source.Remote() {
				args[len(args)-2] = source.Host + ":" + Quote(source.ModelDir(repo)+"/")
			}
			if destination.Remote() {
				args[len(args)-1] = destination.Host + ":" + Quote(stageVault.ModelDir(repo)+"/")
			}
			args = append([]string{"-e", "ssh -o BatchMode=yes -o ConnectTimeout=10"}, args...)
			_, err = Run(Vault{Name: "this machine"}, true, "rsync", args...)
		}
		if err != nil {
			return err
		}
		actual, err := Manifest(stageVault, repo)
		if err != nil {
			return err
		}
		if actual.Digest != meta.Digest {
			return fmt.Errorf("destination verification failed; staging retained and source untouched")
		}
		if err := helper(stageVault, "save", stageVault.ModelDir(repo), meta, nil); err != nil {
			return err
		}
		publication := map[string]string{"staging": stageVault.ModelDir(repo)}
		if destinationModel != nil && destinationModel.Reference {
			publication["reference_identity"] = destinationIdentity
		}
		if err := helper(destination, "publish", destination.ModelDir(repo), publication, nil); err != nil {
			return err
		}
	}
	if move {
		if err := Verify(destination, repo); err != nil {
			return err
		}
		return Delete(source, repo, meta)
	}
	return nil
}

func Delete(vault Vault, repo string, expected *Meta) error {
	if err := ValidateRepo(repo); err != nil {
		return err
	}
	return helper(vault, "delete", vault.ModelDir(repo), expected, nil)
}

func SameArtifacts(first, second *Model) bool {
	if first == nil || second == nil {
		return false
	}
	left, right := []string{}, []string{}
	for _, revision := range first.Revisions {
		left = append(left, revision.Revision)
	}
	for _, revision := range second.Revisions {
		right = append(right, revision.Revision)
	}
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

func AtomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ark-json-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
