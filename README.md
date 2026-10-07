# Arkadian

**Tired of cleaning your disk?** Arkadian keeps AI models in **vaults** and moves
them between vaults. One binary: the command is `ark`.

## The problem

- One 32B model is about 60 GB. The NVMe in a DGX Spark (GB10) is 1 TB. A week of
  downloads fills it.
- Cold models stay on the fast disk, because moving them by hand is a chore and a
  half-finished `rsync` is worse.
- If a registry disappears, the only copies that count are the ones on your disks.

## What we want

- One command that knows where every model is, with sizes and free space: `ark list`.
- One command that moves a model: `ark mv <repo> --from A --to B`. Any pair of vaults:
  a local disk, a mounted share (SMB/NFS), or another machine over ssh.
- No format lock-in. A vault directory is a Hugging Face `--local-dir`, so vLLM and
  transformers read it as it is.
- One static binary, no dependencies. `rsync` and `ssh` do the moving.
- A checksum manifest per model, so NAS bit-rot shows up in a cron job: `ark verify`.
- Nothing in the background. No daemon, no database, no telemetry.

## Status

The code lives in the `first-implementation` branch. It already downloads, lists,
moves, deletes, and verifies. Nothing is published yet. When the build matches this
page, we squash-merge the branch into `main`.

`main` holds the intent. The branch holds the build. That split is the whole process.

## Names that do not change

`ark` (the command), `ARK_CONFIG`, `~/.arkadian/config.json`, `.arkmeta.json`.
