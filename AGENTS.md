# AGENTS.md

Write documentation in English. Use ASD-STE100 Simplified Technical English.
Keep sentences short and active.

## Project

Arkadian is a small Go CLI for model inventory and safe storage operations.
The command is `ark`. Keep `ARK_CONFIG`, `~/.arkadian/config.json`, and
`.arkmeta.json`. PLAN.md is the only task list. README.md holds future ideas.

Work on first-implementation. The current goal stops before the website.
Website follows validation. Homebrew release is the final V1 phase.

## Tools and checks

Use Go 1.27.1 or later. Install with Homebrew when needed. Runtime tests require
HF CLI (tested with 1.5.0), Python 3.9+, rsync, and ssh. No external Go modules.

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go build -o bin/ark ./cmd/ark
./bin/ark version
```

Use RED/GREEN for behavior changes. Leave a small runnable test for nontrivial
logic. Before finishing, run format, vet, tests, build, and a real local-cache
command. Report actual checks and skipped hardware checks separately.

## Structure

| Path | Responsibility |
|---|---|
| cmd/ark/main.go | Arguments, command coordination, display, per-config lock |
| internal/config | Config, HF cache detection, vault location parsing |
| internal/source | HF source URI and download commands |
| internal/store | Native caches, inventory, manifests, transfers, space |

## Rules

1. Write the least code that works. Reuse existing functions and the platform.
2. Delegate download/cache commands to HF. Delegate transport to rsync and SSH.
3. Keep native HF layout. Never silently migrate an old Arkadian directory.
4. Every vault has an explicit huggingface type. Reject unsupported providers.
5. Run external tools through store.Run and store.Command. Quote remote argv.
   SSH uses BatchMode. Do not ask for passwords or configure the operating system.
6. Publish transfers only after verification. Recheck source before deletion.
7. Do not count references as independent copies. Never delete a link target.
8. Preserve the API in PLAN.md. No model aliases, duplicate verbs, or transfer
   flags that repeat positional arguments. Retired commands explain replacements.
9. Keep the 90% space limit in code. Force does not bypass integrity or conflicts.
10. Fail with an actionable error. Do not panic. Invalid usage exits 2.
11. Mark a deliberate real shortcut with a ponytail comment naming its ceiling
    and upgrade path. Do not add abstractions for hypothetical future providers.
12. Do not commit bin/, dist/, test binaries, or vendor/. Do not add license headers.

## Release

Do not publish, commit, merge, or change repository visibility as part of CLI
implementation. Keep releases idle until the planned validation gates pass.
Versions come from tags. GoReleaser generates change logs from commits.
