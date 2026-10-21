# Vault administration and recovery

## Command reference

### Global options

```sh
bn --vault <path> <command> ...
bn --version
bn [command] --help
```

`--vault` selects the vault root for any command and overrides
`$BNOTES_VAULT` and the upward search for `brainnotes.toml`. `bn --version`
prints the build identifier — the Git revision the binary was built from,
`-dirty` for an uncommitted tree — which `bn-vault-guard -version` and
`bn-vault-sync -version` also print, so the three installed binaries can be
compared (see [vault safety](operations/vault-safety.md)). It has no `-v`
shorthand: `-v` is the `--verbose` of `links`, `backlinks`, `values` and
`tags`.

### `bn init` — mark a directory as a vault root

```sh
bn init [path]
```

Writes a commented starter `brainnotes.toml` into `path`. When `path` is
omitted it is the `--vault` directory if that flag is given, otherwise the
current directory; a `path` and a `--vault` that name different directories are
refused. The directory must already exist. `bn init` refuses to overwrite
an existing `brainnotes.toml` and refuses to write through a symlink, so it is
safe to run against a vault you are not sure about.

### `bn config` — show or edit the vault configuration

```sh
bn config [--json]
bn config explain <key> [--json]
bn config edit
```

`bn config` prints the resolved vault root, the `brainnotes.toml` that was
loaded, the active `brainnotes.local.toml` overlay (or `none`), and which of
`--vault` / `$BNOTES_VAULT` / the upward search resolved it. `bn config edit` opens the portable file in `[open].editor` (falling back to `$EDITOR`), even when it no longer parses.

`bn config explain open.editor` shows the effective value after defaults and
normalization, the file that supplied it (or built-in default), and the file
that owns the key. Inspection accepts schema field keys and writes nothing.

`--json` prints either report as an object: `bn config --json` as
`{"vault", "config", "overlay", "resolved_by"}` with `overlay` null when no
overlay is loaded, and `bn config explain <key> --json` as
`{"key", "source", "owned_by", "value"}` with `source` null for a built-in
default. `value` is the same value the text form prints, under its config key
names, and an empty list or table is `[]` or `{}`.

The optional `brainnotes.local.toml` overlay holds settings for this machine.
It is listed when loaded, but `edit` continues to open the portable file.
Use the source and ownership paths from `explain` to choose the appropriate file.

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
Overlay:     none
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
the bytes: setting `5m` against a file storing `5m`, storing `300s`, storing any
other TOML spelling of it (`'5m'`, `"""5m"""`, `"5\u006d"`), or storing no
assignment at all are all the same nothing, so a script may assert the floor it
wants on every run and commit only on the run that moves it. Unsetting
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

`set` and `unset` write **the file that owns the key**. `autosave.min_interval`
is portable, so that is `brainnotes.toml`; if `brainnotes.local.toml` assigns it
anyway, the overlay wins and the write is refused rather than applied to the
file underneath it — a patch nothing would use, reported as a success, is the
one outcome worth refusing over. `bn config migrate` is the way out of that.

