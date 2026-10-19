# Maintenance commands: rename, move, and property edits

Part of [Maintenance and bulk-edit commands](commands-maintenance.md).

### `bn rename` — change a note's title and rewrite wikilinks

```sh
bn rename <note> <new-title> [--dry-run] [--force]
```

`<note>` may be an absolute path, a path with separators, a `.md`-suffixed
filename, or a bare title (resolved case-insensitively against the vault).

Rewrites every `[[wikilink]]` in the vault that pointed at the old title —
including folder-prefixed (`[[Books/Old]]`), aliased (`[[Old|alt]]`), and
heading (`[[Old#Section]]`) forms, and the table-escaped alias Obsidian writes
inside a Markdown table (`[[Old\|alt]]`), which keeps its `\|`. Wikilink-typed frontmatter values
(`categories`, `parent`, …) are also rewritten.

Atomicity: each affected file is rewritten via temp-file + rename. There is no
cross-file transaction — if a mid-apply error occurs, prior writes stand. Use
`git diff` to inspect.

`--dry-run` prints the shared preview format: a `--- <relpath>` header per
affected note, a leading `path: <old> -> <new>` line on the renamed note, then
`body:<line>: <old> -> <new>` and `frontmatter[<key>]: <old> -> <new>` change
lines.

Examples:

```sh
bn rename "References/Books/Old Title.md" "New Title" --dry-run
bn rename "Old Title" "New Title"           # bare-title lookup
```

### `bn move` — move a note between folders

```sh
bn move <note> <new-folder> [--dry-run] [--force]
```

Changes the folder portion of `<note>`'s path; the title stays the same. Every
folder-prefixed wikilink that named the old `<folder>/<title>` is rewritten —
including aliased and headed forms. Bare `[[Title]]` links without a folder
prefix are left untouched (Obsidian resolves them by title alone).

`<new-folder>` is vault-relative; `.` or `""` means the vault root.
Intermediate directories are created with mode `0755`.

`--dry-run` prints the same shared preview format as `bn rename --dry-run`: a
`--- <relpath>` header per affected note, a leading `path: <old> -> <new>` line
on the moved note, then `body:<line>:` and `frontmatter[<key>]:` change lines.

Examples:

```sh
bn move "References/Books/Dune.md" References    # promote to References/
bn move Dune References/Books                    # demote, by bare title
bn move "Old/Foo.md" .                           # move to vault root
```

### `bn replace` — rewrite a frontmatter value across notes

```sh
bn replace <key> <from> <to> [notes...] [find flags] [--dry-run]
```

Rewrites the value of `<key>` from `<from>` to `<to>` in every matching note.
For list-valued keys, every matching element is replaced in place (so
duplicates are all updated). For scalar keys, the stored value is replaced
when it equals `<from>`. Keys absent from a note, or present with a different
value, are skipped silently.

Scope:

- No notes and no find flags: the entire vault.
- One or more `find`-style flags (`--tag`, `--folder`, `--status`, ...): notes
  matching the filter, with the same semantics as `bn find`.
- Positional note paths: only those notes. Mutually exclusive with find flags
  (passing both is an error).

Type coercion of `<from>`/`<to>` matches `bn set` (`true`/`false` → bool,
integer → int, `YYYY-MM-DD` → date, else string). Wikilink-typed properties
have both ends wrapped in `[[ ]]` before comparison so
`bn replace categories Books Films` rewrites a stored `[[Books]]` to
`[[Films]]`.

Each file is rewritten atomically, but there is no cross-file transaction: if
an error stops the run mid-way, prior writes stand — use `git diff` to
recover.

`--dry-run` prints the same shared preview as `bn set --dry-run`
(`--- <relpath>`, then `frontmatter[<key>]: <old> -> <new>` lines) and skips all
writes.

Examples:

```sh
bn replace status wip done                                  # vault-wide
bn replace status wip done --tag project                    # scoped by tag
bn replace categories Books Films --folder References       # scoped by folder
bn replace tags shared common Areas/Alpha.md Areas/Beta.md  # explicit notes
bn replace status wip done --dry-run                        # preview only
```

### `bn set` — write frontmatter properties

```sh
bn set <note...> <key[=|+=|-=]value...> [--type date|datetime|int|bool|float|raw] [--dry-run] [-0]
```

Positional args are split: anything containing an operator (`=`, `+=`, `-=`)
is a pair, and all preceding args are note paths. Use `-` as the sole note
arg to read paths from stdin (so it composes with `bn find`).

