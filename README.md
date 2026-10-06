# Arkadian — `ark`

**Tired of cleaning your disk?** Arkadian keeps Hugging Face models in **vaults**.
A single-binary CLI (Go) that moves LLM models across storage tiers: a fast **local
tier** (NVMe on your GPU box) and one or more **cold vaults** (Synology NAS, mounted
drive, another machine). `ark list` shows the size of every model, the size of every
vault, and the free space left. Download once, verify, serve offline — even if
Hugging Face itself disappears.

> Why "Arkadian"? Ark, the vessel, and Arcadia, the place that stays safe. If the
> model-zoo floods (CDN outage, repo takedowns, deprecations), you want your own
> home for them: curated, checksummed, reachable without internet.
>
> The project is Arkadian. The command you type stays `ark`, and so do its names:
> `ARK_CONFIG`, `~/.arkadian/config.json`, and `.arkmeta.json`.

## The model

```
                 ark download Qwen/Qwen3-32B --and-remote nas
                 │ (source: hf / hf_transfer / obscura / modelscope)
                 ▼
   ┌─────────────────────┐   ark demote (rsync)   ┌──────────────────────────┐
   │ vault: spark (local) │ ◄──────────────────►  │ vault: nas (remote, ssh)  │
   │ ~/ark/spark/models/  │   ark promote (rsync) │ /volume1/ark/models/       │
   │  Qwen--Qwen3-32B/    │                       │  Qwen--Qwen3-32B/          │
   └─────────┬───────────┘                        └──────────────────────────┘
             │ ark link
             ▼
   ~/ark/models/Qwen/Qwen3-32B  ──symlink──►  served offline (HF_HUB_OFFLINE=1)
```

- **Layout = HF `--local-dir` layout**: a vault dir *is* a model path you can hand
  straight to vLLM/transformers. No proprietary format, no conversion.
- **Vault kinds**: `local` (a directory), `samba` (a mounted path, CIFS/SMB or NFS),
  or `remote` (`user@host`, rsync-over-ssh). Multiple vaults supported — NVMe, NAS,
  USB dock, second machine.
- **Sources**: Hugging Face (`hf`, `hf_transfer`, `obscura`) and ModelScope
  (`--source modelscope`). A vault does not have a source type: each model keeps the
  source that fetched it in its own `.arkmeta.json`.
- **Two kinds of symlink**. `ark link` makes a link for *serving*: a path in
  `~/ark/models` that points at the vault copy. `ark promote --link` makes a link
  inside a *vault*: the model shows up in the local vault and costs no space. Both
  need a path the machine can open, so they work with local and mounted vaults, not
  with an ssh vault. A link is not a backup: the bytes live in one place.
- **Manifests**: `--hashes` stores a sha256 manifest (`.arkmeta.json`); `ark verify`
  detects NAS bit-rot.

## Install

```bash
# Go toolchain (works today, no other dependencies):
go install github.com/henry2man/arkadian/cmd/ark@latest

# Or build from source:
git clone https://github.com/henry2man/arkadian && cd arkadian && go build -o bin/ark ./cmd/ark

# Homebrew, planned (not published yet, the repo is private):
# brew tap henry2man/arkadian && brew install --cask arkadian   # installs the ark command
```

Requirements: Go ≥ 1.27 to build. Runtime needs: `rsync` and `ssh` (only for remote
vaults), and one download source in `PATH`: `hf` from `pip install huggingface_hub[cli]`
(or `hf_transfer`, `obscura`, `modelscope`).

## First run

On first use `ark` writes `~/.arkadian/config.json` with a default setup.

```json
{
  "vaults": {
    "spark": { "kind": "local",  "path": "~/ark/spark" },
    "nas":   { "kind": "remote", "host": "user@nas", "path": "/volume1/ark" }
  },
  "default_to": "nas",
  "rsync_flags": "-a --inplace --partial"
}
```

Adapt it by hand or with `ark vault add|rm`. Override the location with `ARK_CONFIG`.
Set `"source": "modelscope"` to make ModelScope the default download source.

### Synology setup (one-time)

1. DSM → Control Panel → **Terminal & SNMP** → enable SSH.
2. `ssh-copy-id user@nas` (ark uses BatchMode ssh; no interactive passwords).
3. First `demote`/`download` creates `/volume1/ark/models` automatically.

SMB/CIFS is *not* required (rsync-over-ssh is faster and preserves HF hardlinks).
You can still point a `local`-kind vault at a mounted CIFS path if you prefer —
`rsync` handles it, just without hardlink dedup.

## Commands

`<arg>` is required. `[--flag V]` is optional. Every command answers `--help`.

