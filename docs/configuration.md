# Configuration


Configuration lives at the root of the vault, in `brainnotes.toml` and an
optional `brainnotes.local.toml` beside it. `brainnotes.toml` is what marks the
directory as a vault root; run `bn init` to create it. There is no external or
user-level config file.

Vault root resolution, in order:

1. `--vault <path>` flag
2. `BNOTES_VAULT` environment variable
3. An upward search from the current directory for the nearest `brainnotes.toml`

All three require the directory to actually contain `brainnotes.toml`. Run
`bn config` to see the resolved vault root, the config path, and which of the
three resolved it; `bn config edit` opens that file in `$EDITOR`.

A vault with no `brainnotes.local.toml` is the normal shape, not a degraded
one — every setting below can be written in `brainnotes.toml` alone and that is
what happens until you have a reason to split. [The machine-local
overlay](#the-machine-local-overlay) explains when you do.

### Migrating from an external configuration file

Earlier versions read configuration from outside the vault. That model is gone,
with no fallback and no automatic migration. If you used it, migrate by hand:

| Removed | Replacement |
| --- | --- |
| `--config <path>` flag | `--vault <path>`, pointing at the directory that holds `brainnotes.toml` |
| `BNOTES_CONFIG` environment variable | `BNOTES_VAULT`, set to the vault root |
| XDG / home discovery (`~/.config/bnotes/config.toml`) | `brainnotes.toml` at the vault root |
| `vault = "..."` key inside the config file | The location of `brainnotes.toml` itself; the key is no longer read |

Migration steps:

1. Run `bn init <vault>` to write a commented starter `brainnotes.toml` at the
   vault root. It refuses to overwrite an existing file, so this is safe to run
   against a vault you are unsure about.
2. Copy your old settings into it, dropping the `vault` key.
3. Rewrite each frontmatter property under `[frontmatter.properties.<key>]`
   using the unified `type` / `items` / `required` / `allowed` schema below.
   There are no implicit defaults: a property gets list semantics, wikilink
   wrapping, or validation only when it is declared.
4. Delete the old external file and unset `BNOTES_CONFIG`.
5. Run `bn config` to confirm the resolved vault root and config path, then
   `bn lint` to see which notes the declarations now flag.

`brainnotes.toml` lives at the vault root, is meant to be committed, and is the
marker that makes the directory a vault.

### The machine-local overlay

`brainnotes.local.toml`, optional and beside `brainnotes.toml`, describes **this
machine** rather than the vault. Which keys belong to which file, how enrollment
changes the rules, and how `bn config migrate` repairs a misplaced key are
covered in [Machine-local configuration](configuration-overlay.md).

### Schema

Every key is optional.

```toml
# Extra vault-relative paths to skip during Walk; merged with hard-coded
# defaults: .git, .obsidian, Attachments, Archive, Trash. The configured
# [templates].dir is also skipped automatically.
ignore = []

[templates]
dir = "Templates"                          # relative to vault
default_extension = ".md"

# Every frontmatter property is declared once, under its own section. There
# are no implicit defaults: a property has list semantics, wikilink wrapping,
# and validation only if it is declared here.
#
#   type      one of string, wikilink, date, datetime, int, float, bool, list
#   items     required when type = "list"; the kind of every element
#             (any type except "list" — lists do not nest)
#   required  when true, `bn lint` reports the property as missing
#   allowed   optional strict allow-list of values
#
# [frontmatter.properties.status]
# type     = "string"
# required = true
# allowed  = ["WIP", "Done", "Archived"]

[frontmatter.properties.categories]
type  = "list"
items = "wikilink"

[frontmatter.properties.tags]
type  = "list"
items = "string"

[frontmatter.properties.topics]
type  = "list"
items = "string"

[frontmatter.properties.org]
type  = "list"
items = "wikilink"

[frontmatter.properties.type]
type  = "list"
items = "string"

[frontmatter.properties.parent]
type = "wikilink"

[frontmatter.properties.area]
type = "wikilink"

# Ordering key for pinned notes: lower ranks come first, and a drag in the web
# UI writes the midpoint between two neighbours. Declared here as documentation
# only — pinning writes this type explicitly, so it works without this block.
[frontmatter.properties.pinned]
type = "float"

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

[capture]
# Vault-relative note that `bn capture` and the web quick-capture box append to.
# Created when absent. A missing '.md' extension is added; an absolute path or
# one escaping the vault falls back to the default. A note inside [templates].dir
# or an ignored path is a configuration error.
inbox_note = "Inbox.md"
# ATX heading captures are appended under, created at the end of the note when
# absent. One to six '#' characters followed by a title.
heading = "## Captures"

[attachments]
# Vault-relative directory files uploaded from the web editor are filed in.
# Must be relative, must not escape the vault, and must not be hidden: an
# attachment under a dot-path would be on disk, outside the content history,
# and unservable. A name colliding with one already claimed or already in the
# vault gets a suffixed one, decided while the upload is still private.
dir = "attachments"
# Per-file upload limit in bytes. Configurable *downward* from the built-in
# 25 MiB ceiling and never upward: the ceiling is also the transport bound the
# server applies before a byte is written, so a larger number here would be a
# limit the request never reaches.
max_file_bytes = 26214400

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
# Unprocessed captures under [capture].heading before the capture-backlog rule
# fires. 0 (the default) disables the rule.
max_captures = 0
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

# Saved views: named query expressions, run with `bn find --view "<name>"`,
# from any other find-scoped command, or from the web nav. Repeat the block
# once per view; the list is ordered and presented in file order. Every key
# except `name` and `query` is optional.
[[views]]
name        = "Active work"
query       = "status:wip and not folder:Archive"
sort        = "-modified"
limit       = 20
description = "What I am actually working on"

# Routing for `bn new <kind> "Title"`. Folder is vault-relative.
[new.routes]
book    = "References/Books"
person  = "People"
goal    = "Goals"
project = "Projects"
daily   = "Daily"

[autosave]
# Hard floor between two commits the external file watcher makes on its own.
# A Go duration between "30s" and "24h" inclusive, or "off". Absent means "5m".
# Explicit `bn`, web, and sidecar changes commit immediately either way.
min_interval = "5m"
```

Explicit wikilinks in any frontmatter property participate in the note graph,
backlinks, broken-link checks, related-note scoring, exports, and web views.
Both quoted YAML strings (`related: "[[People]]"`) and Obsidian's common
unquoted form (`related: [[People]]` or `- [[People]]`) are supported. Plain
metadata such as `categories: People` or `tags: Podcasts` remains metadata and
does not create a graph edge. A `wikilink`-typed property definition controls
automatic wrapping during writes and repairs; it is not required for
discovering or rewriting an already-explicit `[[wikilink]]`.

Default ignore entries (always applied): `.git`, `.obsidian`, `Attachments`,
`Archive`, `Trash`. The configured `[templates].dir` (default `Templates`) is also
skipped automatically, since template files contain unrendered placeholders
that aren't valid YAML.

### Saved views

A saved view is a named query expression stored as a `[[views]]` block. It is
an **array of tables**, not a `[views.<name>]` table: the list is ordered, and
`bn views` and the web nav present views in file order.

| Key | Meaning |
| --- | --- |
| `name` | Required. How `--view` and `bn views` address the view, matched case-insensitively and unique under that folding. Unicode letters and digits plus space, `.`, `_`, and `-`, never leading or trailing. |
| `query` | Required. The query expression, in the syntax `bn find -q` takes. |
| `search` | Optional literal text matched against title/body and ANDed with `query`. Browser saves preserve the text box here. |
| `sort` | Optional ordering in the spelling `--sort` accepts (`modified`, `-modified`, `title:desc`, …). `match` preserves browser best-match ordering using `search`. |
| `relative_to` | Optional anchor note for a `relevance` sort. It is its own key because a sort spec's text form does not carry it. |
| `limit` | Optional result cap. `0` (the default) means no cap. |
| `description` | Optional one-line summary shown by `bn views` and the web nav. |

Validation at load time is deliberately **shape-only** — a name, a non-empty
query, a non-negative limit — and never checks that the query still means
something. `internal/config` is the bottom of the package layering and cannot
import the query language to parse it without closing an import cycle. The
consequence is intentional: a view whose query rotted (a property it names was
renamed, a field was removed) still loads, so one stale view can never make the
vault unopenable. Instead it is reported everywhere as a **disabled entry
carrying its error** — a `STATUS` of `error` in `bn views`, a failure in
`bn views check`, a disabled item in the web nav. Run
[`bn views check`](commands-views.md#bn-views--inspect-saved-views) to see why.

`bn init` does not write a starter `[[views]]` block; add one by hand, or save
the current query from the Library page of `bn serve`, which patches this file
for you after showing the exact diff.

### The autosave floor

`[autosave].min_interval` is a floor on how often the vault's content history
may gain a commit that *nobody asked for* — a snapshot of a change made outside
BrainNotes, by Obsidian, `vim`, a sync client, or a script. It is the only
safety timing a vault configures, and it is the one that is a matter of taste
rather than of correctness: it says how much history you want, not how quickly
the vault reacts to a change.

| | |
| --- | --- |
| Default | `5m`, applied when the key is absent |
| Range | `30s` to `24h`, inclusive |
| Disabled | `off`, the only non-duration value, in lower case |
| Normalization | Go duration syntax; `300s` and `5m0s` are read and printed as `5m`, and a value `bn config set` writes is written in that spelling |

The lower bound sits above the watcher's fixed 30-second quiet period, so a
floor can never be shorter than the readiness it defers. `Off` and `OFF` are
rejected by name rather than guessed at, because a value meaning "stop
committing on your own" is not a place to be lenient. A value outside the range
or in a spelling that does not parse is a load error: `bn` refuses to run
against the file, and `bn-vault-guard` refuses to start on it.

What the floor does **not** touch, at any value including `off`:

- Explicit BrainNotes mutations. Every `bn` command, web write, `bn undo`, and
  durable sidecar update still commits its own paths before it returns, and
  never waits for or resets the floor.
- Deletion protection. A tracked file that disappears with no undo-journal
  record still blocks the vault on the poll that noticed it.
- Pushing, which is scheduled independently of committing.
- The five-second scan, health reporting, and the log.

Read [Vault Safety](operations/vault-safety.md#immediate-versus-scheduled-snapshots)
for the schedule the floor participates in, what changing it does to a running
guard, and how the health check reports a vault that is waiting on it.

#### Reading and writing it from a script

`autosave.min_interval` is the one configuration key with a scriptable
read/write surface. Every other key is edited in the file.

```sh
bn config get   autosave.min_interval
bn config set   autosave.min_interval VALUE [--dry-run]
bn config unset autosave.min_interval [--dry-run]
```

`get` prints the **effective** value and a newline, normalized: `5m` whether the
file says `5m`, `300s`, or nothing at all. It therefore cannot distinguish an
explicit `5m` from an absent key, which is deliberate — what a script wants to
know is which floor is in force.

`set` names a **policy**, not a spelling. It changes the file only when the
floor it asks for is not the one already in force, and then it rewrites exactly
that one assignment — leaving every other byte, including the comments around it
and on it, where it was — creating the `[autosave]` table at the end of the file
if it does not exist yet. What it writes is the canonical spelling of the new
value. What it does *not* do is rewrite a file that already means what was
asked: `bn config set autosave.min_interval 5m` records nothing against a file
storing `5m`, against a file storing `300s`, and against a file with no
assignment at all, because `bn config get` answers `5m` in all three cases. A
script may therefore assert the floor it wants on every run and produce a commit
only on the run that moves it.

"Already means" is decided by reading the file the way `bn` loads it, so it
covers every way TOML can write the same string — `'5m'`, `"""5m"""`, and
`"5\u006d"` name the 5m floor as much as `"5m"` does, and none of them is
rewritten by a `set` that asks for it. A value the loader would *refuse* — a
number, a list, a duration outside the bounds — is no policy at all, so `set`
rewrites it rather than comparing against it. A value written across several
lines is rewritten whole, so what replaces it is one canonical assignment.

`unset` names an **assignment**: it removes the key so the built-in `5m` default
applies again, which is how an explicit floor is taken back out of the file even
when it happens to equal the default. Unsetting a key that is not assigned
changes nothing and records nothing.

Both commands write **the file that owns the key**, and both read the pair to
decide what is in force. `autosave.min_interval` is portable, so that file is
`brainnotes.toml`; a key the overlay assigns anyway makes the write a refusal
rather than a silent no-change (see [the machine-local
overlay](#the-machine-local-overlay)). The precondition covers the pair too: the
file being patched is pinned to the exact bytes the patch was computed from, and
the other layer is pinned to the state the decision was made against — including
its *absence*, so an overlay created between the read and the commit is a
conflict rather than a write made under a configuration nothing ever read.

A change through either command is an ordinary BrainNotes mutation — one
transaction pinned to the bytes the patch was computed from, one undo-journal
entry, one immediate commit — so `bn undo` reverts a floor change like any
other. A semantic no-op and a `--dry-run` create no transaction, no journal
entry, and no commit; `--dry-run` prints the same diff the apply would write.

```text
$ bn config get autosave.min_interval
5m

$ bn config set autosave.min_interval 30m --dry-run
--- brainnotes.toml
- 151: min_interval = "5m"
+ 151: min_interval = "30m"

$ bn config set autosave.min_interval 15s
bn: config: autosave.min_interval "15s" must be between 30s and 24h inclusive
```

The starter config ships the `[autosave]` table with its key commented out, so
`set` fills the assignment in beside the comment. A `brainnotes.toml` written
before the key existed has no such table, and gets one appended:

```text
$ bn config set autosave.min_interval 30m --dry-run
--- brainnotes.toml
+ 206:
+ 207: [autosave]
+ 208: min_interval = "30m"
```


