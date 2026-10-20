# Maintenance commands: schema and key repair

Part of [Maintenance and bulk-edit commands](commands-maintenance.md).

### `bn lint` — report schema violations without changing anything

```sh
bn lint [find flags] [-q EXPR] [--view name] [--property key] [--json]
```

Checks every note — or the subset the find filters select — against the
property definitions in `[frontmatter.properties]` and prints the violations.
Nothing is written. Exit code `0` means clean; exit code `1` means violations
were found, which makes `bn lint` usable directly as a CI or pre-commit gate;
exit code `2` means lint could not run at all (for example, an unknown
`--property`), so a gate can tell a dirty vault from a broken invocation.
`--json` prints `{"count", "violations": [{"path", "property", "kind",
"detail"}]}` instead of the grouped report, with the same exit codes.

A note whose frontmatter cannot be parsed at all is reported as a
`frontmatter: unparsable` violation carrying the parse error, rather than
aborting the run. It is reported whatever the filters select, because a note
that cannot be parsed cannot be matched against them either.

`--property <key>` narrows the check to one declared property; naming a
property that is not declared is an error rather than a silent pass. With no
properties declared at all, `bn lint` prints a hint pointing at the config file
instead of reporting a clean vault. `bn lint` takes no positional note
arguments: scope it with the find flags, `-q`, or `--view`.

Use [`bn schema`](commands-vault.md#bn-schema--show-the-configured-frontmatter-property-schema)
to read the declarations a violation refers to, and `bn repair` below to fix
the mechanically fixable ones.

Examples:

```sh
bn lint                                   # whole vault
bn lint --folder References               # one folder
bn lint --property status                 # one declared property
bn lint -q 'status:wip'                   # scoped by query expression
```

### `bn rename-key` — rename a frontmatter key across notes

```sh
bn rename-key <old-key> <new-key> [note...] [find flags] [--dry-run]
```

Renames a frontmatter key in every matching note, preserving the value exactly:
no type coercion and no wikilink wrapping, since only the key changes. Keys are
limited to letters, digits, `_`, and `-`, and the two keys must differ.

Notes where `<old-key>` is absent are skipped silently. Notes where
`<new-key>` already exists are skipped with a `rename-key: warning:` line on stderr and
counted in the summary, so a rename never merges two keys or overwrites the
destination value.

Scope is the same three-way rule `bn replace` uses: the whole vault with no
notes and no find flags, the filtered set with find flags, or exactly the
positional notes — find flags and positional notes are mutually exclusive.

Each file is rewritten atomically. The run is one undo journal entry, so `bn undo` reverses a completed run
(`bn history` lists entry IDs). There is no cross-file transaction: if an
error stops the run mid-way, prior writes stand and the unfinished entry
cannot be undone — use `git diff` to recover.
`--dry-run` prints the shared preview (`--- <relpath>`, then the removal of the
old key and the addition of the new one as `frontmatter[<key>]` lines) and
writes nothing.

Renaming a key that is declared in `[frontmatter.properties]` prints a `rename-key: warning:` line
reminding you that `brainnotes.toml` still names the old key — `bn` does not
edit your declarations for you.

Examples:

```sh
bn rename-key area areas --dry-run                # preview vault-wide
bn rename-key area areas                          # apply
bn rename-key reviewed last_reviewed --tag project
bn rename-key kind type Areas/Alpha.md            # explicit notes
```

### `bn repair` — fix safe schema violations in frontmatter

```sh
bn repair [note...] [find flags] [--property key] [--dry-run]
```

Inspects frontmatter against the property definitions in
`[frontmatter.properties]` and applies safe, deterministic fixes. Fix
categories:

| Category | What it does |
| --- | --- |
| Type coercion | Convert when the stored value is lossless (e.g. `"true"` → `true`) |
| Allowed-value canonicalization | Case-only normalization to an allowed value (e.g. `wip` → `WIP`) |
| Wikilink wrapping | Bare strings → `[[...]]` for wikilink properties |
| List deduplication | Remove duplicate list entries, preserving order |

Issues that cannot be fixed mechanically (unknown values, missing required
properties with no default) are reported on stderr as `SKIP:` lines and left
untouched.

Scope:

- No notes and no find flags: every note in the vault.
- One or more find flags (`--tag`, `--folder`, `--status`, …): notes matching
  the filter.
- Positional notes: only those notes. Mutually exclusive with find flags.

`--property` restricts repair to a single declared property.

Each file is rewritten atomically. The run is one undo journal entry, so `bn undo` reverses a completed run
(`bn history` lists entry IDs). There is no cross-file transaction: if an
error stops the run mid-way, prior writes stand and the unfinished entry
cannot be undone — use `git diff` to recover.

`--dry-run` previews changes in the same shared preview format as `bn set
--dry-run` (`--- <relpath>`, then `frontmatter[<key>]: <old> -> <new>` lines)
without writing.

The intended workflow is:

```sh
bn lint                  # detect violations
bn repair --dry-run      # preview safe fixes
bn repair                # apply
bn lint                  # verify residue
```

Examples:

```sh
bn repair                                   # fix everything safe
bn repair --dry-run                         # preview only
bn repair --property status                 # fix only status violations
bn repair --folder References               # scoped by folder
bn repair --tag project --property status   # combined scoping
bn repair note.md                           # single note
```

### `bn relink` — repair unresolved wikilinks with deterministic rewrites

```sh
bn relink [note...] [find flags] [--dry-run]
```

Scans unresolved wikilinks and rewrites only links with a deterministic target:

- exact path/key match after path normalization (for example, links using
  backslashes on Unix vaults),
- exact title match (case-insensitive, optional `.md`) when unique.

Ambiguous targets are skipped and reported as `SKIP:` lines for manual follow-up.

Scope:

- No notes and no find flags: entire vault.
- One or more `find`-style flags (`--tag`, `--folder`, `--status`, ...):
  matching notes only.
- Positional note paths: only those notes. Mutually exclusive with find flags.

`--dry-run` prints the shared preview format per changed file (`--- <relpath>`,
then `body:<line>: <old> -> <new>` for body links and
`frontmatter[<key>]: <old> -> <new>` for frontmatter links) and skips all
writes.

Examples:

```sh
bn relink --dry-run                         # preview only
bn relink                                   # apply safe rewrites
bn relink --folder References               # scoped by folder
bn relink --tag project --broken-links      # scoped by find flags
bn relink Notes/Index.md                    # explicit note
```

