package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/henry2man/arkadian/internal/config"
	"github.com/henry2man/arkadian/internal/source"
	"github.com/henry2man/arkadian/internal/store"
)

var version = "dev"

var usages = map[string]string{
	"list":    "list [model] [--vault V] [--json]",
	"refresh": "refresh [--vault V]",
	"pull":    "pull hf://org/model <destination> [--rev R] [--force]",
	"cp":      "cp <model> <source> <destination> [--force]",
	"mv":      "mv <model> <source> <destination> [--yes] [--force]",
	"get":     "get <model> [--force]",
	"evict":   "evict <model> <destination> [--yes] [--force]",
	"rm":      "rm <model> <vault> [--yes] [--force]",
	"sync":    "sync <source> <destination> [--force]",
	"verify":  "verify [model] [--vault V]",
	"path":    "path <model> [--vault V] [--rev R]",
	"vault":   "vault add <name> <location> | ls | rm <name> [--yes]",
	"version": "version",
}

// hints maps names a person may guess to the command that works.
var hints = map[string]string{
	"download": "ark pull hf://org/model <destination>", "load": "ark get <model>",
	"link":   "ark path <model> --vault <source>, then ln -s",
	"unlink": "unlink <path>", "info": "ark list <model> --json",
	"where": "ark list <model>", "ls": "ark list", "move": "ark mv", "remove": "ark rm",
	"promote": "ark get <model>", "demote": "ark evict <model> <destination>",
	"model": "ark list, ark pull, ark cp, ark mv, ark rm", "models": "ark list",
	"serve": "ark path <model>", "copy": "ark cp <model> <source> <destination>",
}

type usageError struct{ message string }

func (err usageError) Error() string { return err.message }

