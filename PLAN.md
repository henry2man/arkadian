# PLAN — memento

Where Arkadian stands and what is left. Read this first after a break. The
command is `ark`. The repo has two branches: `main` holds the intent (README
only), `first-implementation` holds the build. Work in the branch. Squash-merge
into `main` when the build matches the intent.

## The three actions this tool exists for

1. **Conserve.** Models must survive a registry that goes away. Every download
   stores a sha256 manifest, so a copy is checkable for years: `ark verify`.
2. **Manage space.** 1 TB of NVMe on the Spark, models of 60 GB. Know what is
   big and where: `ark list`. Free space: `ark rm`, `ark mv --to <cold>`.
3. **Make available here.** Put a model on the machine that serves it, in one
   command and with an explicit mode: `ark load <repo> --from <vault> --link`
   (no bytes) or `--copy` (bytes, room checked).

## API (11 verbs, no aliases)

```
ark list                                  sizes per model, per vault, free space, risk hints
ark download <repo> --to V [--rev R] [--source S]      manifest always stored
ark load     <repo> --from V --link|--copy [--force]   make it available here
ark mv       <repo> --from A --to B [--link] [--yes]   any pair, remote to remote too
ark rm       <repo> [--vault V] [--yes]                free space
ark path     <repo> [--vault V]                        the path vLLM needs
ark link     <repo> [--dir DIR]                        symlink for serving
ark unlink   <repo> [--dir DIR]                        undo that link
ark verify   [repo]                                    sha256 sweep, 0 clean / 1 rot
ark info     <repo]                                    metadata as JSON
ark vault    ls | add <location> | rm <name>           smb:// and nfs:// are refused
ark version                                            version, about, repo link
```

Rules that hold everywhere: long flags, `--yes` skips a question, the vault is
named and never guessed, bad usage exits 2, errors exit 1, `--help` and `-h`
work at every level, a removed verb prints its replacement.

## Done

- MVP works end to end: download, list, load, mv, rm, path, link, verify, info,
  vault. Tested with `hf-internal-testing/tiny-random-gpt2` (11.9 MB) across a
  local vault, a samba vault, and a remote vault over ssh, including remote to
  remote in one step and integrity after three hops (0 mismatches).
- API collapsed to eleven verbs. `promote`, `demote`, `ark model`, `serve`, and
  the aliases are gone; their names print the new command.
- Vault locations: path, `host:/path`, `ssh://user@host/path`, in `vault add`
  and in `--from` / `--to`.
- `ark load` with explicit modes and the room guard: a `--copy` that would leave
  a vault past 90% used stops (`maxUse`), `--force` overrides.
- Hashes by default, no flag to skip them.
- Bugs fixed: `rsyncCopy` made junk dirs named `user@host:`; the first run wrote
  into a folder named `~`; staging landed in the current folder for a vault with
  no parent path; `ark -h` read as an unknown command.
- Docs: README (model, commands, conventions, flow), AGENTS (rules 1-9),
  PLAN.md (this file), TODO.md. CI checks gofmt, vet, test, build.
- Privacy: no hosts, no IPs, no vendor names in code or docs. History rewritten
  and force-pushed; `711ff85` still lives in GitHub's cache until its GC.

## What is left

1. **Tests, then everything else through tests.** The repo has zero test files and
   `go test ./...` is empty. Switch to TDD: RED, code, GREEN, per change. First
   candidates: `parseFlags`, `Slug`, `DfSpaces`, the room guard, the `RemoveModel`
   guard, `parseLocation`, and the `load` mode rule.
2. **Run it on the real hardware.** The Spark NVMe plus the NAS vault. Sizes,
   `load --link` on a mounted CIFS share (expect EOPNOTSUPP: the hint must be
   right), and a 60 GB `load --copy` to time the 90% guard and the hashing.
3. **Decide the remote shape.** The branch `first-implementation` is not pushed.
   Push it, and move `main` to the intent commit, when you want GitHub to show it.
4. **Optional cleanups.** `/volume1` as an example path (a vendor fingerprint);
   `unload` if `mv --to` aches in daily use; `ark import` from a directory;
   `verify` on remote vaults (one ssh call per model); one `ark doctor` that
   checks sources and hosts; a bilingual one-page site.
5. **Releasing, only when the name check passes.** The pipeline is idle:
   `ci.yml` runs, `release.yml` waits for a manual run, `.goreleaser.yaml` has the
   Homebrew block commented out. The version comes from a git tag, not from code.

## Ceilings kept on purpose

- `mv` has no copy-only mode: use `load --link` to keep the bytes in place, or
  `mv` then nothing. Marked with `// ponytail:`.
- `verify` does not scan remote vaults. Run it where the bytes are.
- `path` returns one path. A model in two vaults needs `--vault`.
- `maxUse` is 90% in code. Raise it with a reason, not with a config key.
