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

# Revisit the current note through related notes and an inspectable context packet.
bn related Notes/Current.md -i
bn context Notes/Current.md --purpose questions

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
# editor = "code --reuse-window"

[related]
# Frontmatter properties whose values contribute to related-note ranking.
# Set to [] to disable metadata scoring while keeping text, graph, and recall.
metadata_properties = ["categories", "tags", "topics", "type", "parent", "area"]

[brief]
# Vault-relative folder prefixes excluded from Daily Brief recommendations.
exclude_folders = []
# Frontmatter type values excluded from Daily Brief recommendations.
exclude_types = []

[agent]
# Optional local research command as a literal argv array. No shell is used.
# The complete context packet is written to stdin; empty disables execution.
command = []
timeout_seconds = 60

[serve]
# HTTP listen address for `bn serve`.
addr = "127.0.0.1:8729"
# Open your browser on startup. Set false for headless use.
open_browser = true

[cockpit]
# Tuning for the Daily Fix Cockpit (`bn doctor` and the web cockpit).
# Rule kinds to hide from the queue entirely.
disabled_rules = []
# Rule kinds to surface first, overriding the built-in ordering.
priority = []
# A long-tail task flags a rare frontmatter value only when the property also
# has a dominant sibling value to consolidate into.
# How many qualifying values per property become long-tail tasks (rarest first).
long_tail_limit = 5
# Highest per-value count that still counts as rare.
long_tail_max_count = 2
# A value is only flagged when some sibling value is at least this many times
# as frequent (the dominant head to merge into).
long_tail_head_factor = 3
# Frontmatter properties to exclude from the long-tail rule entirely (open
# vocabularies like tags/topics where a long tail is natural, not a typo).
long_tail_ignore_properties = []
# Maximum tasks the daily cockpit view surfaces at once (bn doctor --daily,
# GET /api/cockpit?view=daily). Counts still report the full totals.
daily_limit = 10
# Frontmatter property that records when a note was last reviewed.
review_date_property = "reviewed"
# Status values whose notes must carry a review date. Empty disables the rule.
review_statuses = []
# Status values counted as work-in-progress. Empty disables the WIP-limit rule.
wip_statuses = []
# Maximum concurrent WIP notes before the WIP-limit rule flags overflow.
max_wip = 3
# Age in days after which a note's status is treated as stale.
stale_status_days = 30
# Frontmatter type/kind values identifying hub/MOC notes. Empty disables the
# hub-synthesis rule.
hub_type_values = []
# Headings that, when present but empty in a hub note, mark it as needing
# synthesis. Empty disables the hub-synthesis rule.
hub_empty_headings = []
# Enable the sensitive-content scanner (off by default). Matched values are
# never surfaced unredacted — only a line number and detector category.
sensitive_scan = false
# Vault-relative path prefixes the sensitive-content scanner skips.
sensitive_ignore_paths = []
# Regular-expression patterns whose matches the scanner treats as allowed.
sensitive_ignore_patterns = []
# Legacy daily-review-only command override (split on whitespace, no shell).
# When empty, the top-level [agent] command and timeout are used.
agent_command = ""
# Timeout for the legacy override.
agent_timeout_seconds = 60

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
        [--min-links n] [--max-links n] [--dead-end]
        [--broken-links [--show-broken | --list-broken-targets]]
        [-i | --interactive] [-0]
```

Prints matching absolute paths, one per line (or NUL-separated with `-0`).
Pipe into `fzf` with `-i` for an interactive picker.

Filters AND together. Age expressions take the form `>30d`, `<7d`, `=0d`;
`--created` reads the `created` property, `--age` reads `last`.

`--dead-end` selects notes with no resolved outgoing links. To mark an
intentional terminal note without changing its raw graph degree, set the
reserved frontmatter property to the YAML boolean `false`:

```yaml
dead_end: false
```

Only boolean `false` is an exemption; a missing property, `true`, null, or a
quoted string such as `"false"` retains normal dead-end classification. The
exemption affects only dead-end classification, so the note still has
out-degree zero and still matches `--max-links 0`. Preview the change with:

```sh
bn set CLAUDE.md dead_end=false --dry-run
```

In `bn serve`, a dead-end card in Health → Daily Fixes offers **Mark as
intentional dead end**. The button immediately writes the same boolean property,
records the change in the undo journal, and removes the finding from dead-end
surfaces without changing the note's graph degree.

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
bn find --dead-end                        # no resolved outgoing links, unless exempt
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

### `bn related` — rank contextual connections

```sh
bn related <note>
  [--mode balanced|rediscover|connect]
  [--limit N]
  [--paths [--null] | --json]
  [-i | --interactive [--open]]

