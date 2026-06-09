# bnotes (`bn`)

Brain Notes is a small, fast Go CLI for editing and querying a plain-Markdown
notes vault. It complements an editor (VS Code, Neovim) by handling the
operations editors do poorly: frontmatter-aware queries, wikilink-aware file
operations, and templated note creation.

## Install

```sh
go install github.com/sglavoie/brainnotes/cmd/bn@latest
```

Or, from a local clone:

```sh
just install
```

## Development

```sh
npm ci
just web-check
just lint
```

Run `npm ci` before frontend work or after package-lock changes. `just web-build`
regenerates the committed `internal/web/static/app.js` from
`internal/web/static_src`; `just web-check` type-checks, runs frontend tests, and
rebuilds it. The generated JavaScript is committed so Go-only builds do not need
Node. Local `just build` and `just install` stamp this checkout path into the
binary so `bn serve` can refresh web assets from this source tree even when
launched from another directory.

## Quick start

```sh
# Point bn at your vault (or set BNOTES_VAULT / config).
export BNOTES_VAULT=/path/to/vault

# Find every book note that's still 'To Read'.
bn find --category Books --status "To Read"

# Pipe to fzf for interactive selection.
bn find --category Books -i

# Set a property across many notes.
bn find --tag drafts | bn set - status=done
```

## Configuration

The config file is searched, in order:

1. `--config <path>` flag
2. `BNOTES_CONFIG` environment variable
3. `$XDG_CONFIG_HOME/bnotes/config.toml`
4. `~/.config/bnotes/config.toml`

Run `bn config` to see which file is active; `bn config edit` to open it (a
starter template is created on first run).

Vault path resolution, in order:

1. `--vault <path>` flag
2. `BNOTES_VAULT` environment variable
3. The `vault` key in the config file

### Schema

```toml
# Required.
vault = "/path/to/vault"

# Extra vault-relative paths to skip during Walk; merged with hard-coded
# defaults: .git, .obsidian, Attachments, Archive, Trash. The configured
# [templates].dir is also skipped automatically.
ignore = []

[templates]
dir = "Templates"                          # relative to vault
default_extension = ".md"

[frontmatter]
# Properties whose string values are wrapped in [[wikilinks]] on write.
wikilink_properties = ["categories", "org", "parent", "area"]
# Properties that `bn set k=a,b,c` splits into a YAML list.
list_properties = ["categories", "tags", "topics", "org", "type"]

[fzf]
# Args appended to the fzf invocation when `-i` is passed.
args = ["--preview", "bat --style=plain --color=always {}"]

[open]
# Editor to invoke when opening a note. Falls back to $EDITOR when not set.
# editor = "nvim"

[serve]
# HTTP listen address for `bn serve`.
addr = "127.0.0.1:8729"
# Open your browser on startup. Set false for headless use.
open_browser = true

# Routing for `bn new <kind> "Title"`. Folder is vault-relative.
[new.routes]
book    = "References/Books"
person  = "People"
goal    = "Goals"
project = "Projects"
daily   = "Daily"
```

Default ignore entries (always applied): `.git`, `.obsidian`, `Attachments`,
`Archive`, `Trash`. The configured `[templates].dir` (default `Templates`) is also
skipped automatically, since template files contain unrendered placeholders
that aren't valid YAML.

## Commands

### `bn find` — query notes by frontmatter

```sh
bn find [--tag t]... [--category c]... [--status s] [--type t]
        [--folder rel] [--has key] [--missing key]
        [--created EXPR] [--age EXPR] [--orphan]
        [--broken-links [--show-broken | --list-broken-targets]]
        [-i | --interactive] [-0]
```

Prints matching absolute paths, one per line (or NUL-separated with `-0`).
Pipe into `fzf` with `-i` for an interactive picker.

Filters AND together. Age expressions take the form `>30d`, `<7d`, `=0d`;
`--created` reads the `created` property, `--age` reads `last`.

