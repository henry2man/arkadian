# Arkadian V1 — Model Inventory and Safe Storage Operations

## Purpose

**Running out of space for your models?**

> Know which models you have, exactly which artifact each one is, where verified
> copies exist, and move them safely between your storage tiers.

Arkadian owns the inventory and the safety rules between copies. Existing tools
provide downloads, cache management, transport, and mounts. Keep this a small
weekend project. Work on `first-implementation` until the build is validated.

This file replaces TODO.md. Mark tasks complete only after their checks pass.
The current implementation is being restructured. The API below is the target.
INVESTIGATION.md is external research, not an implementation checklist.

## Responsibilities and Scope

| Component | Responsibility |
|---|---|
| Arkadian | Inventory, artifact identity, conflicts, copy verification, safe deletion |
| `hf` | Downloads, authentication, cache discovery, cache deletion |
| `rsync` and SSH | File transport and remote execution |
| Operating system | Mounts, permissions, disks, network access |
| `ln` | User-created symbolic links |
| Model runtime | Serving models from their paths |

- Support HF only. Remove ModelScope and alternative download engines from V1.
- Require named vaults with `type: huggingface` and a location.
- Use native HF cache layout in each vault. Keep models usable without Arkadian.
- Discover the existing HF cache as `hfcache`, the default working vault.
- Use complete repository identifiers. No model aliases in V1.
- Add no daemon, database, telemetry, or external Go modules.
- Report missing requirements with an actionable error. Do not install tools,
  configure SSH, mount storage, or manage inference.

## API

`<model>` is an exact repository identifier, such as `Qwen/Qwen3-8B`.
HF also supports some one-component identifiers. These are not aliases.

| Command | Behavior |
|---|---|
| `ark vault add <name> <location>` | Register a vault; infer the HF type in V1 |
| `ark vault ls` | Show name, type, and location |
| `ark vault rm <name>` | Remove configuration; retain files |
| `ark list [model] [--vault V] [--json]` | Model/artifact matrix, locations, sizes, free space |
| `ark refresh [--vault V]` | Reconcile inventory with observed contents |
| `ark pull hf://org/model <destination> [--rev R]` | Run HF download at the destination |
| `ark cp <model> <source> <destination>` | Create and verify an independent copy |
| `ark mv <model> <source> <destination>` | Copy, verify, then remove the source |
| `ark get <model>` | Find a copy and bring it into the default working vault |
| `ark evict <model> <destination>` | Keep a verified destination copy; free the working vault |
| `ark rm <model> <vault>` | Remove one copy with last-copy protection |
| `ark sync <source> <destination>` | Add source models; never delete or copy backward |
| `ark verify [model] [--vault V]` | Offline verification against manifests |
| `ark path <model> [--vault V] [--rev R]` | Print one snapshot path |
| `ark version` | Print version information |

- Accept paths, mounted paths, `host:/path`, and `ssh://host/path` as locations.
- Persist the vault type explicitly. Reject unknown types.
- Require positional destinations. No `--from` or `--to` transfer alternatives.
- Keep `--help`, exit 0 for success, 1 for errors, and 2 for invalid usage.
- Keep diagnostics on stderr. Keep `path` and `list --json` stdout composable.
- Confirm destructive operations. `--yes` skips questions, never safety checks.
- Do not add `download`, `load`, `link`, `unlink`, `info`, `where`, or
  `mv --link`. Unknown names point to the command to use. Use list filters
  instead of adding `where`.

## Data and Operation Contracts

### Configuration and discovery

Keep `~/.arkadian/config.json`. Store `models.json` beside it. `ARK_CONFIG` sets
the config path and thus the inventory directory. Config stores the default
vault and a map of vaults with type, path, and optional SSH host.

- Respect HF cache environment-variable precedence and default paths.
- On the first list, discover existing cache contents without copying or hashing
  weights. Later lists read the inventory. Refresh collects external changes.
- Do not silently convert old Arkadian directories to native caches.
- Reading saved inventory does not require every vault or tool to be available.

### Inventory and manifests

Key the inventory by repository. Record artifacts by immutable HF commit and
their vault locations. Include size, reference/copy, last observation, and last
verification. Distinguish present, verified, corrupt, missing, and unknown.

- Keep `.arkmeta.json` at the cached repository root, outside snapshots.
  Store checksums separately for each revision. Exclude the manifest from itself.
- User-created references are not independent copies.
- A cached snapshot can be partial. Do not claim model completeness from presence.
- Do not add a new revision over an existing model in V1. Updating is future work.
- Discover all inherited revisions. Copy/move operations preserve their set.
- Write metadata atomically. Protect concurrent inventory updates.
- If metadata saving fails after a transfer, retain data and suggest refresh.

### Refresh and list

Use `hf cache ls --revisions --format json --cache-dir ...`, locally or over SSH.
Refresh transfers no weights and does not perform full hash verification.
Keep unreachable vault entries as unknown. Mark missing only after a complete,
successful scan. Invalidate verification when observed content changes.
Successful Arkadian operations also update the affected inventory entries.

