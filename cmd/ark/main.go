// Command ark (Arkadian) — custody of Hugging Face models across vaults.
//
//	ark list                     models in every vault, with free space
//	ark download <repo> --to V   fetch from HF or ModelScope into a vault
//	ark mv <repo> --from A --to B
//	                           move a model between vaults, or link it
//	ark rm <repo> [--vault V]    delete a model to free space
//	ark path <repo>              the path for vLLM or transformers
//	ark link <repo>              symlink a model for serving; unlink reverses it
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
	case "download":
		cmdDownload(rest)
	case "mv", "move":
		cmdMv(rest)
	case "rm", "remove":
		cmdRm(rest)
	case "path":
		cmdPath(rest)
	case "link":
		cmdLink(rest)
	case "unlink":
		cmdUnlink(rest)
	case "verify":
		cmdVerify(rest)
	case "info":
		cmdInfo(rest)
	case "vault":
		cmdVault(rest)
	case "help", "--help":
		usage()
	default:
		if use, gone := removed[cmd]; gone {
			fmt.Fprintf(os.Stderr, "ark %s is gone. Use:\n  %s\n", cmd, use)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

// removed maps old command names to what replaces them. Muscle memory gets a
// pointer, not a shrug.
var removed = map[string]string{
	"promote": "ark mv <repo> --from <vault> --to <local>",
	"demote":  "ark mv <repo> --from <local> --to <vault>",
	"pull":    "ark mv <repo> --from <vault> --to <local>",
	"down":    "ark mv <repo> --from <local> --to <vault>",
	"push":    "ark mv <repo> --from <local> --to <vault>",
	"model":   "ark list, ark download, ark mv, ark rm, ark path",
	"models":  "ark list",
	"serve":   "ark link <repo>, then the two exports it prints",
	"cp":      "ark mv <repo> --from A --to B --link (a move with no bytes)",
	"copy":    "ark mv <repo> --from A --to B --link (a move with no bytes)",
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
  mv <repo> --from A --to B
                           Move a model between vaults. --link moves no bytes
  rm <repo> [--vault V]    Delete a model. Asks before it deletes
  path <repo> [--vault V]  The path to hand to vLLM or transformers
  link <repo>              Symlink a model for serving. unlink reverses it
  verify [repo]            Check sha256 against .arkmeta.json
  info <repo>              Stored metadata as JSON
  vault ls|add|rm          Manage vaults
  version                  Version, about, and the repo link

Run 'ark <command> --help' for the flags of one command.

Config: ` + config.Path() + `
Vaults: local dir, mounted path (samba), or user@host:/path (rsync over ssh).`)
}

// cmdMv is the one move verb: it puts a model in another vault. It copies, then
// deletes the source. --link skips the bytes: the target gets a symlink and the
// source keeps the data.
func cmdMv(args []string) {
	if wantsHelp(args) {
		abortUsage(helpMv)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		usageErr("usage: ark mv <org/model> --from <vault> --to <vault> [--link] [--yes]\nsee: ark mv --help")
	}
	repo, slug := pos[0], store.Slug(pos[0])
	src, err := resolveSourceRepo(flagOr(flags, "from", ""), repo)
	if err != nil {
		if flags["from"] == "" {
			die("no --from vault. Run: ark vault ls\nknown vaults: %s\nhint: ark mv %s --from <vault> --to <vault>",
				strings.Join(sortedVaultNames(), ", "), repo)
		}
		die("%v\nhint: run: ark list", err)
	}
	dst := mustVault(flags, "to", cfg.DefaultTo)
	if src.Vault.Name == dst.Name {
		fmt.Printf("ark: %s is already in vault %s\n", repo, dst.Name)
		return
	}
	if flags["link"] != "" {
		// no question: nothing is deleted, the source keeps the bytes
		mvAsLink(repo, slug, src.Vault, dst)
		return
	}
	if !hasFlag(args, "yes", "force") &&
		!confirm(fmt.Sprintf("move %s: %s -> %s", repo, src.Vault.Name, dst.Name)) {
		die("cancelled")
	}
	if err := config.EnsureRemote(dst, sshRun); err != nil {
		die("prepare %s: %v (tip: enable SSH on the host, and ssh-copy-id %s)", dst.Name, err, dst.Host)
	}
	srcPath, dstPath := src.Dir, dst.ModelDir(slug)
	if src.Vault.Remote() {
		srcPath = src.Vault.URL() + "/models/" + slug
	}
	if dst.Remote() {
		dstPath = dst.URL() + "/models/" + slug
	}
	if src.Vault.Remote() || dst.Remote() {
		// rsync streams remote to remote too, through this machine, no local disk
		err = rsyncCopy(srcPath, dstPath, cfg.RsyncFlags)
	} else {
		err = store.CopyTree(srcPath, dstPath) // one disk: rsync would add nothing
	}
	if err != nil {
		die("move: %v", err)
	}
	// Drop the source only after a complete copy.
	if err := removeModel(src.Vault, slug); err != nil {
		die("copied, but the source is still there: %v", err)
	}
	fmt.Printf("ark: moved %s: %s -> %s\n", repo, src.Vault.Name, dst.Name)
}

// mvAsLink moves without bytes. The target vault holds a symlink and the source
// vault keeps the data. The source must be a path this machine can open.
func mvAsLink(repo, slug string, src, dst store.Vault) {
	if src.Remote() {
		die("%s is only reachable over ssh, and a link needs a real path. Mount that share\n(ark vault add %s samba <path>), or move without --link", repo, src.Name)
	}
	from, to := src.ModelDir(slug), dst.ModelDir(slug)
	if st, err := os.Lstat(to); err == nil && st.Mode()&os.ModeSymlink == 0 {
		die("%s is already a real copy in %s. Free the space first:\n  ark rm %s --vault %s", repo, dst.Name, repo, dst.Name)
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
	fmt.Printf("ark: %s is in %s now as a link. The bytes stay in %s\n", repo, dst.Name, src.Name)
}

// cmdRm deletes a model to free space. It asks first: this is the point of the
// tool, so the question is the safety bar.
func cmdRm(args []string) {
	if wantsHelp(args) {
		abortUsage(helpRm)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		usageErr("usage: ark rm <org/model> [--vault V] [--yes]\nsee: ark rm --help")
	}
	repo, slug := pos[0], store.Slug(pos[0])
	var targets []store.Vault
	if name := flags["vault"]; name != "" {
		v, err := cfg.Vault(name)
		if err != nil {
			die("%v. Run: ark vault ls", err)
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

// cmdPath prints the path of one model: what vLLM or transformers needs.
func cmdPath(args []string) {
	if wantsHelp(args) {
		abortUsage(helpPath)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		usageErr("usage: ark path <org/model> [--vault V]\nsee: ark path --help")
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

// cmdUnlink removes the serving symlink that ark link made.
func cmdUnlink(args []string) {
	if wantsHelp(args) {
		abortUsage(helpUnlink)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		usageErr("usage: ark unlink <org/model> [--dir DIR]\nsee: ark unlink --help")
	}
	link := filepath.Join(linkTargetDir(flags), pos[0])
	st, err := os.Lstat(link)
	if err != nil {
		die("no link at %s. Run: ark link %s", link, pos[0])
	}
	if st.Mode()&os.ModeSymlink == 0 {
		die("%s is not a link. Not touching it", link)
	}
	if err := os.Remove(link); err != nil {
		die("%v", errHint(err))
	}
	fmt.Printf("ark: unlinked %s\n", pos[0])
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
	if strings.Contains(name, "://") || strings.Contains(name, ":/") ||
		strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~") || strings.HasPrefix(name, ".") {
		v = parseLocation(name, name) // the same spellings `ark vault add` takes
		v.Name = name
		return v
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
(mounted) vault. Move it to a remote vault afterwards: ark mv <repo> --to V

Flags:
  --to V            Destination vault. Required unless default_to is set.
  --rev R           Revision or tag. Default: main (master on ModelScope).
  --source S        hf, hf-transfer, obscura, or modelscope. Default: auto.
  --hashes          Store a sha256 manifest. ark verify needs it.

Examples:
  ark download Qwen/Qwen3-8B --hashes --to spark
  ark download Qwen/Qwen3-8B --to nas --rev v1.5
  ark download Qwen/Qwen3-8B --source modelscope --rev master
`
	helpMv = `usage: ark mv <org/model> --from A --to B [flags]

The one move verb: it puts a model in another vault. It copies, then deletes
the source. Any end works: local, samba, or remote, remote to remote included.

Flags:
  --from V   Source vault. Required. Run ` + "`ark vault ls`" + ` for the names.
  --to V     Destination vault. Required unless default_to is set. A path
             works too: --to /mnt/usb
  --link     Move without bytes: B gets a symlink, A keeps the data. Needs a
             source this machine can open (local or a mounted share).
  --yes      Skip the confirmation question. For scripts.

Examples:
  ark mv Qwen/Qwen3-8B --from spark --to nas     the bytes leave the laptop
  ark mv Qwen/Qwen3-8B --from nas --to spark     the bytes come back
  ark mv Qwen/Qwen3-8B --from nas --to spark --link   load it, copy nothing
  ark mv Qwen/Qwen3-8B --from nas --to backup    machine to machine, no local disk
`
	helpRm = `usage: ark rm <org/model> [flags]

Delete a model to free space. It asks first: this is the point of the tool, so
the question is the safety bar.

Flags:
  --vault V  Touch one vault only. Without it, every copy it finds.
  --yes      Skip the confirmation question. For scripts.

Examples:
  ark rm Qwen/Qwen3-8B
  ark rm Qwen/Qwen3-8B --vault spark --yes
`
	helpLink = `usage: ark link <org/model> [flags]

Symlink a model into the local models dir. Nothing is copied.

This link is for serving: a path for vLLM or any loader that reads ~/ark/models.
For a model inside a vault without copying bytes, use: ark mv --link

Flags:
  --dir DIR   Where to put the symlink. Default: ~/ark/models

When does a symlink work?
  local vault        yes, always
  samba over NFS     yes
  samba over CIFS    usually no: the mount refuses symlink() with EOPNOTSUPP
  remote vault       no: there is no path to point at. Move it in first:
                     ark mv <repo> --from <remote> --to <local>
The link points at the vault copy, so mv or rm breaks it. Check with ark list.
`
	helpUnlink = `usage: ark unlink <org/model> [--dir DIR]

Remove the serving symlink that ark link made. It only removes a symlink, and
refuses a real directory. Nothing in a vault is touched.
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
	helpPath = `usage: ark path <org/model> [--vault V]

Print the path of one model: what vLLM or transformers needs. A remote copy
prints as user@host:path. A model in two vaults needs --vault.
`
	helpVault = `usage: ark vault <subcommand> [args]

  ark vault ls                       Name, kind, and path of every vault
  ark vault add <name> <location>    A path (/data, ~/, ./), a remote
                                     host:/path, or ssh://user@host/path.
                                     smb:// and nfs:// name a protocol ark does
                                     not speak: mount them, then add the
                                     mount point.
  ark vault add <name> <local|samba> <path>   the two-word spelling
  ark vault add <name> <user@host> <path>     the two-word spelling
  ark vault rm <name>                Drop a vault from the config. Asks first

Kinds:
  local   A directory on this machine.
  samba   A mounted path (CIFS/SMB or NFS). Used like a local dir.
  remote  user@host, rsync over ssh. The path is the remote dir.

A vault holds models. It has no download source. The source lives per model.

Examples:
  ark vault add usb /mnt/usb
  ark vault add lab user@192.0.2.10 /vol1/models
  ark vault add lab ssh://user@192.0.2.10/vol1/models
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

// usageErr reports a wrong command line. Exit 2, the bad-usage code.
func usageErr(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "ark: "+f+"\n", a...)
	os.Exit(2)
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
			return store.Model{Vault: v, Slug: slug, Dir: d, Meta: m}, nil
		}
	}
	for name, v := range cfg.Vaults {
		if !v.Remote() || name == localName {
			continue
		}
		out, _ := sshRun(v.Host, fmt.Sprintf("test -d %q && echo yes", v.ModelDir(slug)))
		if strings.TrimSpace(out) == "yes" {
			return store.Model{Vault: v, Slug: slug, Dir: v.ModelDir(slug)}, nil
		}
	}
	return store.Model{}, fmt.Errorf("model %q not found in any vault (try: ark download %s)", repo, repo)
}

