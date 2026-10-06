// Command ark (Arkadian) — custody of Hugging Face models across vaults.
//
//	ark list                     models in every vault, with free space
//	ark download <repo>          fetch from HF or ModelScope into a vault
//	ark promote <repo>           copy model from remote vault to local
//	ark demote <repo>            copy model from local to remote vault
//	ark link <repo>              symlink from local models dir to vault dir
//	ark verify [repo]            checksum models (vs .arkmeta.json)
//	ark info <repo>              show metadata
//	ark vault ls|add|rm          manage vaults
//	ark version                  print the version
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
	"time"

	"github.com/henry2man/arkadian/internal/config"
	"github.com/henry2man/arkadian/internal/source"
	"github.com/henry2man/arkadian/internal/store"
)

var cfg *config.Config

// version is set at build time: -ldflags "-X main.version=v0.1.0"
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			printVersion()
			return
		}
	}

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
	case "vault":
		cmdVault(rest)
	case "model", "models":
		cmdModel(rest)
	case "help", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

// repoURL is the project home page.
const repoURL = "https://github.com/henry2man/arkadian"

// printVersion prints a short about block: version, what it is, and the repo.
func printVersion() {
	fmt.Println("ark " + version + " — Arkadian")
	fmt.Println("Custody of AI models across vaults: local disks, mounted drives")
	fmt.Println("(SMB/NFS), and remote machines over ssh. Tired of cleaning your disk?")
	fmt.Println("Repo and docs: " + repoURL)
	os.Exit(0)
}

func usage() {
	fmt.Println(`ark — custody of Hugging Face models across vaults

Usage:
  ark <command> [args] [flags]

Commands:
  list                     Size per model, per vault, and free disk space
  download <repo> --to V   Fetch from HF or ModelScope into vault V
  promote <repo> --from V  Move a model into the local tier
  demote <repo> --from V --to V
                           Move a model out of the local tier
  model ls|download|mv|rm  Work on one model. Asks before it deletes
  link <repo>              Symlink a model into the local models dir
  verify [repo]            Check sha256 against .arkmeta.json
  info <repo>              Stored metadata as JSON
  vault ls|add|rm          Manage vaults
  version                  Version, about, and the repo link

Run 'ark <command> --help' for the flags of one command.

Config: ` + config.Path() + `
Vaults: local dir, mounted path (samba), or user@host:/path (rsync over ssh).`)
}