Operators:

- `key=value` — set the property (overwriting any prior value).
- `key+=value` — append to a list property; no-op if the value is already
  present (the file is not rewritten, so mtime is unchanged).
- `key-=value` — remove a value from a list property; no-op if absent.

`+=` and `-=` require the key to be declared with `type = "list"` in
`[frontmatter.properties]`; using them on any other key is an error. Values are comma-split for list properties
the same way `=` does (`tags+=foo,bar` adds two entries). Operators can be
mixed in one invocation and apply left-to-right.

Type coercion is automatic: `true`/`false` → bool, integer → int, `YYYY-MM-DD`
→ date, else string. Use `--type` to force. Wikilink-typed properties are
wrapped in `[[ ]]` on write; for `+=`/`-=` the
wrapping happens before the comparison so `tags+=Books` matches an existing
`[[Books]]` entry.

Frontmatter is rewritten with **alphabetically sorted top-level keys**. Each
file is rewritten atomically, but there is no cross-file transaction: if an
error stops the run mid-way, prior writes stand — use `git diff` to recover.

`--dry-run` prints the shared preview format and skips all writes: a
`--- <relpath>` header per changed note, then one `frontmatter[<key>]: <old> ->
<new>` line per changed property. Files with no effective change are omitted,
and blocks are emitted in stable (sorted) path order. Every mutating command —
`set`, `unset`, `replace`, `repair`, `relink`, `rename`, and `move` — now emits
this same canonical format (the path-moving commands add `path:` and
`body:<line>:` lines; see `rename`/`move` above and `relink` in [schema and key repair](commands-maintenance-schema.md)).

Examples:

```sh
bn set References/Books/Dune.md status=Reading rating=7
bn set note.md categories=Books,Personal                    # → wikilink list
bn set note.md created=2026-01-01 --type=date
bn set note.md tags+=project                                # append a tag
bn set note.md tags+=foo,bar                                # append multiple
bn set note.md tags-=draft                                  # drop a tag
bn set note.md tags+=alpha tags-=beta                       # combined
bn set note.md status=done --dry-run                        # preview only
bn find --tag drafts | bn set - status=done                 # stdin pipeline
bn find --tag drafts | bn set - tags+=triage                # bulk append
```

### `bn tags` — count tag occurrences

```sh
bn tags [--sort count|name] [--count=false] [--files]
```

Alias for `bn values tags`. See `bn values` for flag behavior.

### `bn unset` — remove frontmatter properties

```sh
bn unset <note> <property...> [--dry-run] [-0]
bn unset <note...> -- <property...>
```

Without `--`, the first positional is the note (or `-` for stdin) and the rest
are property names. To pass multiple notes, use `--` to separate them from
the property list.

Removing a missing property is a silent no-op. `--dry-run` previews the
removal in the same shared format as `bn set --dry-run` (one
`frontmatter[<key>]: <old> -> ` line per present property) without touching
disk; missing properties remain silent in dry-run mode too.

Examples:

```sh
bn unset note.md rating
bn unset a.md b.md -- title status                          # multi-note
bn unset note.md rating --dry-run                           # preview only
bn find --has rating | bn unset - rating                    # bulk wipe
```

### `bn values` — count occurrences of a frontmatter property's values

```sh
bn values <property> [--sort count|name] [--count=false] [--files]
```

Walks every note, parses frontmatter, and counts how often each value of
`<property>` appears across the vault. Useful for hygiene: typos and stray
variants surface in the long tail.

- String values are counted once; list values are flattened. Map-typed values
  are skipped (use `-v` to surface a one-per-note warning on stderr).
- Wikilink-typed values are emitted with `[[ ]]` intact, since drift between
  `[[Project Alpha]]` and `[[project alpha]]` is exactly the kind of mismatch
  this command exists to surface.
- Default sort is count desc with alphabetical tie-break. `--sort=name` sorts
  alphabetically. `--count=false` emits values only (one per line), handy for
  piping to `grep` or `fzf`.
- `--files` groups output by value and lists the vault-relative path of every
  note containing that value. Combines with `--sort` and `--count=false`.

Examples:

```sh
bn tags                                # count desc, project=42, meeting=18, …
bn tags --sort=name                    # alphabetical
bn values category                     # category-value hygiene
bn values tags --count=false | fzf     # pick a tag to inspect
bn tags --files                        # which notes have each tag?
bn values category --files --sort=name # files per category, alphabetical
```