`--broken-links` selects notes containing at least one wikilink whose target
does not match any note title in the vault. Resolution is title-only
(case-insensitive stem match) — a folder prefix like `[[Folder/Title]]` still
resolves by title alone, matching Obsidian's behavior. Media embeds
(`![[image.png]]`) and links inside code fences are ignored.

Two companion flags provide detail views (mutually exclusive, both require
`--broken-links`):

| Flag | Output |
| --- | --- |
| `--show-broken` | One line per broken link: `<relpath>:<line>: [[target]]` for body links, `<relpath>:frontmatter[<key>]: [[target]]` for frontmatter links. |
| `--list-broken-targets` | Unique broken target titles, sorted case-insensitively, one per line. |

Examples:

```sh
bn find --tag books                       # all notes tagged 'books'
bn find --category Books --status WIP
bn find --has rating --created '>365d'    # stale ratings
bn find --orphan                          # no inbound wikilinks
bn find --broken-links                    # notes with unresolved wikilinks
bn find --broken-links --show-broken      # per-link detail
bn find --broken-links --list-broken-targets  # unique missing titles
bn find --tag drafts -i                   # interactive fzf pick
bn find --tag drafts -0 | xargs -0 nvim   # safe for spaces in paths
```

#### Repair broken links

Start with safe deterministic rewrites:

```sh
bn relink --dry-run
bn relink
bn find --broken-links --show-broken
```

Then (optionally) create stub notes for any remaining targets:

```sh
bn find --broken-links --list-broken-targets | xargs -I{} bn new "{}"
```

Re-run `bn find --broken-links` afterward to verify zero residue.

### `bn search` — search note bodies

```sh
bn search [pattern] [note...] [find-flags]
          [-e | --regex] [--case-sensitive]
          [-C N | --context N] [--count] [--open]
          [-i | --interactive] [-0]
```

Searches note bodies for a literal string or regex, printing matching note paths one per line.
By default the match is case-insensitive. Find flags (`--tag`, `--category`, `--folder`, etc.) scope
the search corpus; positional note arguments and find flags are mutually exclusive.

**Pattern is optional with `-i`**: when omitted, all body lines of matching notes are fed to fzf for
interactive filtering. fzf matching is limited to body text; filenames are display/preview metadata.

**Interactive mode** (`-i`) opens a two-pane fzf view — the main pane shows matching lines in grep
style (`relpath:linenum:content`), the preview pane shows the full note scrolled to the matched line.
Settings from `cfg.Fzf.Args` apply — for example, add to your config for a syntax-highlighted preview:

```toml
[fzf]
args = ["--preview", "bat --style=numbers --color=always --highlight-line {2} {1}"]
```

Here `{1}` is the absolute note path and `{2}` is the matched line number.

With `--open`, `bn search` opens the first matching result in the configured editor
(`[open].editor`, then `$EDITOR`) at the matching file line. When combined with `-i`, it opens the
selected fzf row instead.

| Output mode | Flag | Description |
| --- | --- | --- |
| paths (default) | — | One absolute path per matching note, sorted. |
| context | `--context N` / `-C N` | Each match with N lines of surrounding body context (grep-style). |
| count | `--count` | Match count per note, sorted by count descending then path. |
| open | `--open` | Open the first matching note at its first matching line, or the selected row with `-i`. |

Examples:

```sh
bn search "activation energy" --tag physics
bn search "TODO" --context 2
bn search "meeting" --count
bn search "activation energy" --open
bn search -i                            # interactive, all notes
bn search -i --tag drafts               # interactive, scoped to drafts
bn search "energy" -i                   # pre-filtered, then interactive
bn search "energy" -i --open            # choose a line, then open it in the editor
bn find --tag drafts | bn search "review" -
```

### `bn backlinks` — list notes that link to a target

```sh
bn backlinks <note> [-0] [--verbose]
```

`<note>` is either a bare title (case-insensitive stem lookup) or a path/glob.
Prints the absolute paths of every note whose body or frontmatter contains a
`[[wikilink]]` to the target, one per line (or NUL-separated with `-0`). Links
inside fenced, indented, or inline code are not counted.

