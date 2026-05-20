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
# defaults: .git, .obsidian, Attachments, Archive. The configured
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

# Routing for `bn new <kind> "Title"`. Folder is vault-relative.
[new.routes]
book    = "References/Books"
person  = "People"
goal    = "Goals"
project = "Projects"
daily   = "Daily"
```

Default ignore entries (always applied): `.git`, `.obsidian`, `Attachments`,
`Archive`. The configured `[templates].dir` (default `Templates`) is also
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

List all missing targets and create stub notes for each:

```sh
bn find --broken-links --list-broken-targets | xargs -I{} bn new "{}"
```

Re-run `bn find --broken-links` afterward to verify zero residue.

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
`$EDITOR <path>`. With `<query>`, fzf opens pre-filtered (no auto-select).
`--print` emits the chosen path instead of invoking the editor — useful for
shell composition. `-0` (with `--print`) NUL-terminates the output.

Examples:

```sh
bn open                          # full picker
bn open dune                     # fzf prefiltered with 'dune'
cd "$(dirname "$(bn open --print)")"
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
```

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
| `search`                             | `ripgrep` and editor search already excel at this.                       |
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
| `history` / `timeline` / `recall`    | `git log` is the canonical history source.                               |
| `properties` / `content`             | Subsumed by `bn set` / `bn unset` / `bn find`.                           |
