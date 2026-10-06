# TODO

Wishlist and open tasks. One line per item. Add new ideas at the end.

## Why this exists

The NVMe on the DGX Spark is 1 TB. It fills up fast. Models must move to the NAS
and come back on demand. One swap must take one command, not five.

## Interface

- [x] `ark list` shows size per model, size per vault, and free disk space, plus a
      footer that flags the two risky states: no cold copy, and cold only. Site
      tagline for this: "Tired of cleaning your disk?"
- [x] `--help` at every level: `ark --help`, `ark <command> --help`,
      `ark vault --help`, `ark model mv --help`. Errors point at the next command.
- [x] Show the source of each model in `ark list` (SOURCE column).
- [x] Add `ark model` as a command family: `ls`, `download`, `path`, `mv`, `rm`.
      `mv` and `rm` ask for confirmation; `--yes` skips it for scripts.
- [x] Require `--from` and `--to` where a move needs them. No guessing. The error
      says `Run: ark vault ls` and lists the vaults.
- [x] `ark promote <repo> --from V --link`: virtual promote. The local vault gets a
      symlink, so the model is there and costs no space. `ark list` marks those rows
      `link` and says the bytes stay in the other vault.
- [x] `promote` and `demote` copy, never delete, with real copies (no hardlinks).
      `ark model mv` is the command that moves.
- [ ] Add a TUI. **v2, not v1.** The CLI already answers the 90% case: `ark list`
      shows the sizes, `ark promote`/`ark demote` do the swap.
- [ ] Keep every CLI command when the TUI exists. The TUI calls the same code.
      The CLI stays the interface for scripts and agents.

## Releasing

- [ ] Publish GitHub Releases. The pipeline is ready and idle now. Run
  `.github/workflows/release.yml` by hand, or enable the tag trigger.
- [ ] Publish on Homebrew. Make the repo public first, create the tap repo
  `henry2man/homebrew-arkadian`, add the `HOMEBREW_TAP_GITHUB_TOKEN` secret, and
  uncomment the Homebrew block in `.goreleaser.yaml`. The cask keeps the short
  name `arkadian`: `brew tap henry2man/arkadian && brew install --cask arkadian`.
  The command it installs stays `ark`. Rejected: `ark-model-manager`, too long to
  type. Check that goreleaser writes the file as `Casks/cask_arkadian.rb`, the name
  Homebrew wants now.

## Sources and destinations

The design has two seams. A source brings models in. A destination keeps them.
The source seam is `internal/source`. The destination seam is `store.Vault`:
`local` (a directory), `samba` (a mounted path), or `remote` (`user@host`, ssh).
Each model stores the source that fetched it in `.arkmeta.json` (`source` key).
A vault has no source type.

- [x] Rename `engine` to `source`. The flag is `--source`. No alias: nothing was
      released, so no old command line to keep alive.
- [x] Source: Hugging Face Hub (`hf`, `hf_transfer`, `obscura`).
- [x] Source: ModelScope. `pip install modelscope`, then `ark download <repo>
      --source modelscope`. ModelScope defaults to the `master` revision, HF to
      `main`, so pass `--rev` when it matters.
- [x] Config lives in `~/.arkadian/config.json`. `ARK_CONFIG` overrides the path.
      No migration: no released binary reads the old file.
- [ ] Source: plain import from a directory or a file (`ark import ./weights`).
- [ ] Source: OCI registry / `ollama pull` style registries. Later.
- [ ] Destination: local directory (done).
- [ ] Destination: rsync over ssh (done).
- [x] Destination: Samba/CIFS mount. `ark vault add <n> samba <mounted-path>`. It is
      a local path in the code, so `ark list` labels it `samba`. Hardlink dedup does
      not work on CIFS; the vault just holds files.
- [ ] Vault to vault without the local tier. `ark model mv --from A --to B` works
      when at least one end is a path on this machine (local or samba). Remote to
      remote still needs two steps: promote, then demote. Review a direct copy with
      rsync through an ssh ProxyJump, or run `ark` on a host that sees both. Check
      three things first: who pays the bandwidth, what happens on a broken pipe, and
      whether hardlinks survive. Keep `promote`/`demote` as the simple path.
