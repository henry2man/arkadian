# Arkadian — `ark`

**Running out of space for your models?**

> Know which models you have, exactly which artifact each one is, where verified
> copies exist, and move them safely between your storage tiers.

Arkadian is a small Go CLI for Hugging Face models. It finds your existing HF
cache, tracks copies across named vaults, and verifies a destination before
freeing the source. It keeps models in native HF caches. No daemon, database,
telemetry, or model format conversion.

The implementation and validation tasks are in [PLAN.md](PLAN.md). The initial
website follows restructuring validation. GitHub release and Homebrew are the
final V1 steps. Nothing is published yet.

## Install and requirements

Build from source with Go 1.27.1 or later:

```bash
brew install go
go build -o bin/ark ./cmd/ark
./bin/ark version
```

Runtime tools:

- `hf` with `cache ls --revisions --format json`, `cache rm`, and download dry-run
  support. The implementation is tested with HF CLI 1.5.0.
- Python 3.9 or later for the small filesystem and checksum helper. It uses only
  the Python standard library.
- `rsync` for transfers. `ssh` for remote vaults, with keys and BatchMode access.

Install and authenticate HF where downloads run. Install Python on each host
whose cache Arkadian inspects. No Go installation is needed to run the binary.

Homebrew publication is planned, not available yet. The package name will be
`arkadian`; the command stays `ark`.

## Use existing models immediately

```bash
ark list
ark vault add nas user@nas:/data/models
ark evict Qwen/Qwen3-8B nas --yes
ark get Qwen/Qwen3-8B
vllm serve "$(ark path Qwen/Qwen3-8B)"
```

On first use, Arkadian registers `hfcache` at the HF cache location. It respects
`HF_HUB_CACHE`, the legacy `HUGGINGFACE_HUB_CACHE`, `HF_HOME`, and `XDG_CACHE_HOME`.
The fallback is `~/.cache/huggingface/hub`.

The first list discovers existing models. It does not move, duplicate, or hash
all weights. Later lists read the saved inventory. Use `ark refresh` after
external tools change a cache or after reconnecting a disk.

## Vaults and responsibilities

A vault has a name, `type: huggingface`, and a cache root. Its location is a local
path, a mounted path, `user@host:/path`, or `ssh://user@host/path`.

```bash
ark vault add usb /mnt/usb/hf-cache
ark vault add nas /mnt/nas/hf-cache
ark vault add archive ssh://user@archive/data/hf-cache
ark vault ls
```

Mount SMB/NFS or external disks with the operating system. Configure SSH keys,
ports, and aliases outside Arkadian. An `smb://` or `nfs://` URL is not a mount.
Arkadian reports missing tools and connections; it does not configure them.

HF downloads and manages caches. Rsync transports bytes. Arkadian supplies the
inventory, artifact conflict checks, and verification before deletion.
User-created links remain the job of `ln`. A remote SSH path is not a local path
that a runtime can open. Mount it or copy the model with `get`.

## Commands

Use exact HF repository IDs. Short names are not aliases. Some HF repositories
have a one-component identifier; use the identifier that HF itself stores.

| Command | Purpose |
|---|---|
| `ark list [model] [--vault V] [--json]` | What models exist, which artifacts, and where |
| `ark refresh [--vault V]` | Reconcile inventory without copying weights |
| `ark pull hf://org/model <destination> [--rev R] [--force]` | Run HF download at the named destination |
| `ark cp <model> <source> <destination> [--force]` | Create and verify an independent copy |
| `ark mv <model> <source> <destination> [--yes] [--force]` | Copy, verify, then delete source |
| `ark get <model> [--force]` | Find an available copy and bring it to the working cache |
| `ark evict <model> <destination> [--yes] [--force]` | Keep a verified destination copy and free the working cache |
| `ark rm <model> <vault> [--yes] [--force]` | Remove one copy, with last-copy protection |
| `ark sync <source> <destination> [--force]` | Add source models; preserve destination history |
| `ark verify [model] [--vault V]` | Verify files against offline manifests |
| `ark path <model> [--vault V] [--rev R]` | Print a snapshot path for other tools |
| `ark vault add <name> <location>` | Register a vault with the HF type |
| `ark vault ls` | Show names, types, and locations |
| `ark vault rm <name> [--yes]` | Remove configuration; never delete weights |
| `ark version` | Show the build version |

Every command supports `--help`. Transfer endpoints are positional, in source
then destination order. No `--from` or `--to`. `get` and `evict` use the configured
default working vault, initially `hfcache`.