// cmdModel groups the per-model commands.
func cmdModel(args []string) {
	if len(args) == 0 {
		fmt.Print(helpModel)
		os.Exit(2)
	}
	if wantsHelp(args[:1]) {
		abortUsage(helpModel)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls", "list":
		cmdList(rest)
	case "download", "dl":
		cmdDownload(rest)
	case "info":
		cmdInfo(rest)
	case "path":
		cmdModelPath(rest)
	case "mv", "move":
		cmdModelMove(rest)
	case "rm", "remove", "delete":
		cmdModelRm(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown model subcommand: %s\n\n", sub)
		fmt.Print(helpModel)
		os.Exit(2)
	}
}

func cmdModelPath(args []string) {
	if wantsHelp(args) {
		abortUsage("usage: ark model path <org/model> [--vault V]\n\nPrint the path of a model inside a vault.\n")
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark model path <org/model> [--vault V]")
	}
	m, err := resolveSourceRepo(flags["vault"], pos[0])
	if err != nil {
		die("%v", err)
	}
	if m.Vault.Remote() {
		fmt.Printf("%s:%s\n", m.Vault.Host, m.Vault.ModelDir(store.Slug(pos[0])))
		return
	}
	fmt.Println(m.Dir)
}

// cmdModelMove moves a model between two vaults. At least one end must be a
// path on this machine: rsync cannot stream remote to remote from here.
func cmdModelMove(args []string) {
	if wantsHelp(args) {
		abortUsage("usage: ark model mv <org/model> --from A --to B\n\nMove a model between vaults. One of the two must be local or samba:\nthe copy goes through this machine. To move remote to remote, run two\nsteps: promote, then demote.\n")
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark model mv <org/model> --from A --to B")
	}
	repo, slug := pos[0], store.Slug(pos[0])
	src, err := resolveSourceRepo(flagOr(flags, "from", ""), repo)
	if err != nil {
		if flags["from"] == "" {
			die("no --from vault. Run: ark vault ls\nknown vaults: %s\nhint: ark model mv %s --from <vault> --to <vault>",
				strings.Join(sortedVaultNames(), ", "), repo)
		}
		die("%v", err)
	}
	dst := mustVault(flags, "to", cfg.DefaultTo)
	if src.Vault.Name == dst.Name {
		fmt.Printf("ark: %s is already in vault %s\n", repo, dst.Name)
		return
	}
	if src.Vault.Remote() && dst.Remote() {
		// ponytail: no remote-to-remote path. Ceiling: two steps through this
		// machine. Upgrade: rsync over an ssh ProxyJump, or run ark where both
		// vaults are visible.
		die("cannot copy remote to remote in one step. Run:\n  ark promote %s --from %s\n  ark demote %s --to %s",
			repo, src.Vault.Name, repo, dst.Name)
	}
	if !hasFlag(args, "yes", "force") &&
		!confirm(fmt.Sprintf("move %s: %s -> %s", repo, src.Vault.Name, dst.Name)) {
		die("cancelled")
	}
	if err := config.EnsureRemote(dst, sshRun); err != nil {
		die("prepare %s: %v", dst.Name, err)
	}
	switch {
	case dst.Remote():
		if err := rsyncCopy(src.Dir, dst.URL()+"/models/"+slug, cfg.RsyncFlags); err != nil {
			die("move: %v", err)
		}
	case src.Vault.Remote():
		if err := rsyncCopy(src.Vault.URL()+"/models/"+slug, dst.ModelDir(slug), cfg.RsyncFlags); err != nil {
			die("move: %v", err)
		}
	default:
		if err := store.CopyTree(src.Dir, dst.ModelDir(slug)); err != nil {
			die("move: %v", err)
		}
	}
	// Drop the source only after a complete copy.
	if err := removeModel(src.Vault, slug); err != nil {
		die("copied, but the source is still there: %v", err)
	}
	fmt.Printf("ark: moved %s: %s -> %s\n", repo, src.Vault.Name, dst.Name)
}

func cmdModelRm(args []string) {
	if wantsHelp(args) {
		abortUsage("usage: ark model rm <org/model> [--vault V] [--yes]\n\nDelete a model. This asks for confirmation unless --yes is set.\nWith --vault it touches one vault only; without it every copy it finds.\n")
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark model rm <org/model> [--vault V] [--yes]")
	}
	repo, slug := pos[0], store.Slug(pos[0])
	var targets []store.Vault
	if name := flags["vault"]; name != "" {
		v, err := cfg.Vault(name)
		if err != nil {
			die("%v", err)
		}
		targets = []store.Vault{v}
	} else {
		for _, n := range sortedVaultNames() {
			targets = append(targets, cfg.Vaults[n])
		}
	}
	var found []store.Vault
	for _, v := range targets {
		if modelInVault(v, slug) {
			found = append(found, v)
		}
	}
	if len(found) == 0 {
		die("%s is not in any vault. Run: ark list", repo)
	}
	if !hasFlag(args, "yes", "force") {
		names := make([]string, 0, len(found))
		for _, v := range found {
			names = append(names, v.Name)
		}
		if !confirm(fmt.Sprintf("delete %s from %s? This cannot be undone", repo, strings.Join(names, ", "))) {
			die("cancelled")
		}
	}
	for _, v := range found {
		if err := removeModel(v, slug); err != nil {
			die("%s in %s: %v", repo, v.Name, errHint(err))
		}
		fmt.Printf("ark: deleted %s from %s\n", repo, v.Name)
	}
}

// removeModel deletes a model from one vault, local or remote.
func removeModel(v store.Vault, slug string) error {
	if v.Remote() {
		_, err := sshRun(v.Host, fmt.Sprintf("rm -rf -- %q", v.ModelDir(slug)))
		return err
	}
	return store.RemoveModel(v, slug)
}

// modelInVault reports whether a model dir exists in a vault.
func modelInVault(v store.Vault, slug string) bool {
	if v.Remote() {
		out, err := sshRun(v.Host, fmt.Sprintf("test -d %q && echo yes", v.ModelDir(slug)))
		return err == nil && strings.TrimSpace(out) == "yes"
	}
	st, err := os.Stat(v.ModelDir(slug))
	return err == nil && st.IsDir()
}

// coldVaultHint names a cold vault (samba or remote) for hints.
func coldVaultHint() string {
	if cfg.DefaultTo != "" {
		if v, err := cfg.Vault(cfg.DefaultTo); err == nil && v.KindLabel() != "local" {
			return cfg.DefaultTo
		}
	}
	for _, n := range sortedVaultNames() {
		if cfg.Vaults[n].KindLabel() != "local" {
			return n
		}
	}
	return "<vault>"
}

// mustVault resolves the vault a flag names, or a config default. It never
// guesses: without a name it stops and points at `ark vault ls`. A path also
// works, so `--to /mnt/usb` needs no config entry.
func mustVault(flags map[string]string, flag, def string) store.Vault {
	name := flagOr(flags, flag, def)
	if name == "" {
		die("no --%s vault. Run: ark vault ls\nknown vaults: %s\nor pass a path: --%s /mnt/disk",
			flag, strings.Join(sortedVaultNames(), ", "), flag)
	}
	v, err := cfg.Vault(name)
	if err == nil {
		return v
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~") || strings.HasPrefix(name, ".") {
		return store.Vault{Name: name, Kind: "local", Path: config.Expand(name)}
	}
	die("%v. Run: ark vault ls", err)
	return store.Vault{}
}

// hasFlag reports whether name appears as a bare flag.
func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == "--"+n {
				return true
			}
		}
	}
	return false
}