bn related feedback <source> <candidate>
  --state useful|dismissed|later|clear
  [--until YYYY-MM-DD]
```

`bn related` builds an in-memory view of the current vault on every invocation;
there is no persistent index, database, model download, or service. `<note>` may
be a case-insensitive filename stem, a vault-relative path, or an absolute path.
Ambiguous stems fail with the candidate paths so a relative path can disambiguate
them. Paths and tokens are normalized to Unicode NFC for stable cross-platform
identity.

Ranking combines four explainable signals with fixed v1 weights:

| Signal | Weight | Meaning |
| --- | ---: | --- |
| Text | 0.40 | Weighted title, heading, and prose TF-IDF similarity. |
| Graph | 0.30 | Direct links or shared-neighbor/directed two-hop proximity. |
| Metadata | 0.20 | Overlap in configured `[related].metadata_properties`, downweighted when common. |
| Recall | 0.10 | Creation age plus useful/dismissed/snoozed/open-history state. |

`balanced` returns every candidate with a meaningful signal. `rediscover`
restricts results to direct or directed two-hop notes that are old or have not
been opened recently. `connect` excludes direct links and favors text- or
metadata-supported unlinked notes. Stable vault-relative path order breaks
score ties. Human output includes rank, relationship, path, and up to three
reasons; `--json` exposes the stable weights, component scores, relationship,
bridge, and reasons. `--paths` prints absolute paths for pipelines, with NUL
separation under `--null`.

Interactive mode feeds the already-ranked rows to fzf. A successful selection
prints its absolute path, records `last_opened_at`, and optionally opens it in
the configured editor with `--open`. `--open` therefore requires `-i`.

Feedback is directed from source to candidate:

| State | Effect |
| --- | --- |
| `useful` | Adds a positive recall signal. |
| `dismissed` | Excludes the pair until cleared or replaced. |
| `later --until YYYY-MM-DD` | Excludes the pair until the future local date. |
| `clear` | Removes rating/snooze state while preserving open history. |

Feedback and successful interactive selections are the only writes in this
workflow. They are stored atomically in `<vault>/.bnotes/recall.json`, outside
Markdown and the undo journal. The versioned sidecar contains normalized source
and candidate paths, hashed pair IDs, ratings/snoozes, and timestamps—never note
bodies or excerpts. Removed-note and expired-snooze records are ignored by
queries and pruned on the next explicit state write. Corrupt, invalid,
unsupported, or symlinked state fails closed and is never silently overwritten.

Examples:

```sh
bn related "Project Alpha"
bn related Notes/Current.md --mode connect --json
bn related Notes/Current.md --paths --null | xargs -0 -n1 printf '%s\n'
bn related Notes/Current.md -i --open
bn related feedback Notes/Current.md Notes/Evidence.md --state useful
bn related feedback Notes/Current.md Notes/Detour.md --state later --until 2026-08-01
```

### `bn context` — export a bounded research packet

```sh
bn context <note>
  [--related N]
  [--max-bytes N]
  [--format markdown|json]
  [--purpose synthesis|contradictions|questions]
  [--agent]
```

`bn context` turns the same balanced ranking into deterministic, inspectable
research input. The packet contains the source note's complete body, configured
metadata, ranked identities and reasons, and exact bounded Markdown excerpts
from up to five related notes by default. The manifest names every source span
and records the final serialized byte count. Markdown and JSON select identical
notes and excerpts; only their framing and byte counts differ.

The defaults are `--related 5`, `--max-bytes 65536`, `--format markdown`, and
`--purpose synthesis`. `--related 0` exports only the source. Related excerpts
are truncated at Unicode rune boundaries, but the source body is never
truncated: if the complete source and required framing cannot fit, the command
fails. The command reads recall feedback so dismissed or currently snoozed
pairs stay out of the packet, but it never updates open history or state.

`--agent` requires Markdown and a non-empty top-level `[agent].command`. `bn`
launches that argv directly without a shell, writes the byte-identical preview
packet to stdin, applies `agent.timeout_seconds`, and prints stdout verbatim.
It does not select a provider, persist the response, create an undo entry, or
modify Markdown. Preview the packet without `--agent` whenever you want to
inspect exactly what the command will receive.

Privacy boundary: ordinary `related` and `context` queries stay local and
read-only. A context packet intentionally includes the complete source body and
selected related excerpts; an agent command receives exactly those bytes and
may itself transmit them depending on how that executable is configured. `bn`
adds no network client or provider dependency. Keep sensitive notes out of the
packet, lower `--related`, or omit `--agent` when that boundary is inappropriate.

Examples:

```sh
bn context Notes/Current.md
bn context Notes/Current.md --related 3 --purpose contradictions
bn context Notes/Current.md --format json > /tmp/current-context.json
bn context Notes/Current.md --purpose questions --agent
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