`--yes` skips confirmation only. `--force` can override the 90% space guard.
On `rm`, it also explicitly permits deleting the last copy. Neither flag disables
transfer verification or allows conflict overwrites.

Diagnostics go to stderr. `path` prints only its result to stdout. `list --json`
returns the filtered inventory. Exit codes: 0 success, 1 failure, 2 bad usage.

`download`, `load`, `link`, `unlink`, and `info` are retired. Their errors show
replacements. Use `get` for a real local copy, `ln` for links, and `list --json`
for structured model information.

## Inventory and integrity

```text
MODEL           REVISION      SIZE     hfcache  usb       nas
Qwen/Qwen3-8B   abc123...      15 GiB   present  -         verified
org/other       def456...      30 GiB   missing  unknown   verified
```

The display includes a state legend, vault free space, and copy-risk hints.
A reference is not an independent copy. Sizes per revision can share HF blobs;
do not sum revision sizes to estimate physical cache use.

- `config.json` stores vaults and the default working vault.
- `models.json` stores the last observed artifacts and locations, keyed by repo.
- `.arkmeta.json` at each cached repository root stores file hashes per revision.

Both user JSON files live in `~/.arkadian`. `ARK_CONFIG` changes the config path;
the inventory is stored beside it. Metadata is written atomically. One Arkadian
operation at a time uses each configuration.

Refresh keeps disconnected vaults as unknown. It marks copies missing only after
a complete successful scan. Verified means the last recorded checksum check;
refresh invalidates it when file metadata changes. Full verify checks the bytes.

A cached snapshot can be partial. Presence or a byte manifest does not prove
that all files needed by a runtime are available. A first manifest establishes
the bytes present, not their original Hub authenticity.

Transfers preserve native HF repository contents and internal links. Copies do
not hardlink weights across vaults. Failed transfers retain destination staging
under `.locks/ark-staging`, so a retry can resume. Publication follows checksum
verification. Moves recheck the source before cache deletion.

`verify` needs an existing manifest. Pull and copy establish one. Verification
then works offline, even if the source registry is unavailable.

## Archiving and synchronization

```bash
ark pull hf://Qwen/Qwen3-8B nas
ark sync usb nas
```

Pull into an SSH vault runs HF on that host. It does not store weights on this
machine first. That host must have HF, Python, permissions, and authentication.

Sync is additive and unidirectional. It adds USB models to NAS, retains NAS-only
history, and never returns that history to USB. Different artifact sets are
conflicts, not automatic updates. Failures return nonzero while independent
models can finish.

SSH-to-SSH transfers run rsync at the source host. That host must be able to
authenticate to the destination. There is no implicit local staging fallback.

V1 does not update models or add revisions to an existing model. It recognizes
and preserves multiple revisions that were already in a cache. If `get` finds
different artifacts, choose a source with `cp`. Path selects an explicit
revision, then a cached `main`, then the only revision; otherwise use `--rev`.

Old Arkadian `models/<slug>` vaults are not native HF caches. Register the real
HF cache; do not point the new backend at the old layout.

## Development and release

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go build -o bin/ark ./cmd/ark
./bin/ark version
```

Tests use small native caches and the installed HF/Python/rsync tools. Remote
tests use controlled SSH substitutes. They do not require a real NAS or network
downloads. The core uses the Go standard library. Use the smallest change that
works and retain checks for data safety.

Release remains idle until V1 validation and the initial website are ready.
GoReleaser builds linux/darwin amd64/arm64. Versions come from tags; change logs
come from commits. Test packaging without publishing:

```bash
goreleaser release --snapshot --clean --skip=publish
```

At the final release phase, confirm repository visibility, create the tap
`henry2man/homebrew-arkadian`, configure `HOMEBREW_TAP_GITHUB_TOKEN`, validate the
generated package, and enable the commented Homebrew block and release trigger.
Test a clean install before calling the release complete. PLAN.md tracks these
steps and the initial website.

## Future ideas

- A TUI that retains the CLI for scripts and agents.
- Model aliases, updates, and explicit revision management.
- ModelScope and other providers, driven by contributions.
- Import and discovery outside HF caches.
- OCI/Ollama sources and S3-compatible storage.
- Replica policies, jobs/resume, and garbage collection.
- Advanced hash caching and a diagnostic command.
- Additional website languages after the initial page.

## Thanks

- [Ponytail](https://github.com/DietrichGebert/ponytail): the simplest solution
  that works, with the platform and standard library first.
- [pi](https://github.com/earendil-works/pi): the agent used for the first build.

## License

MIT. See [LICENSE](LICENSE).
