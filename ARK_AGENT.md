# Arkadian Agent Guide

Use this file to teach an AI agent how to operate Arkadian. Add its path to the
agent's project instructions or provide it as context. This guide covers the
`ark` CLI. It does not configure the agent itself.

## Before You Start

Check that `ark` is installed and available on `PATH`. Run `ark --help` to see
the commands. Use `ark <command> --help` for command details.

Arkadian manages Hugging Face model copies in named vaults. A vault has type
`huggingface` and points to a native Hugging Face cache. The default working
vault is usually `hfcache`.

Check the current inventory first:

```sh
ark list
ark vault ls
```

Use the exact Hugging Face repository ID shown by Arkadian. Do not treat a short
name as an alias. Use `ark refresh` when a mounted vault or external tool may
have changed the files.

## Common Tasks

Download a repository into a named vault:

```sh
ark pull hf://org/model vault-name
```

Copy a model between vaults and keep the source:

```sh
ark cp org/model source-vault destination-vault
```

Move a model after Arkadian verifies the destination:

```sh
ark mv org/model source-vault destination-vault
```

Bring an available copy into the default working vault:

```sh
ark get org/model
```

Keep a destination copy and free the default working vault:

```sh
ark evict org/model destination-vault
```

Add models from one vault to another. Sync is one-way and additive:

```sh
ark sync source-vault destination-vault
```

Find a model path for another program:

```sh
ark path org/model
```

Check model copies against their saved manifests:

```sh
ark verify org/model
```

## Safety

- Ask the user before deleting or moving model data. This includes `mv`,
  `evict`, and `rm`.
- Do not add `--yes` unless the user approved the operation. It skips the
  confirmation prompt.
- Do not use `--force` to bypass a failed verification, a content conflict, or
  an integrity check. It only overrides the space limit and, for `rm`, permits
  deliberate removal of the last known copy.
- Prefer `cp` when the user asks to preserve the source. Prefer `get` when the
  user asks to make an existing copy available in the default vault.
- Check `ark list` after a change. Use `ark verify` when the user asks to
  confirm file integrity.
- Do not assume an SSH path is locally accessible to a model runtime. Ask the
  user to mount remote storage or copy the model into a local vault.
- Do not configure mounts, SSH keys, authentication, or the operating system.
  Report missing tools or access as actionable errors.
- Do not invent flags or command aliases. Follow `ark --help` and the command
  help output.

## Output

Keep diagnostics visible to the user. `ark path` writes the selected path to
standard output. `ark list --json` returns structured inventory data. Report
command failures and their error messages. Never claim a copy is verified unless
Arkadian reports that state.