With `--verbose`, each occurrence is printed on its own line as
`<path>:<line>\t<snippet>` for body links, or `<path>:fm:<key>\t<value>` for
frontmatter links. Output is tab-separated so it pipes cleanly into
`awk -F'\t'`. Snippets are trimmed and truncated to 120 characters with an
`…` ellipsis. `--verbose` has no `-v` short form because the root `--verbose`
already claims `-v`.

Examples:

```sh
bn backlinks "Crafting Interpreters"
bn backlinks References/Books/Dune.md
bn backlinks Dune | xargs bn set status=referenced
bn backlinks Dune --verbose | awk -F'\t' '{print $1}'
```

### `bn config` — show or edit the config file

```sh
bn config
bn config edit
```

`bn config` prints the active config path and the resolution chain so you can
see which file `bn` will load and what else was inspected. Each row shows
`not set` when the underlying input is empty; otherwise the path is shown with
`[active]` (the first non-empty entry) and `[exists]` / `[missing]`.

`bn config edit` opens the active config in `$EDITOR`. If no file exists at
the resolved path, a commented starter template is written there first
(parent directories are created as needed), so first-time setup is a single
command.

Example:

```text
$ bn config
Active config: /Users/you/.config/bnotes/config.toml (default, exists)

Resolution order (highest to lowest precedence):
  1. --config flag        not set
  2. $BNOTES_CONFIG       not set
  3. $XDG_CONFIG_HOME     not set
  4. default path         /Users/you/.config/bnotes/config.toml  [active] [exists]
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

### `bn new` — create a note from a template

```sh
bn new <template> <title> [--folder rel] [--var k=v]... [--force] [--edit]
```

Renders `<template>` from `<vault>/Templates/` and writes it to the routed (or
`--folder`) destination as `<title>.md`. Template lookup is case-insensitive
and tries: `<name>`, `<name>.md`, `<name> Template.md`.

Available template functions:

| Function        | Purpose                                            |
| --------------- | -------------------------------------------------- |
| `now`           | `time.Time` at render                              |
| `today`         | Date as `YYYY-MM-DD`                               |
| `dateFormat`    | `dateFormat "layout" t`                            |
| `slug`          | `slug "Some Title"` → `some-title`                 |
| `title`         | Title-case helper                                  |
| `lower`/`upper` | Case conversion                                    |
| `default`       | `default "fallback" .Vars.x` if `.Vars.x` is empty |
| `env`           | Read an env var                                    |
| `wikilink`      | `wikilink "Foo"` → `[[Foo]]`                       |

The `.Vars` map holds repeatable `--var key=value` pairs.

Example template (`Templates/book.md`):

```text
---
categories:
  - "[[Books]]"
author: "{{ index .Vars "author" }}"
status: To Read
created: {{ today }}
tags: []
---

# {{ .Title }}

