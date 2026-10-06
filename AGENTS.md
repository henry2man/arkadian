# AGENTS.md

Instructions for AI agents that work in this repository.
Write new documentation in English. Use ASD-STE100 Simplified Technical English:
short sentences, active voice, one instruction per sentence.

## The Project

The project is Arkadian. The command is `ark`. Keep it that way: do not rename the
binary, `ARK_CONFIG`, `~/.arkadian/config.json`, or `.arkmeta.json`.

`ark` is a single-binary CLI in Go. It keeps Hugging Face models in local and
remote vaults. See README.md for the full design and TODO.md for open ideas.

Install Go 1.27.1 or higher from https://go.dev/dl. Nothing else: `go.mod` pins
the version and `GOTOOLCHAIN=auto` handles the rest.

## Commands

```bash
go build -o bin/ark ./cmd/ark   # build
./bin/ark version               # run it
gofmt -l .                      # format check; the output must be empty
go vet ./...                    # static check
go test ./...                   # there are no test files yet
```

CI runs these checks on every push (`.github/workflows/ci.yml`).

## Structure

| Path | Content |
|---|---|
| `cmd/ark/main.go` | argument parsing and all subcommands |
| `internal/config` | `~/.arkadian/config.json` load/save and defaults (`ARK_CONFIG` overrides the path) |
| `internal/source` | download sources: `hf`, `hf_transfer`, `obscura`, `modelscope` |
| `internal/store` | vault paths, slugs, `.arkmeta.json` metadata, size, sha256, copy |

Add new subcommands in `cmd/ark/main.go`. Put reusable logic in `internal/`.

## Rules

1. Do not add external Go modules. The binary stays dependency-free.
2. Do not change the on-disk layout. A vault directory must stay valid as a
   Hugging Face `--local-dir` path.
3. Run external tools with `run()` (local) and `sshRun()` (remote). `ssh` uses
   BatchMode: never ask for a password.
4. Download into staging first, then move the files into the vault.
5. Report errors with `die()` and `errHint()`. Do not panic.
6. Do not commit `bin/`, `*.test`, or `vendor/`.
7. Write the least code that works. This project uses Ponytail: see the ladder in
   README.md. Do not add an abstraction nobody asked for. Mark a deliberate
   shortcut with a `// ponytail:` comment that names the ceiling and the fix.
8. The command set is the API: `list`, `download`, `mv`, `rm`, `path`, `link`,
   `unlink`, `verify`, `info`, `vault`, `version`. No aliases, no second verb for
   one job, no flags that repeat another command. A new verb needs a job these
   cannot do. PLAN.md holds the reasoning.

## Releases

Nothing is published yet. The repo is private and the release pipeline is idle.
`ci.yml` still checks every push. See the README for the steps to switch
publishing on. TODO.md lists the open ideas.

Do not add version numbers in the code. `main.version` comes from a git tag.
Do not add a release note file: GoReleaser builds the change log from commits.

## Before You Finish

1. Run `gofmt -w .`.
2. Run `go vet ./...` and `go build -o bin/ark ./cmd/ark`.
3. Run the command that you changed against a local vault.
4. Report the exact commands and their real output. If you did not run them,
   say so.
