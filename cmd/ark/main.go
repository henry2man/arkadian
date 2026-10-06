// Command ark — custody of Hugging Face models across local and remote vaults.
//
//	ark list                     models in every vault
//	ark download <repo>          fetch from HF into the vault (default: nas)
//	ark promote <repo>           copy model from remote vault to local
//	ark demote <repo>            copy model from local to remote vault
//	ark link <repo>              symlink from local models dir to vault dir
//	ark verify [repo]            checksum models (vs .arkmeta.json)
//	ark info <repo>              show metadata
//	ark vault ls|add|rm          manage vaults
//	ark serve <repo>             convenience: symlink into staging + export vars
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/henry2man/ark/internal/config"
	"github.com/henry2man/ark/internal/engine"
	"github.com/henry2man/ark/internal/store"
)

var cfg *config.Config

func main() {
	c, err := config.Load()
	if err != nil {
		die("config: %v", err)
	}
	cfg = c

	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list", "ls":
		cmdList(rest)
	case "download", "dl":
		cmdDownload(rest)
	case "promote", "pull":
		cmdPromote(rest)
	case "demote", "down", "push":
		cmdDemote(rest)
	case "link":
		cmdLink(rest)
	case "verify":
		cmdVerify(rest)
	case "info":
		cmdInfo(rest)
	case "serve":
		cmdServe(rest)
	case "vault":
		cmdVault(rest)
	case "help", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`ark — custody of Hugging Face models across vaults

Usage:
  ark list                     List models in all vaults
  ark download <repo> [--to V] [--rev R] [--engine E]
                               Download from HF into vault V (default: to=vault NAS)
  ark promote <repo> [--from V] [--local V]
                               Copy a model from a vault to the local tier
  ark demote  <repo> [--to V]   Copy model from local vault to vault V (default: to)
  ark link <repo> [--dir DIR]   Symlink a vault model into DIR (default: ~/ark/models)
  ark verify [repo]             Check sha256 of models against .arkmeta.json
  ark info <repo>               Show stored metadata
  ark serve <repo> [--dir DIR]  Symlink model + print HF_HUB_OFFLINE env for vLLM
  ark vault ls | add <n> <host> <path> | rm <n>

Config: ` + config.Path() + `
Vaults: local dir or user@host:/path (rsync over ssh).`)
}

// ---------- helpers ----------

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "ark: "+f+"\n", a...)
	os.Exit(1)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// sshRun executes cmd on host (non-interactive).
func sshRun(host, cmd string) (string, error) {
	out, err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", host, cmd).CombinedOutput()
	return string(out), err
}

// parseFlags extracts --key value pairs; returns positional and map.
func parseFlags(args []string) ([]string, map[string]string) {
	pos := []string{}
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			flags[strings.TrimPrefix(args[i], "--")] = args[i+1]
			i++
		} else {
			pos = append(pos, args[i])
		}
	}
	return pos, flags
}

// resolveSource finds where a repo lives: local vault first, then remotes.
func resolveSource(repo string) (store.Model, error) {
	slug := store.Slug(repo)
	var localName string
	// local vaults first
	for name, v := range cfg.Vaults {
		if v.Remote() {
			continue
		}
		localName = name
		d := v.ModelDir(slug)
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			m, _ := store.ReadMeta(d)
			return store.Model{Vault: v, Slug: slug, Dir: d, Meta: m, Status: "ok"}, nil
		}
	}
	for name, v := range cfg.Vaults {
		if !v.Remote() || name == localName {
			continue
		}
		out, _ := sshRun(v.Host, fmt.Sprintf("test -d %q && echo yes", v.ModelDir(slug)))
		if strings.TrimSpace(out) == "yes" {
			return store.Model{Vault: v, Slug: slug, Dir: v.ModelDir(slug), Status: "remote"}, nil
		}
	}
	return store.Model{}, fmt.Errorf("model %q not found in any vault (try: ark download %s)", repo, repo)
}

// rsyncCopy copies a model dir between vaults with checksums.
func rsyncCopy(srcDir, dstDir string, flags string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	args := strings.Fields(flags)
	args = append(args, srcDir+"/", dstDir+"/")
	return run("rsync", args...)
}