List uses rows for model/revision, size, and columns for vaults. Include a state
legend and disk space when available. Keep risk hints for no other copy and for
models absent from the working vault. Do not count references as replicas.

### Pull

Translate the source URI to `hf download <repo> --cache-dir <vault-path>`.
Execute on the destination host for SSH vaults. Never stage remote downloads
on the local disk. Use HF authentication, temporary files, and resumption.
Require HF at the execution host. Do not silently update existing models.
Write the manifest and inventory after a successful download.

### Transfers and deletion

Share one verified transfer between cp, mv, get, evict, and sync.

- Copy the repository with its blobs, snapshots, and refs. Preserve HF internal
  links. Do not hardlink weights between independent vault copies.
- Stage at the destination and resume failed transfers. Exclude download locks
  and active temporary files. Publish only after verification.
- Build or validate the source manifest. Verify destination bytes against it.
- Reject conflicting revisions or contents without overwriting them.
- Recheck the source before deleting. Retain it if it changed during transfer.
- Reject physically identical source and destination locations.
- Keep the 90% space guard in code. `--force` overrides space only, not integrity
  or conflicts. Check remote space on the remote host.
- For SSH-to-SSH, run rsync at the source. Report missing access between hosts.
- Delegate real cache deletion to `hf cache rm`. Remove a repository symlink
  itself without passing it to a deletion path that could follow its target.

Cp retains the source. Mv removes it only after destination verification.
Get prefers accessible local/mounted locations, then SSH, in stable name order.
When candidate artifacts differ, suggest cp with an explicit source.
Evict requires a destination. Create or reuse its copy, verify, then free the
working vault. Removing a reference removes only the reference.
Rm requires `--force` to delete the last known independent copy. A disconnected
or unknown copy does not satisfy this safety check.

### Sync and verify

`ark sync usb master` considers only USB models. Copy missing models, resume
partials, skip identical artifacts, and keep master-only history. Report
conflicts. Continue independent models and exit nonzero if any failed.

Verify compares local or remote files with stored manifests, without the Hub.
A first manifest establishes current bytes; it does not prove their original
Hub authenticity. Transfer verification proves destination byte identity.

Path selects an explicit revision, then a cached main ref, then a sole revision.
Otherwise require `--rev`. A remote path is an SSH location, not a local mount.

## Memento — 2026-10-07

Built: the command set in the API table, HF cache discovery, native cache layout,
inventory with per-revision manifests, offline `verify`, additive `sync`, resumable
verified transfers, last-copy protection, and the 90 percent space guard. Tests use
the standard library only. No external Go modules.

Checked: `scripts/test.sh` passes in CI on ubuntu-latest (Go 1.27.1, GNU rsync 3.2.7,
HF CLI 1.5.0) in run 37604786566. The same script passes on macOS with GNU rsync 3.5.1
from `brew install rsync`. ark rejects openrsync, the macOS default, and names that
command in the error. Transfers pass `rsync -s`, so paths with spaces need no manual
quoting. Install `click` by hand with pip: the `cli` extra does not exist in
huggingface_hub 1.5.0, and typer 0.27 dropped click. Homebrew also ships an `hf`
formula, version 2.1.1. With HF CLI 2.x, Xet files live in the cache-wide shared blob
store and the repository holds a link. Arkadian hashes that content, transfers real
bytes with `--copy-unsafe-links`, and leaves the destination vault self-contained.
Deletion stays with `hf cache rm`, so shared bytes survive. No legacy mode flag is
set.

Kept simple on purpose: one CI job, no container, no macOS runner, and one lock per
config file. Add a platform job when a real platform breaks.

Remaining: hardware validation on Spark, USB, and NAS with one large model and a live
runtime (last Phase 4 item). Then the website (Phase 5) and the release with Homebrew
(Phase 6). Do not publish before that Phase 4 gate passes. The repository is
`arkadian`; the command is `ark`.

## Ordered Work

### Phase 1 — Contract and documentation

- [x] Rewrite this plan in English and reconcile the old TODO.
- [x] Move deferred features to README Future ideas and remove TODO.md.
- [x] Align AGENTS and README with the implemented API and dependencies.
- [x] Retain the fixed names: ark, ARK_CONFIG, ~/.arkadian/config.json,
      .arkmeta.json. Replace obsolete command and naming discussions.

### Phase 2 — HF and inventory

- [x] Simplify sources to HF CLI. Check required capabilities with clear errors.
- [x] Add explicit types, HF cache discovery, and config validation.
- [x] Add atomic inventory, refresh, matrix/filter/JSON output, and space checks.

### Phase 3 — Safe operations

- [x] Add shared resumable transfer and offline local/remote verification.
- [x] Add positional cp, mv, rm, sync, get, and evict with safety guards.
- [x] Add local/remote pull, path selection, and unknown-command hints.

### Phase 4 — Validate the restructuring

- [x] Add focused standard-library tests. Use RED/GREEN for behavior changes.
- [x] Test arguments, filters, cache discovery, inherited revisions, partials,
      inventory rebuilding, concurrent writes, and disconnected vaults.
- [x] Test independent copies, resumption, corruption, source changes, links,
      last-copy protection, additive sync, and remote failures.