func main() { os.Exit(execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func help(output io.Writer, command string) {
	if usage, found := usages[command]; found {
		fmt.Fprintf(output, "usage: ark %s\n", usage)
		if command == "evict" || command == "mv" {
			fmt.Fprintln(output, "Copy and verify the destination before deleting source data.")
		}
		if command == "get" {
			fmt.Fprintln(output, "Find an available copy and bring it into the default HF cache.")
		}
		if command == "sync" {
			fmt.Fprintln(output, "Add source models to destination; never delete or copy backward.")
		}
		if command == "refresh" {
			fmt.Fprintln(output, "Refresh inventory, not weights. Unreachable vaults remain unknown.")
		}
		return
	}
	fmt.Fprintln(output, "Arkadian — model inventory and safe storage operations\nusage: ark <command> [arguments]")
	for _, name := range []string{"list", "refresh", "pull", "cp", "mv", "get", "evict", "rm", "sync", "verify", "path", "vault", "version"} {
		fmt.Fprintf(output, "  %s\n", usages[name])
	}
	fmt.Fprintln(output, "Use ark <command> --help. Mount storage and configure SSH outside Arkadian.")
}

func parseArgs(command string, args []string) ([]string, map[string]string, error) {
	values := map[string][]string{"list": {"vault"}, "refresh": {"vault"}, "pull": {"rev"}, "verify": {"vault"}, "path": {"vault", "rev"}}
	switches := map[string][]string{"list": {"json"}, "pull": {"force"}, "cp": {"force"}, "mv": {"yes", "force"}, "get": {"force"}, "evict": {"yes", "force"}, "rm": {"yes", "force"}, "sync": {"force"}, "vault": {"yes"}}
	positions := []string{}
	flags := map[string]string{}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			positions = append(positions, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") {
			positions = append(positions, argument)
			continue
		}
		name, value, assigned := strings.Cut(strings.TrimPrefix(argument, "--"), "=")
		if flags[name] != "" {
			return nil, nil, usageError{"duplicate option --" + name}
		}
		if slices.Contains(switches[command], name) {
			if assigned {
				return nil, nil, usageError{"--" + name + " takes no value"}
			}
			flags[name] = "true"
			continue
		}
		if !strings.HasPrefix(argument, "--") || !slices.Contains(values[command], name) {
			return nil, nil, usageError{"unknown option " + argument}
		}
		if !assigned {
			index++
			if index >= len(args) || strings.HasPrefix(args[index], "--") {
				return nil, nil, usageError{"missing value for --" + name}
			}
			value = args[index]
		}
		if value == "" {
			return nil, nil, usageError{"empty value for --" + name}
		}
		flags[name] = value
	}
	counts := map[string][2]int{"list": {0, 1}, "refresh": {0, 0}, "pull": {2, 2}, "cp": {3, 3}, "mv": {3, 3}, "get": {1, 1}, "evict": {2, 2}, "rm": {2, 2}, "sync": {2, 2}, "verify": {0, 1}, "path": {1, 1}, "version": {0, 0}, "vault": {1, 3}}
	count := counts[command]
	if len(positions) < count[0] || len(positions) > count[1] {
		return nil, nil, usageError{"wrong number of arguments"}
	}
	return positions, flags, nil
}

func execute(args []string, input io.Reader, output, diagnostics io.Writer) int {
	if len(args) == 0 {
		help(diagnostics, "")
		return 2
	}
	command := args[0]
	if command == "--help" || command == "-h" || command == "help" {
		help(output, "")
		return 0
	}
	if command == "--version" || command == "-v" {
		command = "version"
	}
	if _, found := usages[command]; !found {
		if replacement, found := hints[command]; found {
			fmt.Fprintf(diagnostics, "unknown command %q; use %s\n", command, replacement)
		} else {
			fmt.Fprintf(diagnostics, "unknown command %q\n", command)
		}
		return 2
	}
	if slices.Contains(args[1:], "--help") || slices.Contains(args[1:], "-h") {
		help(output, command)
		return 0
	}
	positions, flags, err := parseArgs(command, args[1:])
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		help(diagnostics, command)
		return 2
	}
	if command == "version" {
		fmt.Fprintf(output, "ark %s\nArkadian — model inventory and safe storage operations\nhttps://github.com/henry2man/arkadian\n", version)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o700); err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(config.Path()), ".ark.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	defer lock.Close()
	// ponytail: one process per config; use per-vault locks if concurrent transfers are needed.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fmt.Fprintln(diagnostics, "another Arkadian operation is using this configuration; retry after it finishes")
		return 1
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	configuration, err := config.Load()
	if err != nil {
		fmt.Fprintln(diagnostics, "config:", err)
		return 1
	}
	application := &app{configuration: configuration, input: bufio.NewReader(input), output: output, diagnostics: diagnostics}
	if command == "vault" {
		err = application.vault(positions, flags)
	} else {
		application.inventory, application.hasInventory, err = store.ReadInventory(config.InventoryPath())
		if err == nil {
			err = application.run(command, positions, flags)
		}
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "ark:", err)
		var invalid usageError
		if errors.As(err, &invalid) {
			help(diagnostics, command)
			return 2
		}
		return 1
	}
	return 0
}

type app struct {
	configuration *config.Config
	inventory     *store.Inventory
	hasInventory  bool
	input         *bufio.Reader
	output        io.Writer
	diagnostics   io.Writer
}