// rsyncCopy copies a model dir between vaults with checksums.
// rsyncCopy copies a tree with the system rsync. Either side can be a
// user@host:path URL; remote to remote streams through this machine.
func rsyncCopy(srcDir, dstDir string, flags string) error {
	// ponytail: a colon means a remote URL. Ceiling: a local path with a colon in
	// its name skips its mkdir and rsync fails. Fix: pass the vault, not a string.
	if !strings.Contains(dstDir, ":") {
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return err
		}
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
					r.src = m.Meta.Source
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
				if s := m.Meta.Source; s != "" {
					r.src = s
				}
				if !m.Meta.Downloaded.IsZero() {
					r.date = m.Meta.Downloaded.Format("2006-01-02")
				}
			}
			if st, err := os.Lstat(m.Dir); err == nil && st.Mode()&os.ModeSymlink != 0 {
				r.where = "link" // an ark mv --link row: the bytes live in another vault
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
		cold := coldVaultHint()
		fmt.Printf("no cold copy (%d): a disk crash loses these\n", len(onlyWarm))
		if cold == "<vault>" {
			// no second vault yet: name one before the move command means anything
			fmt.Println("  ark vault add nas user@nas:/volume1/ark")
			cold = "nas"
		}
		for _, repo := range onlyWarm {
			fmt.Printf("  ark mv %s --from %s --to %s\n", repo, firstLocalVault(), cold)
		}
	}
	if len(onlyCold) > 0 {
		fmt.Printf("cold only (%d): not on local disk, slow or offline to serve\n", len(onlyCold))
		for _, repo := range onlyCold {
			fmt.Printf("  ark mv %s --from %s --to %s --link\n", repo, coldVaultHint(), firstLocalVault())
		}
	}
}

