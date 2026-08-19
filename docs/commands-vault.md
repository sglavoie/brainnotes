# Vault administration and recovery

## Command reference

### `bn init` — mark a directory as a vault root

```sh
bn init [path]
```

Writes a commented starter `brainnotes.toml` into `path` (the current directory
when omitted). The directory must already exist. `bn init` refuses to overwrite
an existing `brainnotes.toml` and refuses to write through a symlink, so it is
safe to run against a vault you are not sure about.

### `bn config` — show or edit the vault configuration

```sh
bn config
bn config edit
```

`bn config` prints the resolved vault root, the `brainnotes.toml` that was
loaded, and which of `--vault` / `$BNOTES_VAULT` / the upward search resolved
it. `bn config edit` opens that same file in `$EDITOR`.

`bn serve` can edit one part of that file from the browser: the
`[frontmatter.properties.<key>]` blocks, through the property workbench. Every
other setting is edited here, in the file. A definition saved from the browser
is written to the same `brainnotes.toml`, so keep it committed.

Example:

```text
$ bn config
Vault:       /Users/you/vault
Config:      /Users/you/vault/brainnotes.toml
Resolved by: upward search
```

### `bn backup` — create a vault snapshot archive

```sh
bn backup
```

Creates `<vault>/backups/backup-<UTC timestamp>.tar.gz`.

Existing `backups/` contents and `.git/` are excluded from the archive.
Success output is:

```text
backup: created <archive-path> (files=<n> bytes=<n> [skipped_symlinks=<n>])
```

If the vault contains symlinks, `bn backup` skips them and reports the skipped
count instead of aborting the snapshot.

### `bn restore` — restore a vault snapshot archive

```sh
bn restore <archive> [--wipe]
```

By default, restore runs in overlay mode: files from the archive overwrite any
existing file at the same path, while unrelated files stay in place.

The command validates the full archive before writing and aborts without
changes if the archive is corrupt/unsafe or if overlay mode hits a destination
type conflict (for example, a file where a directory is required).

Relative archive paths are resolved from the vault root; absolute paths are
also accepted. Success output is:

```text
restore: restored <archive-path> (mode=<overlay|wipe> files=<n>)
```

With `--wipe`, `bn restore` deletes everything in the vault except the root
`backups/` directory and its contents before applying the archive. That
includes `.git/` and any other non-archived extras. `bn backup` / `bn restore`
are vault-level snapshot commands; they do not create undo journal entries.

### `bn history` — list recent undo journal entries

```sh
bn history
```

Lists entries newest-first from `<vault>/.bnotes/journal/`. Output is
tab-separated with columns:

- `TIMESTAMP` (RFC3339Nano UTC),
- `ID` (entry id),
- `COMMAND` (recorded source command).

Use the entry ID with `bn undo <id>`.

### `bn undo` — restore one undo journal entry

```sh
bn undo [id] [--dry-run] [--force]
```

Restores every path recorded in one journal entry to its pre-command state.
When `id` is omitted, `bn undo` restores the most recent entry.

By default, undo is fail-fast: if any current file state no longer matches the
journaled post-command state, the restore aborts with no writes. Use `--force`
to overwrite conflicts.

`--dry-run` prints deterministic preview blocks:

```text
undo: <entry-id> (<source-command>)
--- <path>
- state: <current-state>
+ state: <restored-state>
```

v1 limit: restore is whole-entry only (no per-file interactive restore).