func (application *app) names() []string {
	names := []string{}
	for name := range application.configuration.Vaults {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (application *app) confirm(flags map[string]string, message string) error {
	if flags["yes"] != "" {
		return nil
	}
	fmt.Fprintf(application.diagnostics, "%s [y/N]: ", message)
	answer, err := application.input.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.ToLower(strings.TrimSpace(answer)) != "y" && strings.ToLower(strings.TrimSpace(answer)) != "yes" {
		return fmt.Errorf("cancelled; use --yes for scripts")
	}
	return nil
}

func (application *app) save() error {
	if err := store.AtomicJSON(config.InventoryPath(), application.inventory); err != nil {
		return fmt.Errorf("data retained but inventory save failed: %w; run ark refresh", err)
	}
	return nil
}

func (application *app) refresh(names ...string) error {
	var failures []error
	for _, name := range names {
		vault := application.configuration.Vaults[name]
		if err := application.inventory.Refresh(vault); err != nil {
			failures = append(failures, err)
			continue
		}
		if state := application.inventory.Vaults[name]; state != nil && len(state.Skipped) > 0 {
			noun := "entry"
			if len(state.Skipped) > 1 {
				noun = "entries"
			}
			fmt.Fprintf(application.diagnostics, "%s: ignored %d %s without a snapshot; see ark list --json vaults.%s.skipped\n", name, len(state.Skipped), noun, name)
		}
	}
	if err := application.save(); err != nil {
		return err
	}
	return errors.Join(failures...)
}

func (application *app) run(command string, args []string, flags map[string]string) error {
	if name := flags["vault"]; name != "" {
		if _, err := application.configuration.Vault(name); err != nil {
			return err
		}
	}
	if command == "list" {
		if !application.hasInventory {
			working, _ := application.configuration.Vault("")
			if _, err := os.Stat(working.Path); os.IsNotExist(err) {
				if err := store.Prepare(working); err != nil {
					return err
				}
			}
			application.refresh(application.names()...)
		}
		return application.list(args, flags)
	}
	if command == "refresh" {
		names := application.names()
		if flags["vault"] != "" {
			names = []string{flags["vault"]}
		}
		return application.refresh(names...)
	}
	if command == "sync" {
		return application.sync(args[0], args[1], flags)
	}
	if command == "verify" {
		return application.verify(args, flags)
	}
	if len(args) > 0 && command != "pull" {
		if err := store.ValidateRepo(args[0]); err != nil {
			return usageError{err.Error()}
		}
	}
	switch command {
	case "path":
		vault, err := application.configuration.Vault(flags["vault"])
		if err != nil {
			return err
		}
		model, err := store.Inspect(vault, args[0])
		if err != nil {
			return err
		}
		path, err := store.Snapshot(model, flags["rev"])
		if err != nil {
			return err
		}
		if vault.Remote() {
			path = vault.Host + ":" + path
		}
		fmt.Fprintln(application.output, path)
		return nil
	case "pull":
		return application.pull(args, flags)
	case "get":
		return application.get(args[0], flags)
	case "cp", "mv", "evict":
		sourceName, destinationName := application.configuration.DefaultVault, args[1]
		if command != "evict" {
			sourceName, destinationName = args[1], args[2]
		}
		sourceVault, err := application.configuration.Vault(sourceName)
		if err != nil {
			return err
		}
		destinationVault, err := application.configuration.Vault(destinationName)
		if err != nil {
			return err
		}
		move := command != "cp"
		if move {
			if err := application.confirm(flags, fmt.Sprintf("verify %s in %s, then remove it from %s?", args[0], destinationName, sourceName)); err != nil {
				return err
			}
		}
		if command == "evict" {
			model, err := store.Inspect(sourceVault, args[0])
			if err != nil {
				return err
			}
			if model != nil && model.Reference {
				if err := store.Delete(sourceVault, args[0], nil); err != nil {
					return err
				}
				return application.refresh(sourceName)
			}
		}
		err = store.Transfer(args[0], sourceVault, destinationVault, move, flags["force"] != "")
		refreshErr := application.refresh(sourceName, destinationName)
		if err != nil {
			return err
		}
		return refreshErr
	case "rm":
		return application.remove(args[0], args[1], flags)
	}
	return usageError{"unsupported command"}
}

func (application *app) pull(args []string, flags map[string]string) error {
	repo, err := source.Parse(args[0])
	if err != nil {
		return usageError{err.Error()}
	}
	vault, err := application.configuration.Vault(args[1])
	if err != nil {
		return err
	}
	model, err := store.Inspect(vault, repo)
	if err != nil {
		return err
	}
	if model != nil {
		if flags["rev"] != "" {
			if _, err := store.Snapshot(model, flags["rev"]); err != nil {
				return fmt.Errorf("model already exists with other revisions; updates are not supported in V1")
			}
		}
		fmt.Fprintln(application.diagnostics, "model already cached; no update or download performed")
		return application.refresh(vault.Name)
	}
	if err := store.Prepare(vault); err != nil {
		return err
	}
	need, err := source.DownloadBytes(repo, flags["rev"], vault)
	if err != nil {
		return err
	}
	total, free, err := store.Space(vault)
	if err != nil {
		return err
	}
	if err := store.RoomOK(total, free, need, flags["force"] != ""); err != nil {
		return err
	}
	if err := source.Fetch(repo, flags["rev"], vault); err != nil {
		application.refresh(vault.Name)
		return err
	}
	if _, err := store.EnsureManifest(vault, repo); err != nil {
		application.refresh(vault.Name)
		return err
	}
	return application.refresh(vault.Name)
}

func (application *app) get(repo string, flags map[string]string) error {
	destination, _ := application.configuration.Vault("")
	model, err := store.Inspect(destination, repo)
	if err != nil {
		return err
	}
	if model != nil && !model.Reference {
		fmt.Fprintln(application.diagnostics, "model already present in", destination.Name)
		return application.refresh(destination.Name)
	}
	names := application.names()
	sort.SliceStable(names, func(left, right int) bool {
		return !application.configuration.Vaults[names[left]].Remote() && application.configuration.Vaults[names[right]].Remote()
	})
	var selected *store.Model
	candidates := []store.Vault{}
	for _, name := range names {
		if name == destination.Name {
			continue
		}
		vault := application.configuration.Vaults[name]
		candidate, err := store.Inspect(vault, repo)
		if err != nil {
			fmt.Fprintln(application.diagnostics, err)
			continue
		}
		if candidate == nil {
			continue
		}
		if selected != nil && !store.SameArtifacts(selected, candidate) {
			return fmt.Errorf("different artifacts are available; choose one with ark cp %s <source> %s", repo, destination.Name)
		}
		selected = candidate
		candidates = append(candidates, vault)
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no available copy of %s; mount its vault or run ark refresh", repo)
	}
	var failures []error
	for _, vault := range candidates {
		if err := store.Transfer(repo, vault, destination, false, flags["force"] != ""); err != nil {
			failures = append(failures, err)
			continue
		}
		return application.refresh(vault.Name, destination.Name)
	}
	return errors.Join(failures...)
}

func (application *app) remove(repo, name string, flags map[string]string) error {
	vault, err := application.configuration.Vault(name)
	if err != nil {
		return err
	}
	model, err := store.Inspect(vault, repo)
	if err != nil {
		return err
	}
	if model == nil {
		return fmt.Errorf("model not present in %s", name)
	}
	var meta *store.Meta
	if !model.Reference && flags["force"] == "" {
		meta, err = store.EnsureManifest(vault, repo)
		if err != nil {
			return err
		}
		protected := false
		for _, otherName := range application.names() {
			if otherName == name {
				continue
			}
			other := application.configuration.Vaults[otherName]
			copy, inspectErr := store.Inspect(other, repo)
			if inspectErr != nil || copy == nil || copy.Reference || copy.Identity == model.Identity {
				continue
			}
			actual, hashErr := store.Manifest(other, repo)
			if hashErr == nil && actual.Digest == meta.Digest && store.Verify(other, repo) == nil {
				protected = true
				break
			}
		}
		if !protected {
			return fmt.Errorf("last known independent copy; use ark evict %s <destination>, or --force to delete it deliberately", repo)
		}
	}
	if err := application.confirm(flags, fmt.Sprintf("delete %s from %s?", repo, name)); err != nil {
		return err
	}
	if err := store.Delete(vault, repo, meta); err != nil {
		return err
	}
	return application.refresh(name)
}

func (application *app) sync(sourceName, destinationName string, flags map[string]string) error {
	sourceVault, err := application.configuration.Vault(sourceName)
	if err != nil {
		return err
	}
	destinationVault, err := application.configuration.Vault(destinationName)
	if err != nil {
		return err
	}
	if sourceName == destinationName {
		return fmt.Errorf("source and destination must differ")
	}
	models, _, err := store.Scan(sourceVault)
	if err != nil {
		return err
	}
	var failures []error
	for _, model := range models {
		if err := store.Transfer(model.Repo, sourceVault, destinationVault, false, flags["force"] != ""); err != nil {
			fmt.Fprintln(application.diagnostics, err)
			failures = append(failures, err)
		}
	}
	if err := application.refresh(sourceName, destinationName); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (application *app) verify(args []string, flags map[string]string) error {
	if len(args) > 0 {
		if err := store.ValidateRepo(args[0]); err != nil {
			return usageError{err.Error()}
		}
	}
	names := application.names()
	if flags["vault"] != "" {
		names = []string{flags["vault"]}
	}
	var failures []error
	checked := 0
	for _, name := range names {
		vault := application.configuration.Vaults[name]
		models, _, err := store.Scan(vault)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		corrupt := []string{}
		for _, model := range models {
			if len(args) > 0 && model.Repo != args[0] {
				continue
			}
			checked++
			if err := store.Verify(vault, model.Repo); err != nil {
				failures = append(failures, err)
				corrupt = append(corrupt, model.Repo)
			} else {
				fmt.Fprintf(application.diagnostics, "verified %s in %s\n", model.Repo, name)
			}
		}
		if err := application.inventory.Refresh(vault); err != nil {
			failures = append(failures, err)
		}
		for _, repo := range corrupt {
			if location := application.inventory.Models[repo][name]; location != nil {
				location.State = "corrupt"
			}
		}
	}
	if checked == 0 {
		failures = append(failures, fmt.Errorf("no matching copies were verified"))
	}
	if err := application.save(); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (application *app) list(args []string, flags map[string]string) error {
	if len(args) > 0 {
		if err := store.ValidateRepo(args[0]); err != nil {
			return usageError{err.Error()}
		}
	}
	names := application.names()
	if flags["vault"] != "" {
		names = []string{flags["vault"]}
	}
	unreadable := []string{}
	for _, name := range names {
		state := application.inventory.Vaults[name]
		switch {
		case state == nil || state.ObservedAt.IsZero():
			fmt.Fprintf(application.diagnostics, "%s: never scanned; run: ark refresh\n", name)
			unreadable = append(unreadable, name)
		case state.State == "unknown":
			fmt.Fprintf(application.diagnostics, "%s: unknown; %s\n", name, state.Error)
			unreadable = append(unreadable, name)
		}
	}
	filtered := &store.Inventory{Models: map[string]map[string]*store.Location{}, Vaults: map[string]*store.VaultState{}}
	for _, name := range names {
		if state := application.inventory.Vaults[name]; state != nil {
			filtered.Vaults[name] = state
		}
	}
	for repo, copies := range application.inventory.Models {
		if len(args) > 0 && repo != args[0] {
			continue
		}
		for _, name := range names {
			if location := copies[name]; location != nil {
				if filtered.Models[repo] == nil {
					filtered.Models[repo] = map[string]*store.Location{}
				}
				filtered.Models[repo][name] = location
			}
		}
	}
	if flags["json"] != "" {
		encoder := json.NewEncoder(application.output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(filtered)
	}
	writer := tabwriter.NewWriter(application.output, 0, 4, 2, ' ', 0)
	alone := map[string]bool{}
	fmt.Fprint(writer, "MODEL\tREVISION\tSIZE")
	for _, name := range names {
		fmt.Fprintf(writer, "\t%s", name)
	}
	fmt.Fprint(writer, "\tCOPIES")
	fmt.Fprintln(writer)
	repos := []string{}
	for repo := range filtered.Models {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		revisions := map[string]int64{}
		for _, location := range filtered.Models[repo] {
			for _, revision := range location.Model.Revisions {
				revisions[revision.Revision] = revision.SizeBytes
			}
		}
		commits := []string{}
		for commit := range revisions {
			commits = append(commits, commit)
		}
		sort.Strings(commits)
		for _, commit := range commits {
			fmt.Fprintf(writer, "%s\t%.12s\t%s", repo, commit, humanBytes(revisions[commit]))
			identities, references := map[string]bool{}, 0
			for _, location := range filtered.Models[repo] {
				for _, revision := range location.Model.Revisions {
					if revision.Revision != commit {
						continue
					}
					if location.Model.Reference {
						references++
					} else if !slices.Contains([]string{"missing", "unknown", "corrupt"}, location.State) {
						identities[location.Model.Identity] = true
					}
				}
			}
			for _, name := range names {
				state := "-"
				if location := filtered.Models[repo][name]; location != nil {
					for _, revision := range location.Model.Revisions {
						if revision.Revision == commit {
							state = location.State
							break
						}
					}
				}
				fmt.Fprintf(writer, "\t%s", state)
			}
			if len(identities) == 0 && references > 0 {
				fmt.Fprint(writer, "\tref")
				alone[repo] = true
			} else {
				fmt.Fprintf(writer, "\t%d", len(identities))
				if len(identities) < 2 {
					alone[repo] = true
				}
			}
			fmt.Fprintln(writer)
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(application.output, "\npresent = observed; verified = last checksum check; reference = no independent copy; unknown = unavailable; missing = absent; corrupt = failed check")
	fmt.Fprintln(application.output, "copies = independent copies of that artifact; ref = references only, and references are not copies")
	for _, name := range names {
		vault := application.configuration.Vaults[name]
		total, free, err := store.Space(vault)
		if err != nil {
			fmt.Fprintf(application.output, "%s: space unavailable\n", name)
		} else {
			fmt.Fprintf(application.output, "%s: %s free / %s total\n", name, humanBytes(free), humanBytes(total))
		}
	}
	singles, outside := 0, 0
	for _, repo := range repos {
		if alone[repo] {
			singles++
		}
		working := false
		for name, location := range application.inventory.Models[repo] {
			if name != application.configuration.DefaultVault || location.Model == nil {
				continue
			}
			if !slices.Contains([]string{"missing", "unknown", "corrupt"}, location.State) {
				working = true
			}
		}
		if !working {
			outside++
		}
	}
	if singles > 0 {
		noun, have := "models", "have"
		if singles == 1 {
			noun, have = "model", "has"
		}
		if len(application.configuration.Vaults) < 2 {
			fmt.Fprintf(application.output, "%s is the only vault; %d %s %s one copy. Add a vault with: ark vault add <name> <location>\n", application.configuration.DefaultVault, singles, noun, have)
		} else {
			fmt.Fprintf(application.output, "%d %s %s one copy; add one with: ark cp <model> <source> <destination>\n", singles, noun, have)
		}
	}
	if outside > 0 {
		noun, are := "models", "are"
		if outside == 1 {
			noun, are = "model", "is"
		}
		fmt.Fprintf(application.output, "%d %s %s not in %s; bring one in with: ark get <model>\n", outside, noun, are, application.configuration.DefaultVault)
	}
	if len(unreadable) > 0 {
		return fmt.Errorf("incomplete inventory; %s: run: ark refresh", strings.Join(unreadable, ", "))
	}
	return nil
}

func (application *app) vault(args []string, flags map[string]string) error {
	switch args[0] {
	case "ls":
		if len(args) != 1 || flags["yes"] != "" {
			return usageError{"usage: ark vault ls"}
		}
		for _, name := range application.names() {
			vault := application.configuration.Vaults[name]
			fmt.Fprintf(application.output, "%s\t%s\t%s\n", name, vault.Type, vault.URL())
		}
		return nil
	case "add":
		if len(args) != 3 || flags["yes"] != "" {
			return usageError{"usage: ark vault add <name> <location>"}
		}
		if _, exists := application.configuration.Vaults[args[1]]; exists {
			return fmt.Errorf("vault %s already exists; remove its configuration before replacing it", args[1])
		}
		vault, err := config.ParseLocation(args[1], args[2])
		if err != nil {
			return usageError{err.Error()}
		}
		application.configuration.Vaults[args[1]] = vault
		return application.configuration.Save()
	case "rm":
		if len(args) != 2 {
			return usageError{"usage: ark vault rm <name> [--yes]"}
		}
		if args[1] == application.configuration.DefaultVault {
			return fmt.Errorf("cannot remove the default working vault; change default_vault first")
		}
		if _, err := application.configuration.Vault(args[1]); err != nil {
			return err
		}
		if err := application.confirm(flags, "remove vault configuration only? Files stay on disk."); err != nil {
			return err
		}
		delete(application.configuration.Vaults, args[1])
		if err := application.configuration.Save(); err != nil {
			return err
		}
		inventory, _, err := store.ReadInventory(config.InventoryPath())
		if err != nil {
			return err
		}
		delete(inventory.Vaults, args[1])
		for _, copies := range inventory.Models {
			delete(copies, args[1])
		}
		return store.AtomicJSON(config.InventoryPath(), inventory)
	}
	return usageError{"unknown vault command"}
}

func humanBytes(size int64) string {
	if size < 1024 {
		return strconv.FormatInt(size, 10) + " B"
	}
	amount := float64(size)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB", "PiB"} {
		amount /= 1024
		if amount < 1024 || unit == "PiB" {
			return fmt.Sprintf("%.1f %s", amount, unit)
		}
	}
	return ""
}