func cmdDownload(args []string) {
	if wantsHelp(args) {
		abortUsage(helpDownload)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		usageErr("usage: ark download <org/model> [--to vault] [--rev revision] [--source name]\nsee: ark download --help")
	}
	repo := pos[0]
	if flags["engine"] != "" {
		die("there is no --engine. Use --source %s (hf, hf-transfer, obscura, modelscope)", flags["engine"])
	}
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
		vault, err = reachableVault(toName)
		if err != nil {
			die("%v", err)
		}
	}
	if vault.Remote() {
		die("--to must be a LOCAL or SAMBA vault (sources cannot write over ssh). Download it locally, then: ark mv %s --to %s", repo, toName)
	}
	dest := "vault " + vault.Name
	if usedDefault {
		dest += " (default_to)"
	}
	eng, err := source.Detect(flagOr(flags, "source", cfg.Source))
	if err != nil {
		die("%v", err)
	}
	slug := store.Slug(repo)
	// stage next to the vault: the move into it is then a rename, not a copy
	dir := filepath.Dir(vault.Path)
	staging := filepath.Join(dir, "staging")
	if dir == "." {
		staging = filepath.Join(os.TempDir(), "ark-staging") // never the folder you ran from
	}
	tmpDir := filepath.Join(staging, slug)
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

}