// ---------- help texts ----------

const (
	helpList = `usage: ark list [--help]

List every model in every vault.
First a summary: kind, model count, model size, and free disk space.
Then one row per model: repo, vault, where, size, source, revision, date.
SOURCE is where the model came from: hf, hf-transfer, obscura, or modelscope.
At the end, ark flags the two states that bite: no cold copy, and cold only.

Aliases: ark ls
`
	helpDownload = `usage: ark download <org/model> [flags]

Fetch a model into a vault. The download lands in staging, then moves into
the vault. Nothing is written straight into the vault.

A destination is required. Pass --to, or set default_to in the config.
Sources cannot write over ssh, so the destination must be a local or samba
(mounted) vault. Ship it to a remote vault with --and-remote or ark demote.

Flags:
  --to V            Destination vault. Required unless default_to is set.
  --rev R           Revision or tag. Default: main (master on ModelScope).
  --source S        hf, hf-transfer, obscura, or modelscope. Default: auto.
  --engine S        Old name for --source.
  --hashes          Store a sha256 manifest. ark verify needs it.
  --and-remote V    After the local copy, ship a copy to vault V too.
  --link          Also symlink the model for serving, so vLLM can find it.
  --dir DIR       Where that link goes. Default: ~/ark/models

Examples:
  ark download Qwen/Qwen3-8B --hashes --to spark
  ark download Qwen/Qwen3-8B --to nas --rev v1.5 --and-remote backup
  ark download Qwen/Qwen3-8B --source modelscope --rev master
  ark download Qwen/Qwen3-8B --to usb --link

Aliases: ark dl
`
	helpPromote = `usage: ark promote <org/model> --from <vault> [flags]

Copy a model from a vault into the local tier. This is the "load model" step.

Promote copies and never deletes: the source vault keeps its copy. The copy is
real, not a hardlink, so each copy stands on its own. To move instead of copy:
ark model mv <repo> --from A --to B

Flags:
  --from V   Source vault. Required. Run ` + "`ark vault ls`" + ` for the names.
  --local V  Vault to write to. Default: the first local vault.
  --link     Virtual promote: a symlink in the local vault instead of a copy.
             The model shows up there and costs no space. Needs a source this
             machine can open (local or a mounted share), not an ssh vault.

Aliases: ark pull
`
	helpDemote = `usage: ark demote <org/model> --from <vault> --to <vault>

Copy a model from one vault into another. This is the "unload model" step.

Demote copies and never deletes, with a real copy, not a hardlink. To free the
space on the source: ark model mv <repo> --from A --to B

Flags:
  --from V  Source vault. Required. Run ` + "`ark vault ls`" + ` for the names.
  --to V    Destination vault. Required unless default_to is set.
            A path also works: --to /mnt/usb

Aliases: ark down, ark push
`
	helpLink = `usage: ark link <org/model> [flags]

Symlink a model into the local models dir. Nothing is copied.

This link is for serving: a path for vLLM or any loader that reads ~/ark/models.
For a model inside a vault without copying bytes, use: ark promote --link

Flags:
  --dir DIR   Where to put the symlink. Default: ~/ark/models

When does a symlink work?
  local vault        yes, always
  samba over NFS     yes
  samba over CIFS    usually no: the mount refuses symlink() with EOPNOTSUPP
  remote vault       no: there is no path to point at. Promote it first.
The link points at the vault copy, so demote, mv, or rm breaks it.
In doubt, use ` + "`ark promote <repo>`" + ` and link the local copy.
`
	helpVerify = `usage: ark verify [org/model]

Check files against the sha256 manifest in .arkmeta.json. Without a repo it
checks every model in every local and samba vault. Models with no manifest
are skipped. Remote vaults are not scanned; run ark verify on that machine.

Exit code 0 is clean. Exit code 1 means at least one mismatch. Good for cron.
`
	helpInfo = `usage: ark info <org/model>

Print the stored metadata as JSON: repo, revision, size, source, sha256s.
`
	helpModel = `usage: ark model <subcommand> [args]

Work on one model. The download source is Hugging Face by default. ModelScope
needs --source modelscope. Each model keeps the source that fetched it in its
own .arkmeta.json, so ark always knows where a copy came from.

  ark model ls                       Same as: ark list
  ark model download <repo> [flags]  Fetch a model. See: ark download --help
  ark model info <repo>              Metadata of one model
  ark model path <repo> [--vault V]  Path of a model inside a vault
  ark model mv <repo> --from A --to B
                                     Move a model between two vaults
  ark model rm <repo> [--vault V] [--yes]
                                     Delete a model. Asks for confirmation.

Destructive commands ask first. Pass --yes to skip the question, for scripts.
Every subcommand takes --help.
`
	helpVault = `usage: ark vault <subcommand> [args]

  ark vault ls                       Name, kind, and path of every vault
  ark vault add <name> <local|samba> <path>
  ark vault add <name> <user@host> <path>          rsync over ssh
  ark vault add <name> remote <user@host> <path>   the same, spelled out
  ark vault rm <name>                Drop a vault from the config. Asks first

Kinds:
  local   A directory on this machine.
  samba   A mounted path (CIFS/SMB or NFS). Used like a local dir.
  host    Anything else: user@host, rsync over ssh. <path> is the remote dir.

A vault holds models. It has no download source. The source lives per model.

Examples:
  ark vault add usb samba /mnt/usb
  ark vault add lab user@192.0.2.10 /vol1/models
`
)

