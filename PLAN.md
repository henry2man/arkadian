# PLAN — minimum API for two jobs

Two jobs only:

1. Manage space. Know what is big, where it is, and what is at risk.
2. Move models between vaults.

Anything that does not serve one of those two is out. Less is more.

## Final API

```
ark list                                  sizes per model, per vault, free space, risk hints
ark download <repo> --to V [--rev R] [--source S] [--hashes]
ark mv     <repo> --from A --to B [--link] [--yes]     the one move verb
ark rm     <repo> [--vault V] [--yes]                  free space by deleting a model
ark path   <repo> [--vault V]                          the path to hand to vLLM/transformers
ark link   <repo> [--dir DIR]                          symlink for serving, prints the exports
ark unlink <repo> [--dir DIR]                          remove that link
ark verify [repo]                                      sha256 sweep
ark info   <repo>                                      metadata as JSON
ark vault  ls | add | rm                               manage vaults
ark version                                            version, about, repo link
```

No aliases. No `ark model` family. No `promote`/`demote`/`serve`.

## Steps

- [x] 0. Bug first: `rsyncCopy` runs `os.MkdirAll` on a remote URL, so a
      `user@host:/path` target builds junk directories under the current folder.
      Fix: create the directory only for a local path.
- [x] 1. Delete dead code: `store.Model.Status` (written, never read), the command
      aliases `dl`, `pull`, `down`, `push`, `models`, and the 4-argument
      `vault add <n> remote <host> <path>` form. `ls` stays: it is the shell
      convention, not an invented alias. Old names print the new command instead of
      failing with "unknown command": `ark promote`, `ark demote`, `ark cp`, `ark serve`.
- [x] 2. One move verb `ark mv`. It copies, then deletes the source. `--link` moves
      without bytes: the target holds a symlink and the source keeps the data.
      Delete `promote` and `demote`, which were directional copies of the same act.
      `--link` deletes nothing, so it does not ask; a real move asks unless `--yes`.
- [x] 3. Remote to remote in one step: `rsync hostA:path hostB:path` streams through
      this machine without local disk. Drop the two-step error.
- [x] 4. Model verbs at the top level: `ark path` and `ark rm`. Drop `ark model *`.
- [x] 5. Serving: keep `ark link`, add `ark unlink`. Drop `download --link`,
      `download --dir`, and `download --and-remote`: `ark mv` and `ark link` cover them.
      `ark link <repo> --dir DIR` keeps its flag; that is where the link goes.
- [x] 6. Docs: README command table and example flow, AGENTS structure, TODO notes.
- [x] 7. Checks: `gofmt -l`, `go vet`, build, then the real end-to-end run with
      `hf-internal-testing/tiny-random-gpt2` (11.9 MB) across a local and a samba vault.

## Ceilings kept on purpose

- `mv` has no copy-only mode. To keep both copies, `mv` then `mv` back is wrong; use
  `mv --link` to leave the bytes in the source vault. Marked with `// ponytail:`.
- `verify` does not scan remote vaults. Run it where the bytes are.
- `path` returns one path. A model in two vaults needs `--vault`.