Notes about {{ wikilink .Title }}.
```

Then:

```sh
bn new book "Crafting Interpreters" --var author="Robert Nystrom"
# → References/Books/Crafting Interpreters.md
```

### `bn open` — pick a note with fzf and edit it

```sh
bn open [query] [--print] [-0]
```

Pipes every vault note path into `fzf`; on selection, exec-replaces with
the configured editor (`[open].editor` in config, or `$EDITOR` as fallback).
With `<query>`, fzf opens pre-filtered (no auto-select).
`--print` emits the chosen path instead of invoking the editor — useful for
shell composition. `-0` (with `--print`) NUL-terminates the output.

Examples:

```sh
bn open                          # full picker
bn open dune                     # fzf prefiltered with 'dune'
cd "$(dirname "$(bn open --print)")"
```

### `bn vault` — open the vault root in your editor

```sh
bn vault
```

Exec-replaces with the configured editor opened on the vault root
(`[open].editor` in config, or `$EDITOR` as fallback). Useful for
launching the vault as a project/workspace in editors that treat a
directory argument as a session root (`nvim`, `code`, `subl`, etc.).

### `bn serve` — run the local vault browser

```sh
bn serve [--addr host:port] [--port N] [--no-open]
```

Starts a local HTTP server for vault browsing and lightweight note management:

- `/` Daily review cockpit, note counts, filtered search results, diagnostics, and primary search
- `/n/<note-key>` rendered note view (body, editable frontmatter, backlinks/outgoing, broken affordances)
- `/broken` unresolved wikilink diagnostics with one-click blank stub creation
- `/graph` interactive Cytoscape graph
- `GET /api/history` recent journal entries and `POST /api/undo` for web-originated undo
- `GET /api/review` Daily review queue and `POST /api/review/item` for Done/Snooze/Clear state

The home page starts with Daily review: a health-first queue for frontmatter
parse errors, schema violations, broken wikilink targets, duplicate titles,
orphan notes, and dead-end notes. Review items are grouped by issue and affected
note, sorted by priority, and identified with content-aware hashes over the
issue kind, issue detail, affected relative paths, locations, and file content.
Marking an item Done hides that exact issue until its signature changes; Snooze
hides it until the selected local deadline, either tomorrow or next week; Clear
removes a saved review-state record so the active issue appears again.

Review state is stored in the served vault at `<vault>/.bnotes/review.json`.
It is runtime UI state, not note metadata: it does not edit note frontmatter,
does not write undo journal entries, and is pruned when the underlying issue is
resolved or changes enough to receive a new signature. `GET /api/review`
returns active items plus `active`, `done_hidden`, `snoozed_hidden`, and
`total_generated` counts. `POST /api/review/item` requires CSRF and form fields
`id`, `action=done|snooze|clear`, plus optional `snooze=tomorrow|week`.

The overview search results can select notes for batch lifecycle work: move to
Trash, move to a folder, set/list-edit frontmatter, or unset frontmatter. Bulk
actions require an inline preview first: the server returns a one-use
`preview_id`, counts, destination paths, frontmatter before/after values, move
rewrite counts, and any failures, then the follow-up apply call must reuse that
preview receipt before it expires. Note pages can move the current note to
Trash, edit frontmatter in place, create a blank note for a broken wikilink
target, and open the file in the configured editor for body edits. These web
actions use the same note-management code as the CLI where practical, refresh
the served vault state after success, and return structured JSON for automation.

Trash is a visible vault-root `Trash/` folder. Trashing a note moves the file
there, chooses a non-conflicting destination name, and leaves `Trash/` ignored by
default so trashed notes do not continue to appear in search, graph, or
diagnostics.

Rendered note pages syntax-highlight fenced code blocks that include a language
info string, such as `go`, `sh`, or `json`, on the server. Unlabeled fences
remain plain escaped code blocks. Note bodies also auto-link GFM-style bare URLs
such as `https://example.com`.

Local binaries built with `just build` or `just install` remember this checkout
path. `bn serve` rebuilds web assets there before starting, preferring the local
native `node_modules/esbuild/bin/esbuild` binary so an already-installed checkout
does not need to launch `node` or `npm`. If that binary is missing, it falls back
to `npm run build:web`. Binaries without the local checkout stamp fall back to
detecting a checkout from the current directory, then to embedded assets.

The overview and graph views share the same URL filter parameters for text,
tags, category, status, type, folder, property presence, issue filters
(`broken=1`, `orphan=1`, `dead_end=1`), date/age filters, and link-count
ranges. The facet picker is searchable, count-labeled, keyboard usable, and
writes selected filters into the URL, so filtered views can be shared.

The graph view loads the whole vault by default. Filtering keeps only matching
notes and links between visible notes, while the status line reports the
reduced node and edge counts. Nodes with broken links, orphan status, or
dead-end status are styled distinctly and expose those issues in the node
details panel. Use the Spread slider to rerun the layout from a compact
overview up to a wider graph. Labels default to Auto: small graphs are fully
labeled, dense graphs reveal labels for hovered or selected nodes, nearby
context, and zoomed views, and the All mode is available when every label is
needed. Click a node to lock its details, highlight its one-hop neighborhood,
and enable Open note; click empty graph space to clear the selection.