// ---------- helpers ----------

// wantsHelp reports whether the args ask for help.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

// abortUsage prints command help and stops.
func abortUsage(text string) {
	fmt.Print(text)
	os.Exit(0)
}

// confirm asks yes/no on the terminal. Default is no.
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

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
		if !strings.HasPrefix(args[i], "--") {
			pos = append(pos, args[i])
			continue
		}
		name := strings.TrimPrefix(args[i], "--")
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			flags[name] = args[i+1] // --to nas
			i++
			continue
		}
		flags[name] = "true" // bare switch: --hashes, --link, --yes
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

// modelRow is one line of the `ark list` table.
type modelRow struct {
	repo, vault, where, size, src, rev, date string
}

// vsum is the per-vault line above the table: what it holds and its free space.
type vsum struct {
	name, kind string
	models     int
	bytes      int64
	total      int64
	free       int64
	spaceOK    bool
}

func cmdList(args []string) {
	if wantsHelp(args) {
		abortUsage(helpList)
	}
	var rows []modelRow
	var sums []vsum

	for _, name := range sortedVaultNames() {
		v := cfg.Vaults[name]
		s := vsum{name: name, kind: v.KindLabel()}
		if v.Remote() {
			if out, err := sshRun(v.Host, fmt.Sprintf("df -Pk %q", v.Path)); err == nil {
				s.total, s.free, s.spaceOK = store.DfSpaces(out)
			}
			models, err := store.ListRemote(v, sshRun)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ark: vault %s: warning: %v\n", name, err)
				continue
			}
			for _, m := range models {
				r := modelRow{repo: store.RepoFromSlug(m.Slug), vault: name, where: v.KindLabel()}
				if m.Meta != nil && m.Meta.Repo != "" {
					r.size, r.rev, r.date = humanBytes(m.Meta.SizeBytes), m.Meta.Revision, m.Meta.Downloaded.Format("2006-01-02")
					r.src = m.Meta.SourceName()
					s.bytes += m.Meta.SizeBytes
				} else {
					r.size, r.rev, r.date = "?", "-", "-"
				}
				s.models++
				rows = append(rows, r)
			}
			sums = append(sums, s)
			continue
		}
		if t, f, err := store.Free(v.Path); err == nil {
			s.total, s.free, s.spaceOK = t, f, true
		}
		models, err := store.ListLocal(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ark: vault %s: %v\n", name, err)
			continue
		}
		for _, m := range models {
			r := modelRow{repo: store.RepoFromSlug(m.Slug), vault: name, where: v.KindLabel(),
				rev: "-", date: "-", size: "?", src: "-"}
			sz, _, derr := store.DirSize(m.Dir)
			if m.Meta != nil && m.Meta.SizeBytes > 0 {
				sz, derr = m.Meta.SizeBytes, nil // the manifest also covers a linked model
			}
			if derr == nil {
				s.bytes += sz
				r.size = humanBytes(sz)
			}
			if m.Meta != nil {
				r.rev = m.Meta.Revision
				if s := m.Meta.SourceName(); s != "" {
					r.src = s
				}
				if !m.Meta.Downloaded.IsZero() {
					r.date = m.Meta.Downloaded.Format("2006-01-02")
				}
			}
			if st, err := os.Lstat(m.Dir); err == nil && st.Mode()&os.ModeSymlink != 0 {
				r.where = "link" // a virtual promote: the bytes live in another vault
			}
			s.models++
			rows = append(rows, r)
		}
		sums = append(sums, s)
	}

	w := [4]int{len("VAULT"), len("KIND"), len("MODELS"), len("MODELS SIZE")}
	for _, s := range sums {
		w[0] = max(w[0], len(s.name))
		w[1] = max(w[1], len(s.kind))
	}
	fmt.Printf("%-*s  %-6s  %6s  %11s  %s\n", w[0], "VAULT", "KIND", "MODELS", "MODEL SIZE", "DISK")
	for _, s := range sums {
		disk := "?"
		if s.spaceOK {
			disk = fmt.Sprintf("%s free of %s", humanBytes(s.free), humanBytes(s.total))
			if s.total > 0 && s.free*10 < s.total {
				disk += "  LOW"
			}
		}
		fmt.Printf("%-*s  %-6s  %6d  %11s  %s\n", w[0], s.name, s.kind, s.models,
			humanBytes(s.bytes), disk)
	}

	if len(rows) == 0 {
		fmt.Println("\nno models yet. Run: ark download <org/model>")
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].repo != rows[j].repo {
			return rows[i].repo < rows[j].repo
		}
		return rows[i].vault < rows[j].vault
	})
	cw := [5]int{len("REPO"), len("VAULT"), len("WHERE"), len("SIZE"), len("SOURCE")}
	for _, r := range rows {
		cw[0] = max(cw[0], len(r.repo))
		cw[1] = max(cw[1], len(r.vault))
		cw[2] = max(cw[2], len(r.where))
		cw[3] = max(cw[3], len(r.size))
		cw[4] = max(cw[4], len(r.src))
	}
	fmt.Printf("\n%-*s  %-*s  %-*s  %*s  %-*s  %-7s  %s\n", cw[0], "REPO", cw[1], "VAULT",
		cw[2], "WHERE", cw[3], "SIZE", cw[4], "SOURCE", "REVISION", "DATE")
	for _, r := range rows {
		fmt.Printf("%-*s  %-*s  %-*s  %*s  %-*s  %-7s  %s\n", cw[0], r.repo, cw[1], r.vault,
			cw[2], r.where, cw[3], r.size, cw[4], r.src, trunc(r.rev, 7), r.date)
	}
	listRisk(rows)
}

