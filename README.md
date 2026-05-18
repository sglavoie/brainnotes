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
        [-i | --interactive] [-0]
```

Prints matching absolute paths, one per line (or NUL-separated with `-0`).
Pipe into `fzf` with `-i` for an interactive picker.

Filters AND together. Age expressions take the form `>30d`, `<7d`, `=0d`;
`--created` reads the `created` property, `--age` reads `last`.

Examples:

```sh
bn find --tag books                       # all notes tagged 'books'
bn find --category Books --status WIP
bn find --has rating --created '>365d'    # stale ratings
bn find --orphan                          # no inbound wikilinks
bn find --tag drafts -i                   # interactive fzf pick
bn find --tag drafts -0 | xargs -0 nvim   # safe for spaces in paths
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

### `bn set` — write frontmatter properties

```sh
bn set <note...> <key=value...> [--type date|int|bool|raw] [-0]
```

Positional args are split: anything containing `=` is a key=value pair, and
all preceding args are note paths. Use `-` as the sole note arg to read paths
from stdin (so it composes with `bn find`).

Type coercion is automatic: `true`/`false` → bool, integer → int, `YYYY-MM-DD`
→ date, else string. Use `--type` to force. Properties listed in
`list_properties` are comma-split into a YAML list before coercion. Properties
listed in `wikilink_properties` are wrapped in `[[ ]]` on write.

Frontmatter is rewritten with **alphabetically sorted top-level keys**.

Examples:

```sh
bn set References/Books/Dune.md status=Reading rating=7
bn set note.md categories=Books,Personal                    # → wikilink list
bn set note.md created=2026-01-01 --type=date
bn find --tag drafts | bn set - status=done                 # stdin pipeline
```

### `bn unset` — remove frontmatter properties

```sh
bn unset <note> <property...> [-0]
bn unset <note...> -- <property...>
```

Without `--`, the first positional is the note (or `-` for stdin) and the rest
are property names. To pass multiple notes, use `--` to separate them from
the property list.

Removing a missing property is a silent no-op.

Examples:

```sh
bn unset note.md rating
bn unset a.md b.md -- title status                          # multi-note
bn find --has rating | bn unset - rating                    # bulk wipe
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

`bn` is a deliberate trim of the previous Python `vault-cli` to six commands
that justify themselves in daily use. Omitted commands and the reason each
was dropped:

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
