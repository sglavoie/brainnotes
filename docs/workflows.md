# Workflows and operational notes

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

- Journaled commands (apply mode): `new`, `capture`, `set`, `unset`, `replace`,
  `rename`, `move`, `repair`, `relink`, plus `bn serve` management actions such as
  `bn web trash`, `bn web move`, `bn web frontmatter set`,
  `bn web frontmatter unset`, `bn web capture`, broken-link stub creation, and
  cockpit fixes (`bn cockpit fix`). `bn doctor` is read-only and never journals.
- A capture entry snapshots the whole inbox note, so a restore replaces every
  byte of that note. Ordinary `bn undo` refuses once the inbox changed after the
  capture; `bn undo --force` restores the snapshot and overwrites those later
  edits.
- Property workbench writes journal under their own command names:
  `bn serve property set`, `bn serve property unset`,
  `bn serve property replace value`, `bn serve property list add`,
  `bn serve property rename`, `bn serve property repair`,
  `bn serve property schema upsert`, and `bn serve property schema delete`. The
  two schema entries restore `brainnotes.toml` rather than notes.
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
and canonical rendering. CLI runs additionally load the vault. Recall builds its ranking index in memory;
the separate note-byte cache in `.bnotes/index/` is regenerable.

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
reason each remains omitted:

| Command                              | Why dropped                                                              |
| ------------------------------------ | ------------------------------------------------------------------------ |
| `touch`                              | Editors handle file creation; `bn new` covers templated cases.           |
| `stats` / `analyze`                  | Use `bn doctor` and browser Health for actionable diagnostics.            |
| `archive`                            | Composed from `bn move` + `bn set`.                                      |
| `bases`                              | Obsidian-specific; ignored.                                              |
| `ai` / `infer`                       | No implicit/provider AI command; `bn context --agent` runs only an explicitly configured local argv with inspectable stdin. |
| `batch`                              | Compose pipelines with `find` + `xargs` instead.                         |
| `export`                             | The vault is already plain Markdown.                                     |
| `categories` / `discover`  | Hub-style navigation; the editor's outline + `find --orphan` cover it.   |
| `attachments`                        | Attachments folder is ignored; binary asset management isn't a fit.      |
| `timeline`                           | `git log` is the canonical long-term history source; recall feedback is ranking state, not content history. |
| `properties` / `content`             | Subsumed by `bn set` / `bn unset` / `bn find`.                           |