// listRisk points out the two states that bite: a model with no cold copy, and
// a model that only a cold vault holds. It prints nothing when all is well.
func listRisk(rows []modelRow) {
	warm := map[string]bool{}
	cold := map[string]bool{}
	links := 0
	for _, r := range rows {
		if r.where == "link" {
			links++ // reachable here, but the bytes stay where they are
		}
		if r.where == "local" || r.where == "link" {
			warm[r.repo] = true
		} else {
			cold[r.repo] = true // a samba mount or a remote vault is a cold copy
		}
	}
	var onlyWarm, onlyCold []string
	for repo := range warm {
		if !cold[repo] {
			onlyWarm = append(onlyWarm, repo)
		}
	}
	for repo := range cold {
		if !warm[repo] {
			onlyCold = append(onlyCold, repo)
		}
	}
	sort.Strings(onlyWarm)
	sort.Strings(onlyCold)
	if len(onlyWarm) == 0 && len(onlyCold) == 0 {
		switch {
		case links > 0:
			fmt.Printf("\n%d row(s) are links: the bytes stay in the vault that holds them\n", links)
		case len(rows) > 0:
			fmt.Println("\nevery model has a copy in more than one vault")
		}
		return
	}
	fmt.Println()
	if len(onlyWarm) > 0 {
		fmt.Printf("no cold copy (%d): a disk crash loses these\n", len(onlyWarm))
		for _, repo := range onlyWarm {
			fmt.Printf("  ark demote %s --to %s\n", repo, coldVaultHint())
		}
	}
	if len(onlyCold) > 0 {
		fmt.Printf("cold only (%d): not on local disk, slow or offline to serve\n", len(onlyCold))
		for _, repo := range onlyCold {
			fmt.Printf("  ark promote %s\n", repo)
		}
	}
}