See [Configuration](configuration.md#the-autosave-floor) for the accepted
values and [Vault Safety](operations/vault-safety.md#immediate-versus-scheduled-snapshots)
for what the floor does.

### `bn config migrate` — move every key into the file that owns it

```sh
bn config migrate [--dry-run]
```

Relocates each assignment sitting in the wrong configuration file: the keys
naming *this machine* — `open.editor`, `serve.addr`, `serve.open_browser`,
`fzf.args`, `agent.*`, `cockpit.agent_*` — into `brainnotes.local.toml`, and
everything describing the vault itself into `brainnotes.toml`. Run it on a
legacy single-file vault before enrolling it in synchronization, or on an
enrolled vault whose configuration the loader has started refusing.

It is one of the few commands that runs **without** a successful configuration
load behind it, for that second case: once a vault is enrolled, a misplaced key
stops it loading at all, and requiring a load first would put the only repair
behind the fault it repairs.

- Assignments move as text, so values, quoting, and the comment on a moved line
  arrive unchanged. A comment sitting *above* a key stays where it is — nothing
  in a TOML file says which key a free-standing comment documents.
- The vault behaves identically afterwards. If any key would end up with a
  different effective value the migration is refused rather than written.
- A key **both** files assign is the one case where an assignment disappears:
  only the overlay's copy was ever in force, so that is the one kept. It is
  spelled out in the summary rather than reported as a plain move.
- Running it again does nothing, and says so.
- `--dry-run` prints the moves and both files' diffs and writes nothing.
- Both files move under one transaction and one journal entry, so `bn undo`
  restores the originals — including removing an overlay the migration created —
  and an interrupted run leaves either both files migrated or neither.

```text
$ bn config migrate --dry-run
open.editor: brainnotes.toml -> brainnotes.local.toml
serve.addr: brainnotes.toml -> brainnotes.local.toml
--- brainnotes.toml
- 12: editor = "hx"
- 18: addr = "127.0.0.1:8733"
--- brainnotes.local.toml
+ 7: [open]
+ 8: editor = "hx"
+ 10: [serve]
+ 11: addr = "127.0.0.1:8733"
```

See [Configuration](configuration-overlay.md#the-machine-local-overlay) for which file
owns which key and
[Repairing a misplaced configuration](configuration-overlay.md#repairing-a-misplaced-configuration)
for when to run this.

### `bn schema` — show the configured frontmatter property schema

```sh
bn schema [--json]
```

Prints the `[frontmatter.properties]` declarations as a table — one row per
property with its `TYPE`, its `ITEMS` kind for lists, whether it is `REQUIRED`,
and its `ALLOWED` values — sorted by property name. It reads the config only
and never walks the vault.

This is the reference to read next to a `bn lint` or `bn repair` report: those
commands enforce exactly these declarations, and a property that is not listed
here has no list semantics, no wikilink wrapping, and no validation. With no
properties declared at all, `bn schema` prints a hint pointing at the config
file rather than an empty table. `--json` prints an array of
`{"name", "type", "items", "required", "allowed"}` records in the same order
(`[]` when nothing is declared).

```text
$ bn schema
PROPERTY    TYPE      ITEMS     REQUIRED  ALLOWED
categories  list      wikilink  no
status      string              yes       WIP, Done, Archived
tags        list      string    no
```

### `bn index` — report on or rebuild the note cache

```sh
bn index [--status | --rebuild] [--json]
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

`--json` prints the status report as
`{"state_file", "status", "problem", "entries", "bytes", "last_write"}`, with
`status` one of `not built`, `unusable` or `healthy` and the fields that state
does not report set to null. It cannot be combined with `--rebuild`.

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
bn restore <archive> [--wipe [--yes]] [--dry-run]
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

Because a wipe cannot be undone with `bn undo`, `--wipe` asks before deleting
anything, naming how many paths it would remove. `--yes` skips the prompt, and
is required when stdin is not a terminal: a script gets a refusal (exit 2)
rather than a wipe it never confirmed. `--dry-run` validates the archive and
lists what the restore would `create`, `overwrite` and (with `--wipe`)
`remove`, then a `summary:` line, without changing the vault:

```sh
bn restore backups/backup-20260528T232425.123456789Z.tar.gz --wipe --dry-run
```

### `bn checkpoint` — hold the vault still while another tool copies it

```sh
bn checkpoint run [--drain <duration>] [--lease <duration>] [--reason <text>] -- <command> [args...]
bn checkpoint status [--json]
```

A checkpoint is the vault's quiet window. While one is held, every write that
already reported success is durable and complete, and no write is part-way
through its own steps — notes, configuration, the undo journal, the content
history, staged uploads, activity state and synchronization transitions alike.
A copy taken inside the window is therefore a state the vault was actually in.
Use it to wrap an external snapshot tool (`tar`, `rsync`, a filesystem
snapshot) that does not know about `bn`.

`bn checkpoint run` takes the checkpoint, runs the command, and releases the
checkpoint afterwards whether the command succeeded or not:

```sh
bn checkpoint run -- tar czf /backups/vault.tar.gz .
bn checkpoint run --reason "nightly deployment snapshot" --drain 2m -- ./snapshot.sh
```

- The command is executed directly, with no shell and no expansion, and
  inherits standard input, output and error. Its exit status becomes
  `bn checkpoint run`'s exit status.
- Writers are paused, not failed: a write that starts during the checkpoint
  waits for it, and gives up only if the checkpoint outlasts the writer's own
  wait budget.
- `--drain` (default `30s`) is how long to wait for writers already in flight.
  If they do not finish in time, or another checkpoint already holds the vault,
  nothing is held and the command does not run.
- `--lease` (default `10m`) is the lifetime recorded for observers, and
  `--reason` is a short sentence recorded alongside it.
- The command inherits the checkpoint locks. If `bn` dies first, the vault stays
  held until the command exits, and only then can later writers proceed.

`bn checkpoint status` reports what the checkpoint record says and whether the
process that wrote it is still running. It takes no lock, creates nothing, never
blocks, and always exits 0. With nothing held it prints `checkpoint: none`;
`--json` emits the same report as `announced`, `live` and `blocking` fields. An
announcement whose process is gone is reported as present but not live: it
blocks nothing, and the next checkpoint clears it, so there is nothing to repair
by hand.

### `bn history` — list recent undo journal entries

```sh
bn history [--json]
```

Lists entries newest-first from `<vault>/.bnotes/journal/`. Output is
tab-separated with columns:

- `TIMESTAMP` (RFC3339Nano UTC),
- `ID` (entry id),
- `COMMAND` (recorded source command).

`--json` prints `{"entries": [...]}` instead, each entry with its `id`,
`timestamp`, `command` and the vault-relative `files` it changed. An empty
journal prints nothing on stdout and says so on stderr.

Use the entry ID with `bn undo <id>`.

### `bn undo` — restore one undo journal entry

```sh
bn undo [id] [--dry-run] [--force]
```

Restores every path recorded in one journal entry to its pre-command state.
When `id` is omitted, `bn undo` restores the most recent entry. With an empty
journal there is nothing to restore: it prints `undo: no journal entries found`
and exits 1, the negative-answer status, while `bn history` exits 0 with an
empty list.

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