- `/` Daily Brief cards (Continue, Rediscover, Connect), rolling seven-day activity, compact health snapshot, recent notes, and primary search
- `/library` faceted search, bulk note operations, change history, and recent notes
- `/health` Daily Fixes, vault metrics, and full diagnostics from the shared snapshot
- `/n/<note-key>` rendered note view (body, editable frontmatter, backlinks/outgoing, broken affordances)
- `/broken` unresolved wikilink diagnostics with one-click blank stub creation
- `/graph` interactive Cytoscape graph
- `GET /api/history` recent journal entries and `POST /api/undo` for web-originated undo
- `POST /api/brief/view` idempotent per-day visit recording that refreshes the rolling activity summary
- `POST /api/brief/item` card actions (`open` and `another`) addressed by server-issued `card_id` only
- `POST /api/brief/insight` optional local AI insight for one card, returning raw output and separately rendered safe HTML
- `GET /api/cockpit` hygiene queue (add `?view=daily` for the capped daily view) and `POST /api/cockpit/item` for Done/Snooze/Clear triage
- `POST /api/fixes/preview` then `POST /api/fixes/apply` to preview and apply a single task's fix
- `GET /api/daily-review/context` read-only redacted daily-review snapshot and `POST /api/daily-review/agent` optional local AI review

Resilient note loading (server-only): `bn serve` bounds each note read to a
fixed 2-second timeout so a single stalled file cannot hang a page load. A note
that exceeds that timeout is quarantined for the rest of the `bn serve` process
lifetime — it is skipped without a re-read on every later request and excluded
from recent notes, search, the graph, the cockpit queue, and the health counts.
Skipped notes surface in Health diagnostics and are summarized on the home page,
so the omission is visible rather than silent. Quarantine is in-memory
only and never touches disk; **restart** the server to clear it and retry a
previously skipped note. This bound is server-only: `bn doctor` and every other
CLI command keep their original
full-vault loading semantics and read every note without a per-note timeout.

Daily Brief cards stay stable for the local day (including restarts): Continue
shows the most recently edited substantive note, Rediscover surfaces the
longest-unseen substantive note, and Connect suggests an unlinked note related
to Continue using the same `ModeConnect` ranking engine. `Another` on Continue
atomically replaces Continue and Connect together; `Another` on Rediscover or
Connect replaces only that slot. A successful `open` records activity only after
the editor process starts. Card actions are path-safe and keyed by server-issued
IDs; callers never submit filesystem paths.

Daily Brief runtime state lives in `<vault>/.bnotes/brief.json` and stores only
local-date selections, content-addressed card IDs, exposure/open timestamps, and
visit/open activity windows. It never stores note bodies, AI request payloads,
or AI responses. Reads are fail-closed for corrupt, unsupported, or unsafe
(e.g. symlinked) state files; successful writes are atomic and sidecar-only, so
brief state updates never create undo journal entries and never mutate Markdown.

`POST /api/brief/insight` is optional and uses only the top-level `[agent]`
command/timeout. It sends the selected card's bounded context packet to that
local command's stdin and returns the raw stdout plus separately rendered safe
HTML. The output is never persisted to `.bnotes/brief.json`, undo history, or
Markdown files.

No telemetry, background schedulers, reminders, or analytics pipelines are
introduced: activity metrics are local-only and computed from the brief sidecar.