func cmdLink(args []string) {
	if wantsHelp(args) {
		abortUsage(helpLink)
	}
	pos, flags := parseFlags(args)
	if len(pos) == 0 {
		usageErr("usage: ark link <org/model> [--dir DIR]\nsee: ark link --help")
	}
	repo := pos[0]
	m, err := resolveSource(repo)
	if err != nil {
		die("%v", err)
	}
	if m.Vault.Remote() {
		die("%s lives only on remote vault %s — there is no path to link. Run: ark mv %s --from %s --to <local>", repo, m.Vault.Name, repo, m.Vault.Name)
	}
	linkModel(repo, linkTargetDir(flags), m.Vault)
}

// linkTargetDir resolves where a serving link goes: --dir, else the default.
func linkTargetDir(flags map[string]string) string {
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
				"or move it local first: ark mv " + repo + " --from <vault> --to <local>"
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
		usageErr("usage: ark info <org/model>\nsee: ark info --help")
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

// parseLocation reads a one-argument vault location: a path, host:/path, or
// ssh://[user@host]/path. smb:// and friends name a protocol ark does not
// speak: that one needs a mount first.
func parseLocation(name, loc string) store.Vault {
	if i := strings.Index(loc, "://"); i >= 0 {
		scheme, rest := strings.ToLower(loc[:i]), loc[i+3:]
		switch scheme {
		case "file":
			return store.Vault{Kind: "local", Path: config.Expand(rest)}
		case "ssh":
			host, path := rest, ""
			if slash := strings.Index(rest, "/"); slash >= 0 {
				// keep the leading slash: a remote path stays absolute
				host, path = rest[:slash], rest[slash:]
			}
			if path == "" {
				usageErr("ssh://%s carries no path. Use: ark vault add %s ssh://<user@host>/<path>", host, name)
			}
			if host == "" {
				host = "localhost"
			}
			return store.Vault{Kind: "remote", Host: host, Path: path}
		default:
			usageErr("%s:// names a protocol ark does not speak. Mount the share, then:\n  ark vault add %s samba <mounted-path>", scheme, name)
		}
	}
	switch path := config.Expand(loc); {
	case strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "~") || strings.HasPrefix(loc, "."):
		return store.Vault{Kind: "local", Path: path}
	case strings.Contains(loc, ":"): // the rsync spelling: host:/path
		host, remote, ok := strings.Cut(loc, ":")
		if ok && strings.HasPrefix(remote, "/") {
			return store.Vault{Kind: "remote", Host: host, Path: remote}
		}
	}
	usageErr("%s is not a vault location. Use one of:\n  ark vault add %s <path>\n  ark vault add %s <user@host> <path>\n  ark vault add %s ssh://<user@host>/<path>", loc, name, name, name)
	return store.Vault{}
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
		if len(args) == 3 {
			cfg.Vaults[args[1]] = parseLocation(args[1], args[2])
		} else if len(args) < 4 {
			usageErr("usage:\n  ark vault add <name> <path>\n  ark vault add <name> local <path>\n  ark vault add <name> samba <mounted-path>\n  ark vault add <name> <user@host> <path>\n  ark vault add <name> ssh://<user@host>/<path>")
		} else {
			v := store.Vault{Kind: "local"}
			switch kind := strings.ToLower(args[2]); kind {
			case "local":
				v.Path = config.Expand(args[3])
			case "samba", "smb", "mount", "cifs":
				v.Kind = "samba" // a mounted path: same code path as local
				v.Path = config.Expand(args[3])
			case "remote": // the old 4-word spelling: point at the short one
				usageErr("no remote keyword. Use: ark vault add %s <user@host> <path>", args[1])
			default: // a host in the kind slot: user@host
				if strings.Contains(args[2], "://") {
					usageErr("one location is enough: ark vault add %s ssh://<user@host>/<path>", args[1])
				}
				v.Kind, v.Host, v.Path = "remote", args[2], args[3]
			}
			cfg.Vaults[args[1]] = v
		}
		v := cfg.Vaults[args[1]]
		if err := cfg.Save(); err != nil {
			die("%v", err)
		}
		fmt.Printf("vault %s added (%s): %s\n", args[1], v.KindLabel(), v.URL())
	case "rm":
		if len(args) < 2 {
			usageErr("usage: ark vault rm <name>\nsee: ark vault --help")
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

// reachableVault returns a vault this machine can open a path in: local or
// samba. An empty name picks the first one. It never returns a remote vault.
func reachableVault(name string) (store.Vault, error) {
	if name != "" {
		v, err := cfg.Vault(name)
		if err != nil {
			return store.Vault{}, err
		}
		if v.Remote() {
			return store.Vault{}, fmt.Errorf("vault %s is remote; this needs a local or mounted vault", name)
		}
		return v, nil
	}
	for _, n := range sortedVaultNames() {
		if !cfg.Vaults[n].Remote() {
			return cfg.Vaults[n], nil
		}
	}
	return store.Vault{}, fmt.Errorf("no local or mounted vault configured. Run: ark vault ls")
}

// firstLocalVault names a vault this machine can open, for hints.
func firstLocalVault() string {
	for _, n := range sortedVaultNames() {
		if !cfg.Vaults[n].Remote() {
			return n
		}
	}
	return "<local>"
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
		return store.Model{Vault: v, Slug: slug, Dir: v.ModelDir(slug)}, nil
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
