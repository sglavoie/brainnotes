# Saved views

A saved view is a named query stored in `brainnotes.toml` and run from any
find-scoped command with `--view`. The query language itself is documented in
[Finding notes](commands-query.md#query-expressions--q).

## Command reference

### `bn views` — inspect saved views

```sh
bn views [--json]
bn views show <name>
bn views check
```

A saved view is a named query stored as a `[[views]]` block in
`brainnotes.toml`, run from any find-scoped command with `--view <name>`. Names
are matched case-insensitively, and views are listed in file order. `bn views`
is read-only: it reads the config alone and never walks the vault or builds the
link graph.

| Subcommand | Output |
| --- | --- |
| `bn views` | A `NAME`/`DESCRIPTION`/`QUERY`/`STATUS` table, where `STATUS` is `ok` or `error`. `--json` prints the same columns as `{"name", "description", "query", "status", "error"}` records, `error` set only for a disabled view. |
| `bn views show <name>` | One view's name, description, query, ordering, anchor, and cap. The query is re-rendered canonically — the spelling `--view` actually runs — with a `Stored as:` line added when the stored text differs. |
| `bn views check` | One report per view that no longer compiles, then a non-zero exit. |

`internal/config` cannot validate a saved expression without closing an import
cycle, so its validation is shape-only and a view whose query rotted — a
property it names was renamed, say — still loads. Every surface is required to
render such a view as a **disabled entry carrying its error**, never to drop it
and never to fail the whole config load, since one stale view must not lock you
out of the vault. `bn views check` is how you find out why:

```
$ bn views check
/path/to/vault/brainnotes.toml: view "Stale query"
query: unknown field "statuz"
  statuz:wip
  ^^^^^^
  did you mean: status

/path/to/vault/brainnotes.toml: view "Bad ordering"
view "Bad ordering": sort "relevance" requires relative_to

bn: views check: 2 of 3 saved views failed to compile
```

Not every disabled view renders carets: a query failure does, while an unusable
ordering (`sort = "relevance"` with no `relative_to`, or a `relative_to` with a
non-`relevance` sort) is reported as a one-line message, because there is no
span in the saved query to point at. A view that names a graph field compiles
here without a graph, so `check` cannot catch a wikilink target that no longer
resolves; that error surfaces when the view is actually run.

#### Running a saved view

A view is a complete query shape — query, `sort`, `relative_to`, `limit` — and
`--view` runs all four. Two rules follow.

**A disabled view is refused, never degraded.** If `bn views` reports a view as
`error`, `--view` fails with the same detail rather than falling back to the
query alone; a view that reports as enabled and a view that runs are the same
view.

**Only `bn find` orders and caps its output.** `bn search`, `bn lint`,
`bn graph`, `bn replace`, `bn repair`, `bn relink`, and `bn rename-key` accept
`--view` for its query, but they have nowhere to put an ordering or a cap, so a
view carrying either is **refused** with a message naming the field and pointing
at `bn find --view`:

```
$ bn replace status wip done --view "Recent WIP"
bn: view "Recent WIP" stores limit 2, which replace cannot apply; run it with 'bn find --view "Recent WIP"'
```

Refusing rather than ignoring matters most on the mutators: silently dropping
the cap on `bn replace --view "Top 10"` would have rewritten every match.

On `bn find`, an explicit `--sort` (or `--relative-to`) replaces the view's
ordering wholesale — including an explicit `--sort path`, which is a request for
path order and not an absent flag. The cap has no flag, so it survives an
ordering override: choosing how results are ordered is not a request for more of
them. A view storing no `sort` leaves `bn find --pinned`'s implicit pinned
ordering intact.

Examples:

```sh
bn views
bn views show "Active work"
bn views check                            # usable as a config health gate
bn find --view "Active work"
bn find --view "Active work" --tag urgent # a view ANDs with every other filter
bn find --view "Recent WIP" --sort title  # explicit ordering wins; the cap stays
```