// humanBytes formats byte counts.
func humanBytes(n int64) string {
	units := []string{"B", "K", "M", "G", "T", "P"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f%s", f, units[i])
}

// ---------- commands ----------

func cmdList(args []string) {
	type row struct {
		repo, vault, where, size, rev, date string
	}
	var rows []row

	for _, name := range sortedVaultNames() {
		v := cfg.Vaults[name]
		if v.Remote() {
			models, err := store.ListRemote(v, sshRun)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ark: vault %s: warning: %v\n", name, err)
				continue
			}
			for _, m := range models {
				r := row{repo: store.RepoFromSlug(m.Slug), vault: name, where: "remote"}
				if m.Meta != nil {
					r.size, r.rev, r.date = humanBytes(m.Meta.SizeBytes), m.Meta.Revision, m.Meta.Downloaded.Format("2006-01-02")
				} else {
					r.size, r.rev, r.date = "?", "-", "-"
				}
				rows = append(rows, r)
			}
			continue
		}
		models, err := store.ListLocal(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ark: vault %s: %v\n", name, err)
			continue
		}
		for _, m := range models {
			r := row{repo: store.RepoFromSlug(m.Slug), vault: name, where: "local", rev: "-", date: "-", size: "?"}
			if m.Meta != nil {
				r.rev = m.Meta.Revision
				r.size = humanBytes(m.Meta.SizeBytes)
				if !m.Meta.Downloaded.IsZero() {
					r.date = m.Meta.Downloaded.Format("2006-01-02")
				}
			} else {
				if sz, _, err := store.DirSize(m.Dir); err == nil {
					r.size = humanBytes(sz)
				}
			}
			rows = append(rows, r)
		}
	}

	if len(rows) == 0 {
		fmt.Println("no models yet. Run: ark download <org/model>")
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].repo != rows[j].repo {
			return rows[i].repo < rows[j].repo
		}
		return rows[i].vault < rows[j].vault
	})
	w := [4]int{len("REPO"), len("VAULT"), len("WHERE"), len("SIZE")}
	for _, r := range rows {
		w[0] = max(w[0], len(r.repo))
		w[1] = max(w[1], len(r.vault))
		w[2] = max(w[2], len(r.where))
		w[3] = max(w[3], len(r.size))
	}
	fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-7s  %s\n", w[0], "REPO", w[1], "VAULT", w[2], "WHERE", w[3], "SIZE", "REVISION", "DATE")
	for _, r := range rows {
		fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-7s  %s\n", w[0], r.repo, w[1], r.vault, w[2], r.where, w[3], r.size, trunc(r.rev, 7), r.date)
	}
}

func cmdDownload(args []string) {
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark download <org/model> [--to vault] [--rev revision] [--engine name]")
	}
	repo := pos[0]
	toName := cfg.DefaultTo
	if v, ok := flags["to"]; ok {
		toName = v
	}
	vault, err := cfg.Vault(toName)
	if err != nil {
		die("%v", err)
	}
	if vault.Remote() {
		die("--to must be a LOCAL vault; then use `ark demote %s --to %s` to ship it (HF cannot download directly to remote)", repo, toName)
	}
	eng, err := engine.Detect(flagOr(flags, "engine", cfg.Engine))
	if err != nil {
		die("%v", err)
	}
	slug := store.Slug(repo)
	tmpDir := filepath.Join(filepath.Dir(vault.Path), "staging", slug)
	os.RemoveAll(tmpDir)
	defer os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		die("%v", err)
	}

	fmt.Printf("ark: downloading %s via %s -> staging\n", repo, eng.Name())
	if err := eng.Fetch(repo, flags["rev"], tmpDir); err != nil {
		die("download failed: %v", err)
	}

	meta := store.Meta{
		Repo:     repo,
		RepoType: flagOr(flags, "repo-type", "model"),
		Revision: flagOr(flags, "rev", "main"),
		Engine:   eng.Name(),
	}
	if flags["hashes"] != "" {
		fmt.Println("ark: hashing files (manifest)...")
		meta.Sha256 = map[string]string{}
		for _, rel := range engine.ListFiles(tmpDir) {
			h, err := store.Sha256File(filepath.Join(tmpDir, rel))
			if err != nil {
				fmt.Fprintf(os.Stderr, "ark: warn: hash %s: %v\n", rel, err)
				continue
			}
			meta.Sha256[rel] = h
		}
	} else {
		fmt.Println("ark: pass --hashes to store a checksum manifest (slower, enables `ark verify`)")
	}
	meta.SizeBytes, meta.Files, _ = store.DirSize(tmpDir)

	// local target: hardlink-copy, else rsync to remote.
	dst := vault.ModelDir(slug)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		die("%v", err)
	}
	if err := store.CopyTree(tmpDir, dst); err != nil {
		die("move to vault: %v", err)
	}
	if err := store.WriteMeta(dst, &meta); err != nil {
		die("%v", err)
	}
	fmt.Printf("ark: %s ok -> %s (%d files, %s)\n", repo, vault.URL(), meta.Files, humanBytes(meta.SizeBytes))

	if remoteName := flags["and-remote"]; remoteName != "" {
		rv, err := cfg.Vault(remoteName)
		if err != nil {
			die("%v", err)
		}
		if err := config.EnsureRemote(rv, sshRun); err != nil {
			die("prepare remote: %v", err)
		}
		fmt.Printf("ark: shipping -> %s\n", rv.URL())
		if err := rsyncCopy(dst, rv.ModelDir(slug), cfg.RsyncFlags); err != nil {
			die("ship: %v", err)
		}
		fmt.Printf("ark: %s shipped to %s\n", repo, rv.Name)
	}
}

