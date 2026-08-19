# Maintenance and bulk-edit commands

## Command reference

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
| `schema_violation` | frontmatter values that violate `[frontmatter.properties]` |
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
| `capture_backlog` | more unprocessed captures under `[capture].heading` in the inbox note than `max_captures` allows; off unless `max_captures` is set |

The last six kinds are **advisory / triage-only**: they carry no automatic
fix and are resolved by editing notes yourself, then marking the task done or
snoozed. Each is disabled by default until you configure its `[cockpit]` keys
(an empty gating list, or `sensitive_scan = false`, means the rule is off).
`sensitive_content` never surfaces a matched value — a task shows only the
line number and detector category, so secrets are never echoed into the queue,
JSON, or web UI. `capture_backlog` counts the bullets under `[capture].heading`
with the same scanner `bn capture` writes through, and its signature includes the
inbox note's content: the task re-signs on every capture, so a snooze lasts only
until the next one. That churn is intentional — a backing-up inbox should come
back — but it does mean Snooze behaves differently here than on the other rules.

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


