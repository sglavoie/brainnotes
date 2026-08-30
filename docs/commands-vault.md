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

`bn serve` can edit two parts of that file from the browser: the
`[frontmatter.properties.<key>]` blocks, through the property workbench, and
the `[[views]]` blocks, through the saved-views control on `/library`. Every
other setting is edited here, in the file. Anything saved from the browser is
written to the same `brainnotes.toml`, so keep it committed.

Example:

```text
$ bn config
Vault:       /Users/you/vault
Config:      /Users/you/vault/brainnotes.toml
Resolved by: upward search
```

### `bn config get` / `set` / `unset` — script one configuration key

```sh
bn config get   autosave.min_interval
bn config set   autosave.min_interval VALUE [--dry-run]
bn config unset autosave.min_interval [--dry-run]
```

`autosave.min_interval` — the floor between two commits the external file
watcher makes on its own — is currently the **only** scriptable key, and any
other key is refused by name. A key becomes scriptable only once its normalized
spelling, what an absent assignment falls back to, and whether rewriting it
under a running watcher is safe all have answers; nothing else has all three
yet.

`get` prints the effective value and a newline, normalized the way `bn` writes
it, so it reads straight into a shell variable. `set` names a policy: it rewrites
exactly one assignment — leaving every other byte, comments included, alone, and
creating the `[autosave]` table at the end of the file when it is absent — but
only when the value asked for is not the one already in force. `unset` names an
assignment: it removes the key so the built-in default applies again, which is
how an explicit floor is taken back out of the file.

A change is an ordinary mutation with one transaction, one undo-journal entry,
and one immediate commit, so `bn undo` reverts it. A no-op writes nothing and
records nothing, and neither does `--dry-run`, which prints the same diff the
apply would have written. "No-op" is measured against the policy in force, not
the bytes: setting `5m` against a file storing `5m`, storing `300s`, or storing
no assignment at all are all three the same nothing, so a script may assert the
floor it wants on every run and commit only on the run that moves it. Unsetting
a key that is not assigned is a no-op too.

```text
$ bn config get autosave.min_interval
5m

$ bn config set autosave.min_interval 30m
$ bn config get autosave.min_interval
30m

$ bn config unset autosave.min_interval --dry-run
--- brainnotes.toml
- 151: min_interval = "30m"
```

See [Configuration](configuration.md#the-autosave-floor) for the accepted
values and [Vault Safety](operations/vault-safety.md#immediate-versus-scheduled-snapshots)
for what the floor does.

### `bn schema` — show the configured frontmatter property schema

```sh
bn schema
```

Prints the `[frontmatter.properties]` declarations as a table — one row per
property with its `TYPE`, its `ITEMS` kind for lists, whether it is `REQUIRED`,
and its `ALLOWED` values — sorted by property name. It reads the config only
and never walks the vault.

This is the reference to read next to a `bn lint` or `bn repair` report: those
commands enforce exactly these declarations, and a property that is not listed
here has no list semantics, no wikilink wrapping, and no validation. With no
properties declared at all, `bn schema` prints a hint pointing at the config
file rather than an empty table.

```text
$ bn schema
PROPERTY    TYPE      ITEMS     REQUIRED  ALLOWED
categories  list      wikilink  no
status      string              yes       WIP, Done, Archived
tags        list      string    no
```

### `bn index` — report on or rebuild the note cache

```sh
bn index [--status | --rebuild]
```

`bn` keeps a cache of the note bytes it has already read under
`<vault>/.bnotes/index/`, so a repeated vault load — `bn serve` refreshing its
snapshot, most of all — stats each note instead of reading it from disk again.
Only the read is skipped. A warm load still re-parses each cached note's
frontmatter, because every snapshot gets its own freshly parsed note rather than
sharing a mutable one, so the cache saves the file read and not the YAML parse.
The cache is unconditional and invisible: there is no flag to turn it on or off,
nothing in `brainnotes.toml` configures it, and it holds nothing that is not
already in the vault.

That is why the support surface is this small. Any state file `bn` cannot use —
missing, unreadable, undecodable, written by another version, written for
another vault, or internally inconsistent — is discarded and rebuilt on the next
load without asking, so there is no corrupt-cache state to recover from by hand.
`--rebuild` and `rm -rf .bnotes/index` do the same thing.

`bn index` with no flags prints the same report as `--status`. Neither form
walks the vault: reporting on the cache is never what builds it, so a vault that
has never been indexed says so.

```text
$ bn index
State file:  .bnotes/index/index.gob
Status:      not built

$ bn index --rebuild
Rebuilt the index: 412 notes indexed, 0 unreadable.

$ bn index --status
State file:  .bnotes/index/index.gob
Status:      healthy
Entries:     412
Size:        1043968 B
Last write:  2026-08-26T09:14:07Z
```

A state file that exists but cannot be used reports the failure class and what
`bn` will do about it, which is nothing you need to act on:

```text
Status:      unusable (state file could not be decoded); the next load rebuilds it
```

`--status` and `--rebuild` are mutually exclusive: one asks what the cache is,
the other replaces it, and combining them would only describe the file the same
command had just written.

Add `.bnotes/` to `.gitignore` if it is not there already — the cache is derived
state and is rewritten constantly.

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

With `--wipe`, `bn restore` deletes everything in the vault before applying the
archive, except the two directories no archive ever contains: the root
`backups/` directory and `.git/`. Those are kept because nothing could put them
back — `backups/` holds your other archives, and `.git/` holds the vault's
content history, including every version the wipe is about to discard. Every
other non-archived extra is removed. `bn backup` / `bn restore` are vault-level
snapshot commands; they do not create undo journal entries, but a successful
restore is recorded as a single content-history snapshot.

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