func cmdPromote(args []string) {
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark promote <org/model> [--from vault]")
	}
	repo := pos[0]
	src, err := resolveSourceRepo(flags["from"], repo)
	if err != nil {
		die("%v", err)
	}
	local, err := localVaultNamed(flags["local"])
	if err != nil {
		die("%v", err)
	}
	dst := local.ModelDir(store.Slug(repo))
	if src.Vault.Name == local.Name {
		fmt.Printf("ark: %s already in local tier (%s)\n", repo, dst)
		return
	}
	fmt.Printf("ark: promote %s: %s -> %s\n", repo, src.Vault.URL(), local.URL())
	if src.Vault.Remote() {
		if err := rsyncCopy(src.Vault.URL()+"/models/"+store.Slug(repo), dst, cfg.RsyncFlags); err != nil {
			die("promote: %v", err)
		}
	} else if err := store.CopyTree(src.Dir, dst); err != nil {
		die("promote: %v", err)
	}
	fmt.Printf("ark: promoted %s -> %s\n", repo, dst)
}

func cmdDemote(args []string) {
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark demote <org/model> [--to vault]")
	}
	repo := pos[0]
	toName := flagOr(flags, "to", cfg.DefaultTo)
	dst, err := cfg.Vault(toName)
	if err != nil {
		die("%v", err)
	}
	if dst.Remote() {
		if err := config.EnsureRemote(dst, sshRun); err != nil {
			die("prepare remote: %v (tip: enable SSH in DSM Control Panel, and ssh-copy-id %s)", err, dst.Host)
		}
	}
	srcModel, err := resolveSource(repo)
	if err != nil {
		die("%v", err)
	}
	dstDir := dst.ModelDir(store.Slug(repo))
	fmt.Printf("ark: demote %s: %s -> %s\n", repo, srcModel.Vault.URL(), dst.URL())
	if dst.Remote() {
		if err := rsyncCopy(srcModel.Dir, dst.URL()+"/models/"+store.Slug(repo), cfg.RsyncFlags); err != nil {
			die("demote: %v", err)
		}
	} else {
		if err := store.CopyTree(srcModel.Dir, dstDir); err != nil {
			die("demote: %v", err)
		}
	}
	fmt.Printf("ark: demoted %s -> %s\n", repo, dst.URL())
}

func cmdLink(args []string) {
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark link <org/model> [--dir DIR]  (default DIR: ~/ark/models)")
	}
	repo := pos[0]
	dir := config.Expand(flagOr(flags, "dir", "~/ark/models"))
	m, err := resolveSource(repo)
	if err != nil {
		die("%v", err)
	}
	if m.Vault.Remote() {
		die("%s lives only on remote vault %s — run `ark promote %s` first", repo, m.Vault.Name, repo)
	}
	os.MkdirAll(dir, 0o755)
	link := filepath.Join(dir, repo) // keep org/name hierarchy readable
	os.MkdirAll(filepath.Dir(link), 0o755)
	os.RemoveAll(link)
	if err := os.Symlink(m.Dir, link); err != nil {
		die("%v", err)
	}
	fmt.Printf("ark: linked %s -> %s\n", repo, m.Dir)
	fmt.Println("serve with: HF_HUB_OFFLINE=1 (point vLLM at the link path)")
}

func cmdVerify(args []string) {
	repoFilter := ""
	if len(args) > 0 {
		repoFilter = args[0]
	}
	var checked, bad int
	for _, name := range sortedVaultNames() {
		v := cfg.Vaults[name]
		if v.Remote() {
			continue
		}
		models, _ := store.ListLocal(v)
		for _, m := range models {
			if repoFilter != "" && store.RepoFromSlug(m.Slug) != repoFilter {
				continue
			}
			if m.Meta == nil || len(m.Meta.Sha256) == 0 {
				fmt.Printf("skip  %s (no manifest; redownload with --hashes)\n", store.RepoFromSlug(m.Slug))
				continue
			}
			fmt.Printf("check %s (%d files)...\n", store.RepoFromSlug(m.Slug), len(m.Meta.Sha256))
			for rel, want := range m.Meta.Sha256 {
				got, err := store.Sha256File(filepath.Join(m.Dir, rel))
				checked++
				if err != nil || got != want {
					bad++
					fmt.Printf("  BITROT %s: %v\n", rel, errHint(err))
				}
			}
		}
	}
	fmt.Printf("ark: %d files checked, %d mismatches\n", checked, bad)
	if bad > 0 {
		os.Exit(3)
	}
}

