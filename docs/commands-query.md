# Finding notes

`bn find` and the query expression language it takes. Saved views are in
[Saved views](commands-views.md); the commands that read the link graph are in
[Research commands](commands-research.md).

## Command reference

### `bn find` — query notes by frontmatter

```sh
bn find [--tag t]... [--category c]... [--status s] [--type t]
        [--folder rel] [--has key] [--missing key]
        [--created EXPR] [--age EXPR] [--orphan]
        [--min-links n] [--max-links n] [--dead-end]
        [--broken-links [--show-broken | --list-broken-targets]]
        [--pinned] [-q EXPR | --query EXPR] [--view name]
        [--sort KEY] [--relative-to note]
        [-i | --interactive] [-0]
```

Prints matching absolute paths, one per line (or NUL-separated with `-0`).
Pipe into `fzf` with `-i` for an interactive picker.

A note whose frontmatter is not valid YAML does not stop the read. `bn find`,
`bn search`, `bn links`, `bn backlinks`, `bn values`, `bn graph`,
`bn related`, and `bn context` leave it out of their results and print one
line per skipped note on stderr, so stdout stays a clean result:

```
find: warning: skipped Inbox/draft.md: frontmatter: parse yaml: yaml: line 1: did not find expected ',' or ']'
```

A skipped note is absent from the link graph too, so a link to it can read as
broken until it is fixed. `bn lint` reports it as a violation instead, and the
commands that change notes still refuse to run until it parses.

Filters AND together. Age expressions take the form `>30d`, `<7d`, `=0d`;
`--created` reads the `created` property, `--age` reads `last`.

#### Query expressions (`-q`)