`--addr` overrides `[serve].addr`. `--port` rewrites only the port on the
resolved address host. `--no-open` forces browser launch off even when
`[serve].open_browser = true`.

The default bind is localhost (`127.0.0.1:8729`). This is single-user local
software; there is no auth layer. All side-effecting web routes use `POST`,
require a per-process CSRF token (`X-BN-CSRF` header or `token` form field),
validate note route keys, and reject paths outside the vault. Overview bulk
automation must `POST /api/notes/bulk/preview` first, then send the returned
one-use `preview_id` to `POST /api/notes/bulk`; missing, stale, mismatched, or
reused previews are rejected with `409`. Preview receipts stay process-local,
expire after five minutes, and never write journal entries by themselves.
Review item state changes use `POST /api/review/item` with the same CSRF token
and only write `<vault>/.bnotes/review.json`.
File-changing web actions write undo journal entries with `bn web ...` command
names; `POST /api/undo` restores the latest entry by default or a selected
entry ID.

Editor opens from the note page use the same editor resolution as CLI open
commands (`[open].editor`, then `$EDITOR`). Successful opens are asynchronous;
if editing the file outside the web UI changes the body, refresh the page
manually to see updates.

Media embeds (`![[image.png]]`) render through `/media/...` only when the file
exists. Missing media embeds remain plain text (they do not redirect to
`/broken`).

### `bn repair` — fix safe schema violations in frontmatter

```sh
bn repair [notes...] [find flags] [--property key] [--dry-run]
```

Inspects frontmatter against the property schema defined in `config.toml` and
applies safe, deterministic fixes. Fix categories:

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

`--property` restricts repair to a single schema property.

Each file is rewritten atomically, but there is no cross-file transaction: if a
mid-apply error occurs, prior writes stand — use `git diff` to recover.

`--dry-run` previews changes in the same unified-diff format as `bn set
--dry-run` (`--- path`, then `- key: old` / `+ key: new`) without writing.

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
bn relink [notes...] [find flags] [--dry-run]
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

`--dry-run` prints unified-diff-style blocks per changed file (`--- path`, then
`- body[...]` / `+ body[...]` or frontmatter key lines) and skips all writes.

Examples:

```sh
bn relink --dry-run                         # preview only
bn relink                                   # apply safe rewrites
bn relink --folder References               # scoped by folder
bn relink --tag project --broken-links      # scoped by find flags
bn relink Notes/Index.md                    # explicit note
```

### `bn rename` — change a note's title and rewrite wikilinks

```sh
bn rename <note> <new-title> [--dry-run] [--force]
```

`<note>` may be an absolute path, a path with separators, a `.md`-suffixed
filename, or a bare title (resolved case-insensitively against the vault).

Rewrites every `[[wikilink]]` in the vault that pointed at the old title —
including folder-prefixed (`[[Books/Old]]`), aliased (`[[Old|alt]]`), and
heading (`[[Old#Section]]`) forms. Wikilink-typed frontmatter values
(`categories`, `parent`, …) are also rewritten.

Atomicity: each affected file is rewritten via temp-file + rename. There is no
cross-file transaction — if a mid-apply error occurs, prior writes stand. Use
`git diff` to inspect.

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
integer → int, `YYYY-MM-DD` → date, else string). Properties listed in
`wikilink_properties` have both ends wrapped in `[[ ]]` before comparison so
`bn replace categories Books Films` rewrites a stored `[[Books]]` to
`[[Films]]`.

Each file is rewritten atomically, but there is no cross-file transaction: if
an error stops the run mid-way, prior writes stand — use `git diff` to
recover.