func cmdInfo(args []string) {
	if len(args) == 0 {
		die("usage: ark info <org/model>")
	}
	m, err := resolveSource(args[0])
	if err != nil {
		die("%v", err)
	}
	out := map[string]any{"repo": args[0], "vault": m.Vault.Name, "path": m.Dir}
	if m.Meta != nil {
		b, _ := json.Marshal(m.Meta)
		json.Unmarshal(b, &out)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

func cmdServe(args []string) {
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark serve <org/model> [--dir DIR]")
	}
	cmdLink(append(pos, "--dir", flagOr(flags, "dir", config.Expand("~/ark/models"))))
	fmt.Println()
	fmt.Println("export HF_HUB_OFFLINE=1")
	fmt.Printf("export ARK_MODEL_PATH=%s\n", filepath.Join(config.Expand(flagOr(flags, "dir", "~/ark/models")), pos[0]))
}

func cmdVault(args []string) {
	if len(args) == 0 {
		die("usage: ark vault ls | add <name> <host|local> <path> | rm <name>")
	}
	switch args[0] {
	case "ls":
		for _, n := range sortedVaultNames() {
			v := cfg.Vaults[n]
			kind := "local "
			if v.Remote() {
				kind = "remote"
			}
			fmt.Printf("%-10s %s %s\n", n, kind, v.URL())
		}
	case "add":
		if len(args) < 4 {
			die("usage: ark vault add <name> <host|local> <path>")
		}
		v := store.Vault{Kind: "local", Path: config.Expand(args[3])}
		if args[2] != "local" {
			v.Kind = "remote"
			v.Host = args[2]
		}
		cfg.Vaults[args[1]] = v
		if err := cfg.Save(); err != nil {
			die("%v", err)
		}
		fmt.Printf("vault %s added\n", args[1])
	case "rm":
		if len(args) < 2 {
			die("usage: ark vault rm <name>")
		}
		delete(cfg.Vaults, args[1])
		if err := cfg.Save(); err != nil {
			die("%v", err)
		}
		fmt.Printf("vault %s removed\n", args[1])
	default:
		die("unknown vault subcommand")
	}
}

// ---------- small utils ----------

func flagOr(flags map[string]string, k, def string) string {
	if v, ok := flags[k]; ok {
		return v
	}
	return def
}

func sortedVaultNames() []string {
	var names []string
	for n := range cfg.Vaults {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func localVaultNamed(name string) (store.Vault, error) {
	if name != "" {
		v, err := cfg.Vault(name)
		if err != nil {
			return store.Vault{}, err
		}
		if v.Remote() {
			return store.Vault{}, fmt.Errorf("vault %s is remote; --local must name a local vault", name)
		}
		return v, nil
	}
	for _, n := range sortedVaultNames() {
		if !cfg.Vaults[n].Remote() {
			return cfg.Vaults[n], nil
		}
	}
	return store.Vault{}, fmt.Errorf("no local vault configured")
}

func resolveSourceRepo(from, repo string) (store.Model, error) {
	slug := store.Slug(repo)
	if from == "" {
		return resolveSource(repo)
	}
	v, err := cfg.Vault(from)
	if err != nil {
		return store.Model{}, err
	}
	if v.Remote() {
		out, _ := sshRun(v.Host, fmt.Sprintf("test -d %q && echo yes", v.ModelDir(slug)))
		if strings.TrimSpace(out) != "yes" {
			return store.Model{}, fmt.Errorf("%s not found in vault %s", repo, v.Name)
		}
		return store.Model{Vault: v, Slug: slug, Dir: v.ModelDir(slug), Status: "remote"}, nil
	}
	d := v.ModelDir(slug)
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		return store.Model{}, fmt.Errorf("%s not found in vault %s", repo, v.Name)
	}
	m, _ := store.ReadMeta(d)
	return store.Model{Vault: v, Slug: slug, Dir: d, Meta: m}, nil
}

func errHint(err error) string {
	if err == nil {
		return "checksum mismatch"
	}
	return err.Error()
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var _ = bufio.NewReader
var _ = strconv.Itoa
