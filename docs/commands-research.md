# Research commands

The commands that read note bodies and the link graph. `bn search` and
`bn graph` accept the find-scoping flags documented in
[Finding notes](commands-query.md), including `--view`, which names a view from
[Saved views](commands-views.md). `bn backlinks`, `bn links`, `bn related` and
`bn context` start from one target note instead and take no scoping flags.

## Command reference

### `bn search` — search note bodies

```sh
bn search [pattern] [note...] [find-flags]
          [-e | --regex] [--case-sensitive]
          [-C N | --context N] [--count] [--open] [--json]
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
| JSON | `--json` | An array of `{path, abs_path, title, count, matches: [{line, text}]}`, in path order. |

Like grep, `bn search` exits 1 when nothing matches — in every mode, including
`--open` (which then opens nothing) and `--json` (which still prints `[]`) — and
when an `-i` picker selects nothing. See
[exit status](integrations.md#scripting-exit-status-and-json). `--json` cannot be
combined with `-i`, `--open`, `-0`, `--count` or `--context`; its records already
carry each note's count and matching lines, with `line` the 1-based file line
`--context` prints.

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
bn backlinks <note> [-0] [-v|--verbose] [--json]
```

`<note>` is either a bare title (case-insensitive stem lookup) or a path/glob.
Prints the absolute paths of every note whose body or frontmatter contains a
`[[wikilink]]` to the target, one per line (or NUL-separated with `-0`). Links
inside fenced, indented, or inline code are not counted.

With `--verbose`, each occurrence is printed on its own line as
`<path>:<line>\t<snippet>` for body links, or `<path>:fm:<key>\t<value>` for
frontmatter links. Output is tab-separated so it pipes cleanly into
`awk -F'\t'`. Snippets are trimmed and truncated to 120 characters with an
`…` ellipsis. `--verbose` is the root flag, so `-v` works too; it cannot be
combined with `-0`.

`--json` prints the same notes as an array of `{path, abs_path, title}` records
(`path` vault-relative), `[]` when nothing links to the target. It cannot be
combined with `-0` or `--verbose`.

Examples:

```sh
bn backlinks "Crafting Interpreters"
bn backlinks References/Books/Dune.md
bn backlinks Dune | xargs bn set status=referenced
bn backlinks Dune --verbose | awk -F'\t' '{print $1}'
```

### `bn links` — list the notes a target links to

```sh
bn links [note] [-0] [-v|--verbose] [--json]
```

The outgoing counterpart of `bn backlinks`. `<note>` is either a bare title
(case-insensitive stem lookup) or a path/glob; with no argument, an fzf picker
chooses the target interactively. Prints the absolute path of every note the
target references via a resolved `[[wikilink]]`, one per line, sorted (or
NUL-separated with `-0`). Only links whose target resolves to a note in the
vault are listed — unresolved ones are `bn find --broken-links` territory —
and links inside fenced, indented, or inline code are not counted.

`--verbose` prints one line per occurrence instead, in the same tab-separated
shape `bn backlinks --verbose` uses: `<path>:<line>\t<snippet>` for body links
and `<path>:fm:<key>\t<value>` for frontmatter links, frontmatter first. `-v`
is its short form, and it cannot be combined with `-0`. `--json` prints the
same `{path, abs_path, title}` records as `bn backlinks --json`.

Examples:

```sh
bn links "Deep Work"
bn links Notes/Current.md -0 | xargs -0 -n1 basename
bn links Notes/Current.md --verbose | awk -F'\t' '{print $1}'
bn links                                  # pick the target with fzf
```

### `bn graph` — export the link graph

```sh
bn graph [--format dot|mermaid|code] [find-flags] [-q EXPR] [--view name]
```

Exports the vault's wikilink graph, or the subgraph selected by the find
filters, to stdout. With no filters the whole vault is exported; with filters,
the selected notes become the node set and only edges between them are drawn,
so `bn graph --tag project -q 'near:[[Deep Work]]<=2'` exports exactly the
neighbourhood you can already select with `bn find`.

| `--format` | Output |
| --- | --- |
| `dot` (default) | Graphviz DOT, for `dot -Tsvg`. |
| `mermaid` | A Mermaid flowchart, for a Markdown viewer that renders it. |
| `code` | One line per edge with absolute `file:line` references, which are cmd-clickable in most editor terminals. |

`bn graph` takes no positional note arguments; scope it with the find flags,
`-q`, or `--view`. For an interactive view of the same graph, use the `/graph`
page of `bn serve`.

Examples:

```sh
bn graph | dot -Tsvg -o vault.svg
bn graph --format mermaid --folder Projects
bn graph --format code -q 'orphan:true or dead-end:true'
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

The Related Notes explorer on `bn serve` note pages uses this same ranker, mode
semantics, directed feedback, and sidecar. Its three lazy, independent panels
show five results at a time up to twenty, but deliberately expose only excerpts,
relationships, optional bridges, and plain-language reasons—not numeric scores
or weights. Browser and editor opens share the same directed open history; Clear
removes only rating/snooze state and preserves that history.

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
  [--format markdown|json | --json]
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
`--purpose synthesis`. `--json` is shorthand for `--format json`. `--related 0` exports only the source. Related excerpts
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
