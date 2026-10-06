# Project ARK — `ark`

**Custody of Hugging Face models across vaults.** A single-binary CLI (Go) that keeps
LLM models safe across storage tiers: a fast **local tier** (NVMe on your GPU box) and
one or more **cold vaults** (Synology NAS, external disk, another machine). Download once,
verify, serve offline — even if Hugging Face itself disappears.

> Why "ARK"? If the model-zoo floods (CDN outage, repo takedowns, deprecations),
> you want your own vessel: curated, checksummed, reachable without internet.

## The model

```
                 ark download Qwen/Qwen3-32B --and-remote nas
                 │ (engine: hf CLI / hf_transfer / obscura)
                 ▼
   ┌─────────────────────┐   ark demote (rsync)   ┌──────────────────────────┐
   │ vault: spark (local) │ ◄──────────────────►  │ vault: nas (remote, ssh)  │
   │ ~/ark/spark/models/  │   ark promote (rsync) │ /volume1/ark/models/       │
   │  Qwen--Qwen3-32B/    │                       │  Qwen--Qwen3-32B/          │
   └─────────┬───────────┘                        └──────────────────────────┘
             │ ark link / ark serve
             ▼
   ~/ark/models/Qwen/Qwen3-32B  ──symlink──►  served offline (HF_HUB_OFFLINE=1)
```

- **Layout = HF `--local-dir` layout**: a vault dir *is* a model path you can hand
  straight to vLLM/transformers. No proprietary format, no conversion.
- **Vaults are pluggable**: `local` (a directory) or `remote` (`user@host`, rsync-over-ssh).
  Multiple vaults supported — NAS, USB dock, second machine.
- **Manifests**: `--hashes` stores a sha256 manifest (`.arkmeta.json`); `ark verify`
  detects NAS bit-rot.

## Install

```bash
go install github.com/henry2man/ark/cmd/ark@latest
# or build from source:
git clone https://github.com/henry2man/ark && cd ark && go build -o bin/ark ./cmd/ark
```

Requirements: Go ≥ 1.23 to build. Runtime needs: `rsync` and `ssh` (only for remote
vaults), and one download engine in `PATH` (`hf` from `pip install huggingface_hub[cli]`).

## First run

On first use `ark` writes `~/.ark/config.json` with a default setup:

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

### Synology setup (one-time)

1. DSM → Control Panel → **Terminal & SNMP** → enable SSH.
2. `ssh-copy-id user@nas` (ark uses BatchMode ssh; no interactive passwords).
3. First `demote`/`download` creates `/volume1/ark/models` automatically.

SMB/CIFS is *not* required (rsync-over-ssh is faster and preserves HF hardlinks).
You can still point a `local`-kind vault at a mounted CIFS path if you prefer —
`rsync` handles it, just without hardlink dedup.

## Commands

| Command | What it does |
|---|---|
| `ark list` | Table of every model across **all** vaults (size, revision, date) |
| `ark download <repo> [--to V] [--rev R] [--engine E] [--hashes] [--and-remote V]` | Fetch from HF into staging → vault; `--and-remote` ships it in one shot |
| `ark promote <repo> [--from V] [--local V]` | Vault → local tier (the "load model" operation) |
| `ark demote <repo> [--to V]` | Local tier → vault (the "unload model" operation) |
| `ark link <repo> [--dir DIR]` | Symlink vault model → `~/ark/models/<org>/<name>` |
| `ark serve <repo>` | Link + print `HF_HUB_OFFLINE=1` env for vLLM |
| `ark verify [repo]` | sha256 check vs manifest (bit-rot detection) |
| `ark info <repo>` | Stored metadata as JSON |
| `ark vault ls \| add <name> <host\|local> <path> \| rm <name>` | Manage vaults |

### Example flow

```bash
ark download Qwen/Qwen3-8B --hashes --and-remote nas   # HF -> local -> NAS
ark demote  Qwen/Qwen3-8B                              # (if you forgot --and-remote)
ark list
ark serve Qwen/Qwen3-8B
export HF_HUB_OFFLINE=1
vllm serve $ARK_MODEL_PATH ...                         # fully offline
ark verify                                             # monthly cron: bit-rot sweep
```

## Design notes

- **Staging**: downloads land in `<vault-root>/../staging`, then hardlink-move into
  the vault — an interrupted download never pollutes a vault.
- **Transfers**: `rsync -a --inplace --partial` → resumable, checksum-safe, and HF's
  content-addressed cache never re-copies an unchanged blob.
- **Engines**: auto-detects `hf` → `hf_transfer` → `obscura`. Pluggable interface
  (`internal/engine`) if you want a torrent/mirror backend later.
- **Integrity**: `.arkmeta.json` (repo, revision, sha256s, size). `ark verify` exits
  non-zero on mismatch — wire it into cron/healthchecks.

## Honest limitations

- HF's *total* archive is petabytes; ARK is for a **curated** lifeboat, not a full mirror.
- `download` writes to a **local** vault first (HF engines can't write over ssh), then
  ships. Disk on the staging box must fit the largest single model.
- Remote `list` needs ssh reachability; degraded vaults warn rather than fail.

## License

MIT — see [LICENSE](LICENSE).