The Health page starts with Daily Fixes: the same hygiene queue as `bn doctor`
(over the notes the server successfully loaded — a note quarantined by the
2-second timeout above is absent from the web queue but still scanned by the CLI),
covering frontmatter parse errors, schema violations, broken wikilink targets,
duplicate titles, orphan notes, dead-end notes, long-tail property values, and —
when their `[cockpit]` keys are configured — the advisory rules `sensitive_content`,
`missing_review_date`, `stale_status`, `wip_limit`, and `hub_needs_synthesis`.
Tasks are grouped by issue and affected note, sorted by priority, and identified
with content-aware hashes over the issue kind, detail, affected relative paths,
locations, and file content. Marking a task Done hides that exact issue until its
signature changes; Snooze hides it until the selected local deadline, either
tomorrow or next week; Clear returns a suppressed task to the active queue.

The advisory rules are triage-only: they never carry a fix action, so the web UI
suppresses the fix button for them (driven by each task's `triage_only` flag) and
they are resolved by editing notes yourself and then marking Done or Snooze.
`sensitive_content` rows show only a redacted `line N: <detector category>`
location — the matched secret value is never sent to the browser, the queue JSON,
or any review surface.

`GET /api/cockpit?view=daily` caps the workflow-filtered queue to the top
`cockpit.daily_limit` tasks (default `10`) while leaving the counts as the full
totals, so the daily panel shows the highest-priority findings without losing the
active/hidden tallies. `GET /api/daily-review/context` returns the same
deterministic, redacted snapshot as `bn doctor --review-context` — per-kind
groups and ordered items with server-redacted locations, no file hashes, and no
raw bodies — so the CLI and web review surfaces cannot drift for the notes the
server loaded. The one intended difference is coverage, not format: a note
quarantined by the server-only 2-second load timeout is absent from the web
snapshot while `bn doctor` still includes it from its full-vault scan.

`POST /api/daily-review/agent` is an opt-in, local-only AI review. A non-empty
legacy `cockpit.agent_command` remains the daily-review-only override (split on
whitespace, no shell); otherwise the route uses the shared top-level
`[agent].command` argv and timeout. It feeds exactly the redacted daily-review
context on stdin and returns stdout verbatim as `{ok, enabled: true, output}`.
When neither command is configured, the route returns a clean disabled response
(`{ok: true, enabled: false}`) with HTTP `200`, not an error—no AI provider
dependency is added. Failures and timeouts return `internal_error` (HTTP `500`).
Because only the redacted daily-review context crosses this boundary, raw note
bodies, file hashes, and unredacted sensitive values never reach this agent
route. The route is CSRF-protected like every other side-effecting web action.

Cockpit workflow state is stored in the served vault at
`<vault>/.bnotes/cockpit.json`, migrated automatically from readable records in
the legacy `<vault>/.bnotes/review.json` on first load. It is runtime UI state,
not note metadata: it does not edit note frontmatter, does not write undo
journal entries, and is pruned when the underlying issue is resolved or changes
enough to receive a new signature. `GET /api/cockpit` returns active tasks plus
`active`, `done_hidden`, `snoozed_hidden`, and `total_generated` counts.
`POST /api/cockpit/item` requires CSRF and form fields `id`,
`action=mark_done|snooze|clear` (`done` is accepted as an alias for
`mark_done`), plus optional `snooze=tomorrow|week`.

Fixable tasks advertise a fix action (schema repair, deterministic relink, or
blank-stub creation for a broken target). Applying a fix is preview-gated:
`POST /api/fixes/preview` returns the planned per-note changes plus a one-use
`preview_id`, and `POST /api/fixes/apply` must echo that receipt back before any
file is touched. A receipt that is missing, expired, replayed, or whose task
changed on disk since the preview is rejected with `409` so a stale fix never
lands. Both endpoints require CSRF and the task `id` and `action`; a successful
apply writes an undo journal entry named `bn cockpit fix`.

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
such as `https://example.com`. Beyond that, rendering supports the GitHub
Flavored Markdown extensions — tables, `~~strikethrough~~`, and task lists
(`- [ ]` / `- [x]`, shown as read-only checkboxes since `bn serve` does not edit
note bodies) — plus footnotes, definition lists, and typographic punctuation
(straight quotes become curly, `---` becomes an em dash, `--` an en dash, and
`...` an ellipsis). Wide tables scroll horizontally so they stay readable on
narrow viewports, and wikilinks resolve inside table cells just as they do in
body text. Inline and fenced code are left untouched by these transforms.

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
Cockpit fixes follow the same preview-then-apply contract via
`POST /api/fixes/preview` and `POST /api/fixes/apply`. Cockpit triage
(Done/Snooze/Clear) uses `POST /api/cockpit/item` with the same CSRF token and
only writes `<vault>/.bnotes/cockpit.json`.
File-changing web actions write undo journal entries with `bn web ...` command
names (cockpit fixes use `bn cockpit fix`); `POST /api/undo` restores the latest
entry by default or a selected entry ID.

Editor opens from note pages and Daily Brief cards use the same editor resolution as CLI open
commands (`[open].editor`, then `$EDITOR`). Successful opens are asynchronous;
if editing the file outside the web UI changes the body, refresh the page
manually to see updates.

Media embeds (`![[image.png]]`) render through `/media/...` only when the file
exists. Missing media embeds remain plain text (they do not redirect to
`/broken`).

Two-week Daily Brief dogfood readout (local state only):

1. Count distinct visit days in the last 14 days from `.bnotes/brief.json` `visits`.
2. Compute average visits per week as `visit_days / 2`.
3. Compute open-day rate as `days_with_open / visit_days` (where opens come from `opens` and are grouped by local date).
4. Success threshold: at least 4 visits per week and opens on at least 50% of visited days.

### `bn doctor` — show the vault hygiene queue (Daily Fix Cockpit)

```sh
bn doctor [--json] [--daily] [--review-context] [--folder path]
```

Reports the current hygiene queue for the vault. It is read-only and never
modifies files; the exit code is always `0` for a successful scan, and an empty
queue means the vault is clean. The queue is the CLI face of the same Daily Fix
Cockpit the web UI serves (`bn serve`), so both surfaces show identical tasks
for every note both surfaces loaded. `bn doctor` always reads the full vault
with no per-note timeout; the one intended exception is that `bn serve` bounds
each note read to a fixed 2-second timeout and quarantines a timed-out note (it
appears in the server's "Skipped notes" diagnostic and is absent from the web
queue until you **restart** the server), whereas `bn doctor` still includes that
note. See `bn serve` above for the full description of this server-only bound.

Detected rule kinds:

| Kind | What it flags |
| --- | --- |
| `frontmatter_error` | frontmatter that does not parse as YAML |
| `schema_violation` | frontmatter values that violate `[frontmatter.schema]` |
| `broken_target` | wikilinks whose target note does not exist |
| `duplicate_title` | two or more notes sharing the same title |
| `orphan_note` | notes with no inbound links from other notes |
| `dead_end_note` | notes with no resolved outgoing links, except notes with boolean `dead_end: false` |
| `long_tail_value` | rare frontmatter values that a dominant sibling value can absorb (e.g. a `status` typo to merge into the common value) |
| `sensitive_content` | notes whose body matches a built-in secret detector (AWS/GitHub/Slack/Google keys, JWTs, private-key blocks, secret assignments); off unless `sensitive_scan = true` |
| `missing_review_date` | notes in a `review_statuses` status that lack the `review_date_property` |
| `stale_status` | notes whose status has not changed in more than `stale_status_days` days |
| `wip_limit` | more concurrent `wip_statuses` notes than `max_wip` allows |
| `hub_needs_synthesis` | hub/MOC notes (by `hub_type_values`) with a `hub_empty_headings` heading present but empty |

The last five kinds are **advisory / triage-only**: they carry no automatic
fix and are resolved by editing notes yourself, then marking the task done or
snoozed. Each is disabled by default until you configure its `[cockpit]` keys
(an empty gating list, or `sensitive_scan = false`, means the rule is off).
`sensitive_content` never surfaces a matched value — a task shows only the
line number and detector category, so secrets are never echoed into the queue,
JSON, or web UI.

Issues are grouped into tasks by kind and detail, content-addressed over the
issue and every affected note's path, location, and file content, and sorted by
priority. Each task lists the fix actions available for it. Tasks you mark done
or snooze through the cockpit (web UI or the state file) are hidden from the
default view; the footer reports how many were suppressed.

`--json` emits the full queue — every task, its affected notes, available
actions, and the active/done/snoozed/total counts — in a stable schema
identical to the web `GET /api/cockpit` payload. `--daily` caps the surfaced
tasks to the top `cockpit.daily_limit` (default `10`) after workflow-state
filtering, mirroring the web daily view (`GET /api/cockpit?view=daily`); the
counts still report the full active/hidden totals, so `active` can exceed the
number of tasks shown. `--review-context` emits a deterministic, redacted JSON
snapshot of that daily queue for agent review — it implies `--daily`, omits
file hashes and raw bodies, keeps only the server-redacted per-note locations,
and never invokes any AI command. `--folder <path>` keeps only tasks that touch
at least one note under a vault-relative folder; other `find` dimensions do not
map onto tasks that group many notes and are intentionally not offered.

Workflow state (done/snoozed) lives in `<vault>/.bnotes/cockpit.json`, migrated
automatically from readable records in the legacy `<vault>/.bnotes/review.json`
on first load. Tune the queue with the `[cockpit]` config block:
`disabled_rules` hides rule kinds entirely, `priority` surfaces chosen kinds
first, and three keys tune `long_tail_value`: a value is flagged only when it is
rare — its count is at most `long_tail_max_count` (default `2`) — *and* the
property has a dominant sibling value at least `long_tail_head_factor` (default
`3`) times as frequent to consolidate into. This keeps genuine typos/variants
(e.g. `status = Maintenance` when `status = Completed` dominates) while dropping
both common head values and unique-per-note fields (`email`, `phone`, `url`) that
have no dominant sibling. Qualifying values are then capped by `long_tail_limit`
(default `5`, rarest first) so a property with many variants cannot flood the
queue. `long_tail_ignore_properties` (default empty) excludes named properties
from the rule entirely — set it to open-vocabulary fields (e.g. `tags`,
`topics`, `created`) where a long tail is expected and never a typo, keeping the
rule focused on enum-like properties such as `status`, `kind`, and `type`.

```sh
bn doctor                                   # human-readable queue
bn doctor --json                            # machine-readable queue
bn doctor --daily                           # top cockpit.daily_limit tasks only
bn doctor --review-context                  # redacted daily snapshot for review
bn doctor --folder Projects                 # only tasks touching Projects/
```

For a daily review workflow, pipe the redacted context into a local agent:

```sh
bn doctor --review-context | my-review-agent   # no secrets/bodies leave the vault
```

The same redacted snapshot backs the web review surface
(`GET /api/daily-review/context`) and the optional AI route
(`POST /api/daily-review/agent`); see `bn serve` below.

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
integer → int, `YYYY-MM-DD` → date, else string). Properties listed in
`wikilink_properties` have both ends wrapped in `[[ ]]` before comparison so
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

