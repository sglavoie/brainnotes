# Note creation and navigation

## Command reference

### `bn new` — create a note from a template

```sh
bn new <template> <title> [--folder rel] [--var k=v]... [--force] [--edit]
```

Renders `<template>` from `<vault>/Templates/` and writes it to the routed (or
`--folder`) destination as `<title>.md`. Template lookup is case-insensitive
and tries: `<name>`, `<name>.md`, `<name> Template.md`. `--edit` then opens the
created note in `[open].editor`, falling back to `$EDITOR`, as `bn open` does.

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

### `bn capture` — append a timestamped line to the inbox note

```sh
bn capture [text...] [--note rel] [--dry-run]
```

Appends one line to the configured inbox note (`[capture].inbox_note`, default
`Inbox.md`) under the configured heading (`[capture].heading`, default
`## Captures`). Both the note and the heading are created when absent; every
other byte of the note — frontmatter included — is left untouched, so content
above *and* below the capture section stays byte-identical.

The line format is fixed and not configurable:

```text
- YYYY-MM-DD HH:MM <text>
```

Wikilinks you type pass through verbatim: `bn capture "ask [[Bob]] about the
migration"` writes a real graph edge. Positional arguments are joined with a
single space and internal whitespace runs collapse to one space. With no
positional arguments the text is read from stdin, so `pbpaste | bn capture`
works. The `-` stdin sentinel used by the note-path commands is deliberately
*not* accepted here, because capture takes text rather than paths.

`--note <rel>` overrides `[capture].inbox_note` for one invocation (a missing
`.md` extension is added, and the path must stay inside the vault). `--dry-run`
renders the canonical preview format and writes nothing:

```text
--- Inbox.md
body:4:  -> - 2026-08-17 09:30 buy milk
```

A successful capture prints the destination, the exact line written, the journal
entry ID, and the undo caveat:

```sh
$ bn capture "call the dentist"
captured to Inbox.md (created)
- 2026-08-17 09:30 call the dentist
journal_id: 20260817T093000.000000000Z-1a2b3c4d
note: undo restores all of Inbox.md; after later edits to it 'bn undo' refuses, and 'bn undo --force' overwrites those edits
```

**Undo caveat.** Each capture is journaled as a whole-note snapshot of the
inbox, not as a line-level delta, so a restore replaces the entire inbox note.
Ordinary `bn undo <id>` is fail-fast: once the note no longer matches the state
the capture left behind — because you or `bn serve` edited it since — the
restore aborts with no writes and tells you to re-run with `--force`.
`bn undo <id> --force` then restores the whole snapshot, overwriting every later
edit to that note. Undoing the capture that created the inbox deletes the note.

The inbox is the one genuinely hot file in a vault — the CLI, `bn serve`, and
your open editor can all write it. Capture pins its write to the exact revision
it planned against and, unlike every other write in `bn`, re-reads and re-plans
up to three times when it loses that race (safe only because an append is
commutative). If all three attempts lose, it fails with a conflict error rather
than guessing; retry once the other writer is done.

`bn serve` exposes the same primitive as the quick-capture box on the home page,
and `[cockpit].max_captures` turns on the `capture_backlog` advisory rule so a
piling-up inbox shows up in `bn doctor` and the web Daily Fixes panel.

```sh
bn capture "call the dentist"
bn capture "read [[Dune]] after Sapiens"
pbpaste | bn capture
bn capture --dry-run "idea worth checking"
bn capture --note "Scratch.md" "one-off idea"
```

### `bn pin` — keep a note at hand

```sh
bn pin <note> [--dry-run]
```

Appends a note to the pinned list by writing a `pinned` float rank into its
frontmatter: one more than the highest existing rank, so the note lands last.
The note is named by title (case-insensitive) or by path.

State lives in the note, not in a sidecar, so a pin survives `bn rename` and
`bn move` for free and is visible and editable in Obsidian. The rank is always
written as a float, whether or not `brainnotes.toml` declares
`[frontmatter.properties.pinned]`, so pinning works in any vault.

Pinning an already-pinned note is a no-op that leaves its rank alone — including
a hand-written `pinned: true`, which is never overwritten with a rank.
Reordering is web-only (`bn serve` → `/pinned`): dragging writes the midpoint
between two neighbouring ranks, so one drag rewrites exactly one note.

```sh
bn pin "Reading List"
bn pin Projects/Reading\ List.md
bn pin --dry-run "Reading List"
```

### `bn unpin` — drop a note from the pinned list

```sh
bn unpin <note> [--dry-run]
```

Removes the `pinned` property entirely rather than writing `pinned: false`, so
`--has pinned` and `--missing pinned` keep agreeing with `--pinned`. Unpinning a
note that is not pinned is a silent no-op. Both `bn pin` and `bn unpin` are
journaled, so `bn undo` reverts either.

```sh
bn unpin "Reading List"
bn unpin --dry-run "Reading List"
```

### `bn remove` — move a note to Trash

```sh
bn remove [note] [-y | --yes] [--dry-run]
```

Accepts a note title (case-insensitive) or path. Without a note argument, opens
the normal single-note `fzf` picker. On a terminal both forms ask for
confirmation, on stderr:

```text
Move Projects/Ideas.md to Trash? [y/N]
```

Only `y` or `yes` confirms; blank input, `n`, or EOF reports `remove: canceled`,
leaves the vault unchanged, and exits 1. `--yes` (`-y`) skips that prompt.
Without a terminal — a script, a pipe — `bn remove` refuses (exit 2) unless
`--yes` is given, rather than reading an answer from stdin; `bn restore --wipe`
follows the same rule. An empty vault or a picker that selects nothing also
changes nothing and exits 1 (see
[exit status](integrations.md#scripting-exit-status-and-json)).

`--dry-run` prints the move in the shared `--dry-run` format and writes
nothing, without asking:

```text
--- Projects/Ideas.md
path: Projects/Ideas.md -> Trash/Projects/Ideas.md
```

When `Trash/` already holds that path, the preview shows a collision-suffixed
destination whose short ID is chosen afresh by the real move.

Removal is recoverable: the note moves to `<vault>/Trash/`, retaining its
vault-relative folder (for example, `Projects/Ideas.md` becomes
`Trash/Projects/Ideas.md`). Existing files at that destination get a
collision-safe suffix. Backlinks are deliberately not rewritten. The command
prints the source, Trash destination, journal ID, and a `bn undo <id>` recovery
command.

```sh
bn remove
bn remove "Remove me"
bn remove Projects/Ideas.md --yes
bn remove --dry-run "Remove me"
bn remove -y
# restore the printed journal entry:
bn undo 20260817T093000.000000000Z-1a2b3c4d
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