- [x] Run gofmt, go vet, go test, build, and a temporary-vault end-to-end flow.
- [x] Make CI check first-implementation and main. Cross-build supported targets.
- [x] Record real checks and simulated checks separately.
- [x] Run scripts/test.sh in CI on ubuntu-latest. On macOS install GNU rsync
      with `brew install rsync`; ark rejects openrsync.
- [ ] Validate Spark/USB/NAS when available. Exercise a large model and an
      inference runtime. These hardware checks were not available in this run.

### Validation record — 2026-10-07

Local tools: Homebrew Go 1.27.1, HF CLI 1.5.0, Python 3, rsync, and SSH.
The following commands passed:

```text
gofmt -w .                     (no output)
gofmt -l .                     (no output)
go vet ./...                   (no output)
go test ./... -count=1
ok  github.com/henry2man/arkadian/cmd/ark          28.167s
ok  github.com/henry2man/arkadian/internal/config  0.330s
ok  github.com/henry2man/arkadian/internal/source  0.315s
ok  github.com/henry2man/arkadian/internal/store  16.467s
go build -o bin/ark ./cmd/ark   (no output)
bin/ark version
ark dev
Arkadian — model inventory and safe storage operations
https://github.com/henry2man/arkadian
git diff --check               (no output)
```

Cross-builds passed for darwin/arm64, darwin/amd64, linux/arm64, and linux/amd64.
Each used `GOOS=<os> GOARCH=<arch> go build -o /tmp/ark-<os>-<arch> ./cmd/ark`.
Linux builds required permission to read the Go toolchain outside the sandbox.
The Linux binaries were not executed on a Linux host. CI is configured but was
not run on GitHub in this session.

A real Hub download fetched 10 files for
`hf-internal-testing/tiny-random-gpt2` into `/tmp/ark-v1-live.Fc9Nhz/cache`.
The cached commit is `71034c5d8bde858ff824298bdedc65515b97d2b9`.
The model occupies 11.9 MiB. These commands then passed with `HF_HUB_OFFLINE=1`:

```bash
export ARK_CONFIG=/tmp/ark-v1-live.Fc9Nhz/config.json
export HF_HUB_OFFLINE=1
bin/ark list
bin/ark vault add archive /tmp/ark-v1-live.Fc9Nhz/archive
bin/ark cp hf-internal-testing/tiny-random-gpt2 hfcache archive
bin/ark evict hf-internal-testing/tiny-random-gpt2 archive --yes
bin/ark get hf-internal-testing/tiny-random-gpt2
bin/ark sync hfcache archive
bin/ark verify
bin/ark path hf-internal-testing/tiny-random-gpt2
bin/ark refresh
```

Verify reported verified copies in archive and hfcache. Path printed the cached
snapshot path. Tests used real HF cache commands and rsync on small fixtures.
SSH tests replaced only the SSH connection with a controlled local command.
They checked remote transfers, remote pull, physical aliases, quoted paths,
and connection failure. Tests also checked interrupted transfers, changed
sources, corrupt copies, links, last-copy protection, inventory recovery,
configuration locking, and the 90% space guard.

No Spark, external USB disk, or NAS was used. No large model or inference runtime
was exercised. Website, release, and Homebrew publication have not started.

### Phase 5 — Initial website, after restructuring validation

- [ ] Build a single-page site with plain HTML and a small CSS file.
- [ ] Use a minimal retro-futuristic style: dark background, one accent,
      monospace headings, light grid detail, and hover-only motion.
- [ ] Keep page assets under 60 KB. No framework, build step, or trackers.
- [ ] Include the space hook and core tagline, install instructions, GitHub and
      source links, offline use, storage tiers, custody, and the command table.
- [ ] Include a short author note with profile link and a muted license footer.
      Mention AGENTS.md; mention a skill only if one actually ships.
- [ ] Check small and large screens, keyboard access, links, and page weight.
- [ ] Publish the initial website after its checks pass.

### Phase 6 — Release and Homebrew, last

- [ ] Recheck Arkadian and ark names on GitHub and Homebrew before publication.
- [ ] Validate GoReleaser snapshots and the linux/darwin amd64/arm64 binaries.
- [ ] Confirm website/docs/install instructions match the release candidate.
- [ ] Prepare repository visibility, the tap henry2man/homebrew-arkadian,
      credentials, and release workflow permissions.
- [ ] Enable the existing release/Homebrew configuration when ready.
- [ ] Publish the GitHub release and Homebrew package. Keep the package name
      arkadian and executable ark. Confirm the generated package path.
- [ ] Test a clean Homebrew install and ark version. Versions come from tags.
- [ ] Clean up remaining stale decisions and documentation. Keep this last.

## Tracking and Execution

The active implementation goal stops before Phase 5. Website and Homebrew are
part of V1, but start only after the preceding validation gates.
Do not commit, merge, change repository visibility, or publish as a side effect
of CLI implementation. Keep release notes generated from commits.

Future work lives in README.md under Future ideas. This plan is the only task
list. Alias verbs, serving links, local staging for remote downloads,
alternative providers, and optional transfer flags are out of V1.