`--dry-run` prints the shared preview format and skips all writes: a
`--- <relpath>` header per changed note, then one `frontmatter[<key>]: <old> ->
<new>` line per changed property. Files with no effective change are omitted,
and blocks are emitted in stable (sorted) path order. Every mutating command —
`set`, `unset`, `replace`, `repair`, `relink`, `rename`, and `move` — now emits
this same canonical format (the path-moving commands add `path:` and
`body:<line>:` lines; see `rename`/`move`/`relink` above).

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

A `.vscode/tasks.json` snippet that can pick a related note, inspect a context
packet or optional local research brief, and rename the current note:

```jsonc
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "bn: related current",
      "type": "process",
      "command": "bn",
      "args": ["related", "${file}", "-i", "--open"],
      "presentation": { "reveal": "always", "focus": true }
    },
    {
      "label": "bn: context current",
      "type": "process",
      "command": "bn",
      "args": ["context", "${file}", "--purpose", "questions"],
      "presentation": { "reveal": "always", "focus": false }
    },
    {
      "label": "bn: research brief current",
      "type": "process",
      "command": "bn",
      "args": ["context", "${file}", "--purpose", "questions", "--agent"],
      "presentation": { "reveal": "always", "focus": false }
    },
    {
      "label": "bn: rename current",
      "type": "process",
      "command": "bn",
      "args": ["rename", "${file}", "${input:newTitle}"]
    }
  ],
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

### Contextual research workflow

Move from the note in front of you to explainable candidates, durable feedback,
an inspectable packet, and an optional local brief:

```sh
source_note="Notes/Current.md"