`--dry-run` prints the same unified-diff preview as `bn set --dry-run`
(`--- path`, then `- key: old` / `+ key: new`) and skips all writes.

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
bn set <note...> <key[=|+=|-=]value...> [--type date|int|bool|raw] [--dry-run] [-0]
```

Positional args are split: anything containing an operator (`=`, `+=`, `-=`)
is a pair, and all preceding args are note paths. Use `-` as the sole note
arg to read paths from stdin (so it composes with `bn find`).

Operators:

- `key=value` — set the property (overwriting any prior value).
- `key+=value` — append to a list property; no-op if the value is already
  present (the file is not rewritten, so mtime is unchanged).
- `key-=value` — remove a value from a list property; no-op if absent.

`+=` and `-=` require the key to be in `[frontmatter].list_properties`; using
them on any other key is an error. Values are comma-split for list properties
the same way `=` does (`tags+=foo,bar` adds two entries). Operators can be
mixed in one invocation and apply left-to-right.

Type coercion is automatic: `true`/`false` → bool, integer → int, `YYYY-MM-DD`
→ date, else string. Use `--type` to force. Properties listed in
`wikilink_properties` are wrapped in `[[ ]]` on write; for `+=`/`-=` the
wrapping happens before the comparison so `tags+=Books` matches an existing
`[[Books]]` entry.

Frontmatter is rewritten with **alphabetically sorted top-level keys**. Each
file is rewritten atomically, but there is no cross-file transaction: if an
error stops the run mid-way, prior writes stand — use `git diff` to recover.

`--dry-run` prints a unified-diff preview per affected file and skips all
writes. Files with no effective change are omitted from the preview, and
blocks are emitted in stable (sorted) path order. The format is intentionally
distinct from `rename --dry-run` / `move --dry-run`; the two will likely be
harmonized in a follow-up release. The same flag is being rolled out to other
mutating commands.

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
removal in the same unified-diff format as `bn set --dry-run` (one `- key:
value` line per present property) without touching disk; missing properties
remain silent in dry-run mode too.

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

## Shell completion

Cobra ships completion scripts for free:

```sh
bn completion bash   > /etc/bash_completion.d/bn
bn completion zsh    > "${fpath[1]}/_bn"
bn completion fish   > ~/.config/fish/completions/bn.fish
```

For a one-shot zsh session:

```sh
source <(bn completion zsh) && compdef _bn bn
```

## Editor integration

### Neovim

```lua
-- Pick a vault note and open it.
vim.keymap.set("n", "<leader>fb", function()
  local handle = io.popen("bn find -i")
  if handle == nil then return end
  local path = handle:read("*l"); handle:close()
  if path then vim.cmd.edit(vim.fn.fnameescape(path)) end
end)

-- Rename current buffer (also rewrites every wikilink).
vim.keymap.set("n", "<leader>nr", function()
  local current = vim.api.nvim_buf_get_name(0)
  local new = vim.fn.input("New title: ")
  if new == "" then return end
  vim.fn.system({ "bn", "rename", current, new })
  vim.cmd.edit()
end)
```

### VS Code

A `tasks.json` snippet:

```jsonc
{
  "label": "bn: rename current",
  "type": "shell",
  "command": "bn",
  "args": ["rename", "${file}", "${input:newTitle}"],
  "inputs": [{ "id": "newTitle", "type": "promptString", "description": "New title" }]
}
```

## Recipes

```sh
# Bulk-add a category to every uncategorised note under References/Books.
bn find --folder References/Books --missing categories |
  bn set - categories=Books

# Find orphan notes interactively.
bn find --orphan -i

# Sweep stale 'WIP' projects to an Archive folder, preserving wikilinks.
bn find --status WIP --created '>180d' -0 |
  while IFS= read -r -d '' p; do bn move "$p" Archive/Projects; done

# Spot typo'd tags in the long tail, then rewrite every occurrence.
bn tags | tail -20
bn find --tag projetc | bn set - tags=project

# Mark every note linking to a hub note as 'referenced'.
bn backlinks "Project Alpha" | bn set - status=referenced
```

### Refactoring workflows

Preview-first refactors compose `bn find` (scope), `bn replace` (rewrite a
single value), and `bn set --dry-run` (any-shape mutation) so the diff is
visible before any file is touched:

```sh
# Rename the 'WIP' status to 'in-progress' across every project note. Preview
# first, then re-run without --dry-run once the diff looks right.
bn find --tag project --status WIP
bn replace status WIP in-progress --tag project --dry-run
bn replace status WIP in-progress --tag project