`-q` takes a query expression, which reaches filters the flags cannot spell.
The flags, `-q`, and `--view` all AND together, so each one narrows the result
further. `--view` names a saved view — a `[[views]]` block in `brainnotes.toml`
— and contributes that view's query. On `bn find` it also applies the view's
stored `sort`, `relative_to`, and `limit`; see
[Running a saved view](commands-views.md#running-a-saved-view).

| Form | Example | Meaning |
| --- | --- | --- |
| Boolean composition | `-q 'tag:books and not folder:Archive'` | `and`, `or`, `not`, grouped with `(` `)`; `not` binds tighter than `and`, which binds tighter than `or`. Juxtaposition means `and`. |
| Free text | `-q 'activation energy'` | A bare word or quoted phrase searches title and body. |
| Flag-shaped fields | `-q 'orphan:true'` | Every find flag has a field spelling: `tag`, `category`, `status`, `type`, `has`, `missing`, `age`, `folder`, `orphan`, `pinned`, `broken-links`, `dead-end`, `backlinks`, `links`. |
| Declared property | `-q 'rating>=4'`, `-q 'due<today'` | Compared according to its declared `[frontmatter.properties]` type. Ordering is legal only for `date`, `datetime`, `int`, and `float`. |
| Undeclared property | `-q 'prop:mykey=value'` | The `prop:` escape hatch; a bare unknown name is an error with did-you-mean instead. |
| Link graph | `-q 'near:[[Deep Work]]<=2'` | Also `links-to:`, `linked-from:`, `path-to:`, `degree>=5`, `cluster-size:1`. |

The reserved vocabulary is twenty-one field names. Any other bare name is an
error with did-you-mean candidates, unless it is a property you declared in
`[frontmatter.properties]`.

| Field | Selects |
| --- | --- |
| `tag` | Notes carrying a tag. |
| `category` | Notes in a frontmatter category. |
| `status` | Notes with a status value. |
| `type` | Notes with a type (or kind) value. |
| `has` | Notes that store a frontmatter property (`has:rating`). |
| `missing` | Notes that omit one (`missing:rating`; exactly `not has:rating`). |
| `folder` | Notes under a vault folder. |
| `age` | Notes by date: `age>90d`, or another date property as its argument, `age:created<30d`. |
| `prop` | Any frontmatter key, declared or not: `prop:rating>3`. |
| `pinned` | Pinned notes, spelled `pinned:true`. |
| `orphan` | Notes nothing links to, spelled `orphan:true`. |
| `dead-end` | Notes that link nowhere, spelled `dead-end:true`. |
| `broken-links` | Notes with an unresolved wikilink, spelled `broken-links:true`. |
| `backlinks` | Incoming link count: `backlinks>3`. |
| `links` | Outgoing link count: `links<2`. |
| `links-to` | Notes linking to a target: `links-to:[[Deep Work]]`. |
| `linked-from` | Notes a target links to. |
| `near` | Notes within N hops of a target: `near:[[Hub]]<=2`. |
| `path-to` | Notes connected to a target at any distance. |
| `degree` | Distinct linked neighbours, in either direction: `degree>=5`. |
| `cluster-size` | The size of the note's connected component: `cluster-size:1`. |

The last six read the link graph, so a query naming one costs a graph build;
the others do not. A bare word is always free text, which is why the
flag-shaped fields are written `orphan:true` rather than bare `orphan` — typing
`orphan` alone searches for that word. `!=` negates a comparison, and both `:`
and `=` mean equality.

Every graph field names a *note*, so its target is resolved before the query
runs: a target that resolves to nothing is a compile error with did-you-mean
candidates rather than an empty result set indistinguishable from a correct
one. Targets may be written as a wikilink (`links-to:[[Deep Work]]`) or as a
quoted title (`links-to:"Deep Work"`).

A malformed expression is rejected before anything is read, with the offending
span marked by carets:

```
$ bn find -q 'status:wip and (rating>3'
query: unclosed group opened at column 16: expected ")"
  status:wip and (rating>3
                          ^
```

Two quoting rules follow from the lexer: an RFC3339 literal must be quoted
(`reviewed="2026-05-19T10:30:00Z"`), because its `:` separators would otherwise
end the word, and so must a negative relative offset (`due>"-7d"`), because a
leading `-` is negation.

`-q` and `--view` are find filter flags, so like every other one they cannot be
combined with positional note arguments on `bn search`, `bn replace`,
`bn repair`, `bn relink`, and `bn rename-key`.

Saved views are defined as `[[views]]` blocks in `brainnotes.toml` (see
[Configuration](configuration.md)) and inspected with [`bn views`](commands-views.md#bn-views--inspect-saved-views).

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

A **Long-tail value** card similarly offers **Remove property from affected
notes**. It immediately removes the flagged property from every note grouped in
that task and records the batch as one undo-journal entry.

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

`--pinned` selects notes carrying a `pinned` property and prints them in pinned
order — ascending rank first, then unranked pins, each group tie-broken by path
— instead of the default path sort. Only an absent key and the YAML boolean
`false` count as unpinned; a hand-written `pinned: true` or `pinned: "high"` is
pinned but has no rank, so it sorts after every ranked pin. Ranks are written by
`bn pin` and reordered in `bn serve`.

#### Ordering (`--sort`)

Results are path-ordered by default. `--sort` changes the order and never
changes which notes match.

| Key | Orders by |
| --- | --- |
| `path` | Vault path (the default). |
| `title` | Note title, case-insensitively. |
| `match` | Title hits for a saved view’s `search` text first, then title, folder and path. Without saved text, title/folder/path order. |
| `modified` | The `last` property. |
| `created` | The `created` property. |
| `backlinks` | Inbound links from other notes. |
| `degree` | Distinct linked neighbours, in either direction. |
| `pinned` | Pinned order, as `--pinned` prints it. |
| `relevance` | Relatedness to `--relative-to`, most related first. |

Direction is given either as a sign prefix (`--sort -modified`) or as a suffix
(`--sort modified:desc`), but not both at once, even when the two agree. A note
with no value for the key — an undated note under `--sort modified`, an
unpinned one under `--sort pinned` — sorts last in *both* directions, so
`--sort -modified` means "newest first" rather than "undated first". Notes that
tie are ordered by path.

`--sort relevance` requires `--relative-to`, which names the anchor note by
title or path; `--relative-to` without `--sort relevance` is likewise an error
rather than a silently ignored flag. Ranking uses the same text, graph,
metadata, and recall signals as `bn related`, but selection stays
authoritative: the anchor's own recall feedback reorders the matches and never
removes one, so a dismissed or snoozed note that the query matched is still
printed.

`relevance` is most related first, and `-relevance` reverses the *scores* and
nothing else — it is the same rule the other keys follow. Concretely:

- The anchor scores above everything, being the thing the rest are measured
  against, so an anchor the query matched leads `relevance` and trails
  `-relevance`.
- A note the recall corpus does not index has no score and sorts last in both
  directions, exactly like an undated note under `--sort modified`. It is never
  dropped: ordering does not change which notes match.
- Notes that tie — equal scores, or two unscored notes — stay in ascending path
  order in both directions, because the tie-break is a stability device rather
  than part of the requested ordering.

The web `/api/search` executes this definition through the same function, so a
saved view carrying `sort = "-relevance"` and a `limit` keeps the same top N in
`bn find --view` and in the browser.

`bn find` only ever reads recall state. It records no open history and creates
no `.bnotes/recall.json`; writes remain limited to `bn related -i`,
`bn related feedback`, and the web related actions.

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
bn find --pinned                          # pinned notes, in pinned order
bn find --status wip --sort -modified     # newest work in progress first
bn find --tag books --sort relevance --relative-to "Deep Work"
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