# Inspect ranked evidence or choose one in fzf.
bn related "$source_note" --json
bn related "$source_note" -i --open

# Teach future rankings about one directed source/candidate pair.
bn related feedback "$source_note" "Notes/Useful Evidence.md" --state useful
bn related feedback "$source_note" "Notes/False Lead.md" --state dismissed

# Review exact agent input first; run the configured argv only when ready.
bn context "$source_note" --purpose contradictions > /tmp/context.md
bn context "$source_note" --purpose contradictions --agent
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
  `bn web frontmatter unset`, broken-link stub creation, and cockpit fixes
  (`bn cockpit fix`). `bn doctor` is read-only and never journals.
- Snapshot commands `backup` / `restore` are separate from the undo journal and
  do not create journal entries.
- Read-only commands, including ordinary `bn related`, `bn context`,
  `bn context --agent`, `bn doctor`, and `bn serve` bulk/fix previews, never
  journal. Applying a cockpit fix (`POST /api/fixes/apply`) does journal, under
  the command name `bn cockpit fix`.
- Daily Fix Cockpit Done/Snooze/Clear triage state is stored separately in
  `<vault>/.bnotes/cockpit.json` (migrated from the legacy
  `<vault>/.bnotes/review.json`); it is not journaled and does not modify note
  frontmatter.