- [ ] Destination: S3 compatible store (MinIO, Cloudflare R2, Wasabi). **v2.**
      Needs multipart upload and its own verify path. Out of v1.
- [ ] One `ark doctor` command: print which sources are installed and which
      destinations answer.

## Names

The project started as a lifeboat for Hugging Face models. These names keep that
idea. Keep the binary short: type it every day.

| Name | Why | Risk |
|---|---|---|
| `ark` (now) | The vessel. Short, clear, easy to type. | Many tools use "ark" |
| Arkadian | Nice display name for the docs and README. `ark` stays the command. | Reads like "Arcadian", a fintech/DevOps brand |
| `ferry` | Moves cargo between two shores: NVMe and NAS. | Taken: Fermilab Ferry-CLI, ferryproxy/ferry |
| ~~`portage`~~ | Carry the boat over land. Fits tier hops exactly. | Taken: Gentoo Portage, the package manager |
| `tug` | `tug pull`, `tug push` match promote and demote. | Name clashes in Docker land |
| `silo` | Grain silo: one bin per model, tiers of bins. | "Data silo" is negative |
| `larder` | The cold room where food waits. Warm and British. | Soft sound |
| `keep` | The strong room of a castle: custody, exactly. | `make` uses "keep" |
| `mammoth` | Freeze it, thaw it later. `thaw` = promote. | Mammoth is used by other tools |
| `hoard` | Honest: we hoard weights. `hoard list` is fun. | Sounds greedy |
| `relic` | Custody of valued objects, with a checksum shrine. | Religious tone |
| `noah` | The guy with the manifest. `noah verify` writes itself. | Person-name CLI |
| `mabbul` | Hebrew for "the flood": the model-zoo flood we survive. | Hard to spell |
| `caravel` | Small ship, long trips, small crew. | Pretty but vague |
| `quay` | Where the models dock before vLLM loads them. | `key` homophone |

Name check on 2026-10-07 (local SearXNG, web search):

- `arkadian` and `Arkadian`: no CLI or dev tool with that name. Existing users are
  not in tech tooling: a UK consultancy, a cybersecurity firm, one music artist,
  one personal site, and a new "Arkadian" network account on X. PyPI name is free.
  Note the neighbour `Arcadian` (with a c) is crowded: game engines, fintech, and
  science tools. Expect some typo traffic.
- `portage`: do not use. Gentoo Portage is the package manager behind `emerge`.
  `portage-cli` and `portage-ng` also exist. Linux people will search for Portage,
  not for your model mover.
- `ferry`: do not use. Fermilab ships a `Ferry-CLI` and `ferryproxy/ferry` moves
  traffic between Kubernetes clusters.
- `silo`: crowded. SILO Labs (agent sandbox) and silo.ai.
- `ark` itself: `ark` exists on PyPI (unrelated) and "Ark" is the Volcengine model
  API from ByteDance. Low risk for a Go binary, but expect company matches.

Decision (2026-10-07): the project is **Arkadian**. The command stays `ark`.
The module path and the repo name become `arkadian`. Config and data keep the
short name: `ARK_CONFIG`, `~/.ark`, `.arkmeta.json`, and the binary `ark`.

- [x] Pick the display name: Arkadian.
- [ ] Before you publish, check the name once more on GitHub and Homebrew.

## Site

- [ ] Add a GitHub Pages site. Keep it one page. Style: minimal and
      retro-futuristic, close to pi.dev. No framework, no build step, no tracker.
      Plain HTML plus one small CSS file.
- [ ] Top block: what Arkadian is in one line, and the install command. Hook line:
      "Tired of cleaning your disk?" Sizes and free space are the reason this tool
      exists, so lead with them.
- [ ] Call to action: install, GitHub, and the source download. Put the license in
      muted text in the footer.
- [ ] Add a short note for agents: the repo ships AGENTS.md and a skill file.
- [ ] Why block: three use cases. Offline vLLM serving, the 1 TB NVMe that fills
      up, and long-term custody of a curated model set.
- [ ] Minimal docs: the command table from the README, nothing more.
- [ ] About me: two sentences and a link to your profile.
- [ ] Look and feel: dark background, one accent colour, monospace headings, a
      grid or scanline touch. No animation except a hover. Page weight under 60 KB.

## Ideas

- Put your ideas here.