func cmdDownload(args []string) {
	if wantsHelp(args) {
		abortUsage(helpDownload)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark download <org/model> [--to vault] [--rev revision] [--source name]\nsee: ark download --help")
	}
	repo := pos[0]
	toName := cfg.DefaultTo
	usedDefault := true
	if v, ok := flags["to"]; ok {
		toName, usedDefault = v, false
	}
	if toName == "" {
		die("no destination. Pick a vault: ark download %s --to <vault>\nknown vaults: %s\nor set default_to in %s",
			repo, strings.Join(sortedVaultNames(), ", "), config.Path())
	}
	vault, err := cfg.Vault(toName)
	if err != nil {
		vault, err = localVaultNamed(toName)
		if err != nil {
			die("%v", err)
		}
	}
	if vault.Remote() {
		die("--to must be a LOCAL or SAMBA vault; then use `ark demote %s --to %s` to ship it (sources cannot write over ssh)", repo, toName)
	}
	dest := "vault " + vault.Name
	if usedDefault {
		dest += " (default_to)"
	}
	eng, err := source.Detect(flagOr(flags, "source", flagOr(flags, "engine", cfg.SourceName())))
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

	fmt.Printf("ark: downloading %s via %s -> staging for %s\n", repo, eng.Name(), dest)
	if err := eng.Fetch(repo, flags["rev"], tmpDir); err != nil {
		die("download failed: %v", err)
	}

	meta := store.Meta{
		Repo:       repo,
		RepoType:   flagOr(flags, "repo-type", "model"),
		Revision:   flagOr(flags, "rev", "main"),
		Source:     eng.Name(),
		Downloaded: time.Now().UTC(),
	}
	if flags["hashes"] != "" {
		fmt.Println("ark: hashing files (manifest)...")
		meta.Sha256 = map[string]string{}
		for _, rel := range source.ListFiles(tmpDir) {
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

	if flags["link"] != "" {
		linkModel(repo, linkTargetDir(flags), vault)
	}

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
	if wantsHelp(args) {
		abortUsage(helpPromote)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark promote <org/model> --from <vault> [--local vault]\nsee: ark promote --help")
	}
	repo := pos[0]
	fromName := flagOr(flags, "from", "")
	if fromName == "" {
		die("no --from vault. Run: ark vault ls\nknown vaults: %s\nhint: ark promote %s --from <vault>",
			strings.Join(sortedVaultNames(), ", "), repo)
	}
	src, err := resolveSourceRepo(fromName, repo)
	if err != nil {
		die("%v\nhint: run: ark vault ls, then: ark promote %s --from <vault>", err, repo)
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
	if flags["link"] != "" {
		virtualPromote(repo, src.Vault, local)
		return
	}
	if src.Vault.Remote() {
		if err := rsyncCopy(src.Vault.URL()+"/models/"+store.Slug(repo), dst, cfg.RsyncFlags); err != nil {
			die("promote: %v", err)
		}
	} else if err := store.CopyTreeFull(src.Dir, dst); err != nil {
		die("promote: %v", err)
	}
	fmt.Printf("ark: promoted %s -> %s (copy; %s still holds its own)\n", repo, dst, src.Vault.Name)
}

// virtualPromote puts a model in the local vault as a symlink. No bytes move:
// the local entry points at the copy a source vault already holds, so the model
// is there and costs no space. The source must be a path this machine can open.
func virtualPromote(repo string, src, dst store.Vault) {
	if src.Remote() {
		die("%s is only reachable over ssh, and a link needs a real path. Mount that share,\nadd it with `ark vault add %s samba <path>`, or promote without --link", repo, src.Name)
	}
	from, to := src.ModelDir(store.Slug(repo)), dst.ModelDir(store.Slug(repo))
	if st, err := os.Lstat(to); err == nil && st.Mode()&os.ModeSymlink == 0 {
		die("%s is already a real copy in %s. Free the space first:\n  ark model rm %s --vault %s", repo, dst.Name, repo, dst.Name)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		die("%v", err)
	}
	if err := os.RemoveAll(to); err != nil {
		die("%v", err)
	}
	if err := os.Symlink(from, to); err != nil {
		die("link into %s: %v", dst.Name, errHint(err))
	}
	size := "?"
	if sz, _, err := store.DirSize(from); err == nil {
		size = humanBytes(sz)
	}
	fmt.Printf("ark: promoted %s -> %s as a link: %s stays in %s and costs no local space\n",
		repo, dst.Name, size, src.Name)
}

func cmdDemote(args []string) {
	if wantsHelp(args) {
		abortUsage(helpDemote)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark demote <org/model> --from <vault> --to <vault>\nsee: ark demote --help")
	}
	repo := pos[0]
	dst := mustVault(flags, "to", cfg.DefaultTo)
	fromName := flagOr(flags, "from", "")
	if fromName == "" {
		die("no --from vault. Run: ark vault ls\nknown vaults: %s\nhint: ark demote %s --from <vault> --to %s",
			strings.Join(sortedVaultNames(), ", "), repo, dst.Name)
	}
	srcModel, err := resolveSourceRepo(fromName, repo)
	if err != nil {
		die("%v\nhint: run: ark list, or: ark vault ls", err)
	}
	if dst.Remote() {
		if err := config.EnsureRemote(dst, sshRun); err != nil {
			die("prepare %s: %v (tip: enable SSH in DSM Control Panel, and ssh-copy-id %s)", dst.Name, err, dst.Host)
		}
	}
	dstDir := dst.ModelDir(store.Slug(repo))
	if srcModel.Vault.Remote() && dst.Remote() {
		die("cannot copy remote to remote in one step. Run:\n  ark promote %s --from %s\n  ark demote %s --from %s --to %s",
			repo, srcModel.Vault.Name, repo, "local", dst.Name)
	}
	fmt.Printf("ark: demote %s: %s -> %s\n", repo, srcModel.Vault.URL(), dst.URL())
	switch {
	case dst.Remote():
		if err := rsyncCopy(srcModel.Dir, dst.URL()+"/models/"+store.Slug(repo), cfg.RsyncFlags); err != nil {
			die("demote: %v", err)
		}
	case srcModel.Vault.Remote():
		if err := rsyncCopy(srcModel.Vault.URL()+"/models/"+store.Slug(repo), dstDir, cfg.RsyncFlags); err != nil {
			die("demote: %v", err)
		}
	default:
		if err := store.CopyTreeFull(srcModel.Dir, dstDir); err != nil {
			die("demote: %v", err)
		}
	}
	fmt.Printf("ark: demoted %s -> %s\n", repo, dst.URL())
}

func cmdLink(args []string) {
	if wantsHelp(args) {
		abortUsage(helpLink)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		die("usage: ark link <org/model> [--dir DIR]\nsee: ark link --help")
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
	linkModel(repo, dir, m.Vault)
}

// linkTargetDir resolves the link location: --link DIR, then --dir, then the
// default local models dir.
func linkTargetDir(flags map[string]string) string {
	if d := flags["link"]; d != "" && d != "true" {
		return config.Expand(d)
	}
	return config.Expand(flagOr(flags, "dir", "~/ark/models"))
}

// linkModel symlinks a vault model into dir. It works when the vault is a path
// on this machine and that filesystem takes symlinks. See helpLink.
func linkModel(repo, dir string, v store.Vault) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		die("%v", err)
	}
	link := filepath.Join(dir, repo) // keep the org/name hierarchy readable
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		die("%v", err)
	}
	if err := os.RemoveAll(link); err != nil {
		die("%v", err)
	}
	if err := os.Symlink(v.ModelDir(store.Slug(repo)), link); err != nil {
		hint := ""
		if v.KindLabel() == "samba" {
			hint = "\nhint: most CIFS/SMB mounts cannot hold symlinks. Mount with NFS, " +
				"or run `ark promote " + repo + "` and link the local copy."
		}
		die("%s: %v%s", link, errHint(err), hint)
	}
	fmt.Printf("ark: linked %s -> %s\n", repo, v.ModelDir(store.Slug(repo)))
	fmt.Println("serve it offline:")
	fmt.Println("  export HF_HUB_OFFLINE=1")
	fmt.Printf("  export ARK_MODEL_PATH=%s\n", link)
}

func cmdVerify(args []string) {
	if wantsHelp(args) {
		abortUsage(helpVerify)
	}
	repoFilter := ""
	if len(args) > 0 {
		repoFilter = args[0]
	}
	var checked, bad int
	for _, name := range sortedVaultNames() {
		v := cfg.Vaults[name]
		if v.Remote() {
			// ponytail: remote vaults are not scanned. Ceiling: remote bit-rot stays
			// unseen. Upgrade: stream the hashes with one ssh call per model.
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
		os.Exit(1)
	}
}

func cmdInfo(args []string) {
	if wantsHelp(args) {
		abortUsage(helpInfo)
	}
	if len(args) == 0 {
		die("usage: ark info <org/model>\nsee: ark info --help")
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

func cmdVault(args []string) {
	if len(args) == 0 {
		fmt.Print(helpVault)
		os.Exit(2)
	}
	if wantsHelp(args) {
		abortUsage(helpVault)
	}
	switch args[0] {
	case "ls":
		for _, n := range sortedVaultNames() {
			v := cfg.Vaults[n]
			fmt.Printf("%-10s %-7s %s\n", n, v.KindLabel(), v.URL())
		}
	case "add":
		if len(args) < 4 {
			die("usage:\n  ark vault add <name> local <path>\n  ark vault add <name> samba <mounted-path>\n  ark vault add <name> <user@host> <path>\n  ark vault add <name> remote <user@host> <path>")
		}
		v := store.Vault{Kind: "local"}
		switch kind := strings.ToLower(args[2]); kind {
		case "local":
			v.Path = config.Expand(args[3])
		case "samba", "smb", "mount", "cifs":
			v.Kind = "samba" // a mounted path: same code path as local
			v.Path = config.Expand(args[3])
		case "remote":
			if len(args) < 5 {
				die("usage: ark vault add <name> remote <user@host> <path>")
			}
			v.Kind, v.Host, v.Path = "remote", args[3], args[4]
		default: // a host in the kind slot: user@host or host:/path
			v.Kind, v.Host, v.Path = "remote", args[2], args[3]
		}
		cfg.Vaults[args[1]] = v
		if err := cfg.Save(); err != nil {
			die("%v", err)
		}
		fmt.Printf("vault %s added (%s): %s\n", args[1], v.KindLabel(), v.URL())
	case "rm":
		if len(args) < 2 {
			die("usage: ark vault rm <name>\nsee: ark vault --help")
		}
		name := args[1]
		v, ok := cfg.Vaults[name]
		if !ok {
			die("no vault %q. Known vaults: %s", name, strings.Join(sortedVaultNames(), ", "))
		}
		if !hasFlag(args[2:], "yes", "force") &&
			!confirm(fmt.Sprintf("drop vault %s (%s) from the config? Files stay on disk at %s", name, v.KindLabel(), v.Path)) {
			die("cancelled")
		}
		delete(cfg.Vaults, name)
		if err := cfg.Save(); err != nil {
			die("%v", err)
		}
		fmt.Printf("vault %s removed from the config. Files on disk were not touched.\n", name)
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