# Stage a tag rename without leaving anything half-done: find → preview → apply.
bn find --tag drafts | bn set - tags-=drafts tags+=draft --dry-run
bn find --tag drafts | bn set - tags-=drafts tags+=draft

# Lint → repair cycle: detect violations, preview fixes, apply, verify.
bn lint
bn repair --dry-run
bn repair
bn lint
```

## Undo/history safety workflow (v1)

`bn` keeps a vault-local operation journal at `<vault>/.bnotes/journal/`.

- Journaled commands (apply mode): `new`, `set`, `unset`, `replace`, `rename`,
  `move`, `repair`, `relink`, plus `bn serve` management actions such as
  `bn web trash`, `bn web move`, `bn web frontmatter set`,
  `bn web frontmatter unset`, and broken-link stub creation.
- Snapshot commands `backup` / `restore` are separate from the undo journal and
  do not create journal entries.
- Read-only commands, including `bn serve` bulk previews, never journal.
- Daily review Done/Snooze/Clear state is stored separately in
  `<vault>/.bnotes/review.json`; it is not journaled and does not modify note
  frontmatter.
- Any `--dry-run` invocation never writes journal entries.

Recommended safety workflow:

1. Run `--dry-run` first (for commands that support it).
2. Re-run without `--dry-run` to apply.
3. Inspect `git diff`.
4. If needed, run `bn history`, then `bn undo <id>` (or `bn undo` for latest).
   In `bn serve`, use the Recent changes panel or `POST /api/undo` for the same
   journal entries.
5. Re-check with `git diff` before committing.

v1 limits and non-goals:

- Undo restores whole entries only (no per-file interactive restore).
- No redo stack.
- No auto-merge conflict resolution.
- No cross-machine/distributed journal sync.
- `git` remains the long-term audit/history system.

## Performance

Measured against a 309-note vault on Darwin/arm64:

| Operation                                | Time   |
| ---------------------------------------- | ------ |
| `bn find --tag X`                        | ~10 ms |
| `bn find` (all notes)                    | ~10 ms |
| `bn rename` (single referrer rewritten)  | ~20 ms |
| `bn rename --dry-run` (full vault scan)  | ~10 ms |

Typical run: <50 ms.

## Differences from `vault-cli`

`bn` is a deliberate trim of the previous Python `vault-cli`: the core surface
is the six file-mutating / query commands (`find`, `new`, `rename`, `move`,
`set`, `unset`), with three thin quick-access shortcuts on top (`open`,
`backlinks`, `values` + the `tags` alias). The shortcuts compose with the core
commands rather than introducing new categories of feature — they exist to
make the next pipeline shorter, not to broaden scope. Omitted commands and the
reason each was dropped:

| Command                              | Why dropped                                                              |
| ------------------------------------ | ------------------------------------------------------------------------ |
| `touch`                              | Editors handle file creation; `bn new` covers templated cases.           |
| `validate` / `lint`                  | Vault conventions are loose by design; no schema to validate.            |
| `stats` / `analyze` / `health`       | Reports without an obvious next action.                                  |
| `archive`                            | Composed from `bn move` + `bn set`.                                      |
| `bases`                              | Obsidian-specific; ignored.                                              |
| `ai` / `infer`                       | LLM integration is out of scope for v1.                                  |
| `batch`                              | Compose pipelines with `find` + `xargs` instead.                         |
| `export`                             | The vault is already plain Markdown.                                     |
| `categories` / `links` / `discover`  | Hub-style navigation; the editor's outline + `find --orphan` cover it.   |
| `attachments`                        | Attachments folder is ignored; binary asset management isn't a fit.      |
| `timeline` / `recall`                | `git log` is the canonical long-term history source; `bn history` / `bn undo` are scoped to short-term operational safety. |
| `properties` / `content`             | Subsumed by `bn set` / `bn unset` / `bn find`.                           |
