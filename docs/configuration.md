# Configuration


Configuration lives in a single `brainnotes.toml` at the root of the vault, and
that file is what marks the directory as a vault root. Run `bn init` to create
it. There is no external or user-level config file.

Vault root resolution, in order:

1. `--vault <path>` flag
2. `BNOTES_VAULT` environment variable
3. An upward search from the current directory for the nearest `brainnotes.toml`

All three require the directory to actually contain `brainnotes.toml`. Run
`bn config` to see the resolved vault root, the config path, and which of the
three resolved it; `bn config edit` opens that file in `$EDITOR`.

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

One vault, one file: `brainnotes.toml` lives at the vault root, is meant to be
committed, and is the marker that makes the directory a vault.

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
| `sort` | Optional ordering in the spelling `--sort` accepts (`modified`, `-modified`, `title:desc`, …). |
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
[`bn views check`](commands-query.md#bn-views--inspect-saved-views) to see why.

`bn init` does not write a starter `[[views]]` block; add one by hand, or save
the current query from the Library page of `bn serve`, which patches this file
for you after showing the exact diff.