| Command | What it does |
|---|---|
| `ark list` | Size per model, size per vault, free disk space, and the copies that are at risk |
| `ark vault ls` | Name, kind, and path of every vault |
| `ark download <repo> --to V [--rev R] [--source S] [--hashes] [--and-remote V] [--link]` | Fetch from HF or ModelScope into staging, then into the vault |
| `ark promote <repo> --from V [--local V] [--link]` | Vault → local tier. The "load model" move. `--link` = virtual: a symlink in the local vault, no bytes, no space |
| `ark demote <repo> --from V --to V` | Local tier → vault. The "unload model" move |
| `ark model ls` | Same as `ark list` |
| `ark model download <repo> [--flags]` | Same as `ark download` |
| `ark model path <repo> [--vault V]` | Path of one model, local or `host:path` |
| `ark model mv <repo> --from V --to V` | Move between two vaults. Asks first, `--yes` skips it |
| `ark model rm <repo> [--vault V] [--yes]` | Delete a model. Asks first, `--yes` skips it |
| `ark link <repo> [--dir DIR]` | Symlink a model → `~/ark/models/<org>/<name>`, then print the two exports to serve it offline |
| `ark verify [repo]` | sha256 check against the manifest. Bit-rot sweep |
| `ark info <repo>` | Stored metadata as JSON |
| `ark vault add <name> local <path>` | A directory on this machine |
| `ark vault add <name> samba <path>` | A mounted CIFS/SMB/NFS path |
| `ark vault add <name> <user@host> <path>` | A remote machine, rsync over ssh |
| `ark vault rm <name> [--yes]` | Drop a vault from the config. Never deletes files |
| `ark version` | Version, one-line about, and the repo link |

### Conventions

- Flags are long and spelled out: `--to nas`. No short flags, except `-h` and `-v`.
- A flag with no value is a switch: `--hashes`, `--link`, `--yes`.
- Destructive commands ask. `--yes` answers yes for scripts.
- The vault is named, never guessed: a missing `--from` or `--to` stops and says
  `Run: ark vault ls`.
- Errors go to stderr and exit 1. Bad usage exits 2. `--help` exits 0.
- Paths in a flag are accepted too: `--to /mnt/usb` needs no config entry.

### Example flow

```bash
ark download Qwen/Qwen3-8B --hashes --and-remote nas   # HF -> local -> NAS
ark download Qwen/Qwen3-8B --source modelscope         # same model, from ModelScope
ark demote  Qwen/Qwen3-8B                              # (if you forgot --and-remote)
ark list                                               # sizes per vault + free space
ark link Qwen/Qwen3-8B                                 # symlink + the export lines
export HF_HUB_OFFLINE=1
vllm serve $(ark model path Qwen/Qwen3-8B) ...          # fully offline
ark verify                                             # monthly cron: bit-rot sweep
```

## How this code is written

This repo uses [Ponytail](https://github.com/DietrichGebert/ponytail). It is a skill
for AI agents, pinned for this project in `.pi/settings.json`. One rule: write the
least code that works. Before new code, stop at the first rung that holds.

1. Do not build it. Someone asked; that is not a reason.
2. Reuse what lives in `internal/` already.
3. Use the Go standard library.
4. Use the platform: `rsync`, `ssh`, hardlinks, `df`.
5. Write the minimum that works, in the fewest files.

Kept honest by `gofmt`, `go vet`, and a real run of the command you changed.
Deliberate shortcuts carry a `// ponytail:` comment that names the ceiling and the
way out. The binary stays free of external Go modules.

## Design notes

- **Staging**: downloads land in `<vault-root>/../staging`, then hardlink-move into
  the vault — an interrupted download never pollutes a vault.
- **Transfers**: `rsync -a --inplace --partial` → resumable, checksum-safe, and HF's
  content-addressed cache never re-copies an unchanged blob.
- **Sources**: auto-detects `hf` → `hf_transfer` → `obscura`. ModelScope is opt-in:
  `--source modelscope`. The seam is `internal/source`, so a torrent or mirror
  backend is one small file.
- **Integrity**: `.arkmeta.json` (repo, revision, sha256s, size). `ark verify` exits
  non-zero on mismatch — wire it into cron/healthchecks.

## Honest limitations

- Arkadian is for a **curated** lifeboat, not a full mirror. HF's *total* archive
  is petabytes.
- `download` writes to a **local** vault first (download sources cannot write over
  ssh), then ships. Disk on the staging box must fit the largest single model.
- Remote `list` needs ssh reachability; degraded vaults warn rather than fail.

## Releases

Nothing is published yet. The repo is private, so the release pipeline is ready
but idle.

- `ci.yml` runs on every push and pull request: `gofmt`, `go vet`, `go test`,
  `go build`. This works on a private repo.
- `release.yml` uses [GoReleaser](https://goreleaser.com). It starts by hand for
  now (Actions → Release → Run workflow). It builds linux/darwin × amd64/arm64
  and opens the GitHub Release.
- The Homebrew block in `.goreleaser.yaml` is commented out. Homebrew needs a
  public repo and a second repo as the tap.

Try the build locally. Nothing leaves your machine:

```bash
goreleaser release --snapshot --clean --skip=publish   # writes dist/ only
```

To publish later: make the repo public, uncomment the two marked blocks, create
the empty tap repo `henry2man/homebrew-arkadian`, add a token for it as the
`HOMEBREW_TAP_GITHUB_TOKEN` secret, and set Actions → General → Workflow
permissions to *Read and write*. Then a tag publishes: `git tag v0.1.0 &&
git push origin v0.1.0`. The version comes from the tag; `ark version` prints it.

## Thanks

- [Ponytail](https://github.com/DietrichGebert/ponytail), the lazy senior dev. Most
  of this code is code that was never written, and the parts that exist are shorter
  because he looked at them.
- [pi](https://github.com/earendil-works/pi), the coding agent this repo was built
  with. The Go tooling, the vault tests, and the docs all went through it.

## License

MIT — see [LICENSE](LICENSE).