- Related-note feedback and successful interactive selections are stored
  separately in `<vault>/.bnotes/recall.json`; they are not journaled and never
  modify Markdown.
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

Contextual-recall core benchmarks were measured on 2026-07-13 with an Apple M1
Max (`darwin/arm64`, Go 1.26.4). Values are the median `ns/op` from five runs;
portable tests deliberately do not enforce wall-clock thresholds.

```sh
go test ./internal/recall -run '^$' \
  -bench 'Benchmark(BuildRankerAndRank|ContextBundle)$' -benchmem -count=5
```

| Synthetic corpus | Build ranker + balanced query | Build bounded context bundle | Target |
| ---: | ---: | ---: | ---: |
| 500 notes | ~3.7 ms | ~6.4 ms | <150 ms |
| 5,000 notes | ~36.9 ms | ~36.4 ms | <1 s |

These benchmarks start from deterministic in-memory document models: ranking
includes a fresh lexical/hybrid index build and query, while context generation
includes its excerpt lexical index, selection, dual-format byte-budget checks,
and canonical rendering. CLI runs additionally walk and parse the vault on each
invocation; there is intentionally no persistent index to warm or invalidate.

Existing command measurements against a 309-note vault on Darwin/arm64:

| Operation                                | Time   |
| ---------------------------------------- | ------ |
| `bn find --tag X`                        | ~10 ms |
| `bn find` (all notes)                    | ~10 ms |
| `bn rename` (single referrer rewritten)  | ~20 ms |
| `bn rename --dry-run` (full vault scan)  | ~10 ms |

Typical non-recall run: <50 ms.

## Differences from `vault-cli`

`bn` is a deliberate trim of the previous Python `vault-cli`: focused commands
compose over a plain-Markdown vault instead of recreating a general notes
platform. Contextual recall follows the same rule—it is an in-memory,
explainable query plus optional explicit local process execution, not an index,
provider integration, or automatic content mutation. Omitted commands and the
reason each was dropped:

| Command                              | Why dropped                                                              |
| ------------------------------------ | ------------------------------------------------------------------------ |
| `touch`                              | Editors handle file creation; `bn new` covers templated cases.           |
| `validate` / `lint`                  | Vault conventions are loose by design; no schema to validate.            |
| `stats` / `analyze` / `health`       | Reports without an obvious next action.                                  |
| `archive`                            | Composed from `bn move` + `bn set`.                                      |
| `bases`                              | Obsidian-specific; ignored.                                              |
| `ai` / `infer`                       | No implicit/provider AI command; `bn context --agent` runs only an explicitly configured local argv with inspectable stdin. |
| `batch`                              | Compose pipelines with `find` + `xargs` instead.                         |
| `export`                             | The vault is already plain Markdown.                                     |
| `categories` / `links` / `discover`  | Hub-style navigation; the editor's outline + `find --orphan` cover it.   |
| `attachments`                        | Attachments folder is ignored; binary asset management isn't a fit.      |
| `timeline`                           | `git log` is the canonical long-term history source; recall feedback is ranking state, not content history. |
| `properties` / `content`             | Subsumed by `bn set` / `bn unset` / `bn find`.                           |
