# Recall and context boundaries

Companion to `CLAUDE.md`; read before changing `bn related`, `bn context`,
`bn find --sort relevance`, `internal/recall`, or the recall sidecar.

- **Recall read/write boundary**: ordinary `bn related`, every `bn context` form, `bn context --agent`, and `bn find --sort relevance` never write recall state, the undo journal, or Markdown. Only successful `related -i` selection, `related feedback`, and CSRF-protected `POST /api/related/item` actions write `<vault>/.bnotes/recall.json`; that sidecar is runtime state and is not recoverable through `bn undo`. No related workflow writes Markdown or creates an undo-journal entry. `bn find` stays on the read side by construction: `internal/cli/recall_corpus.go` hands the ranker `recallSetup.signals()`, which is typed `recall.RecallSignalSource` and therefore has no `RecordOpened` to call.
- **Ranking a chosen set**: a caller that has already selected its notes ranks with `Ranker.RankSelected`, never `Ranker.Rank`. Selection is authoritative — mode eligibility and dismiss/snooze suppression must not drop a chosen note — so `Rank` would silently delete query matches and make an ordering change *which* notes come back. `bn find --sort relevance` and the synthesis supporters in `internal/web/synthesis_api.go` are both on this path; `bn related` uses `Rank` because it is discovery, where suppression is the point.
- **Pinned recall clock**: every recall-reading command reads the wall clock exactly once, through `recallNow`, and threads that instant into both the state store (`recall.WithClock`) and `RankOptions.Now`. `internal/cli/recall_corpus.go` owns the pattern. Ranking recency, snooze expiry, and any write must agree on "now", and pinning the clock is also what makes a relevance-sorted query reproducible byte for byte under a test clock.
- **Context privacy boundary**: a context packet intentionally contains the complete source body plus selected exact excerpts. `--agent` receives exactly the inspectable Markdown bytes and prints stdout verbatim; the configured executable may transmit them, but `bn` itself adds no provider or network client. Do not reuse the web daily-review claim that bodies are redacted—the two agent inputs are intentionally different.
- **Context selection invariant**: Markdown and JSON must select the same related notes and source spans even though JSON escaping can be larger. Budget candidate changes against both serializers, never truncate the source body, and keep all excerpts tied to exact `Document.Blocks` locations.
- **Recall state recovery**: missing state is a valid empty read and is created lazily; missing-note records and expired snoozes remain untouched by reads and are pruned on the next explicit write. Never auto-repair or overwrite corrupt, unsupported-version, invalid-record, or symlinked state.

## Browser reference and connection previews

`GET /api/note/peek` remains read-only. Alongside its short excerpt it returns up
to twelve plain-text passages, bounded to 8,000 Unicode characters in total.
An optional served `context` note selects up to two prose passages mentioning
its title, including passages beyond the note opening. Short matches bring the
nearest substantive prose block (within six blocks) into the same preview, in
document order; the original 8,000-character bound still applies. The response names
the served context title so the client can highlight exact text safely. If none
match, the opening passages remain available. These are lexical matches, not
generated inferences.
References load neither attachments nor agents and write no recall or Brief state.

The connection explorer is shared by the Brief, Related Notes, and Graph. Its
writing action reads `/new?source=…&source=…`, whose page validates both sources
against the served snapshot and prepares folder-qualified wikilinks. The client
embeds that ordinary form beneath the passages, keeps its validated source prefix
separate from the thought input, and reveals naming controls when Save thought is
chosen. Nothing is created until the ordinary New note form is submitted. The full
body and creation receipt use the same tab-local recovery key as the standalone
form, scoped to the vault and source pair.
Reference selection and recent switcher destinations store only bounded route
identities/titles, scoped to the vault and browser tab; they never alter ranking.

## Connection evidence and passage thoughts

Ranking version 2 weights each indexed shared neighbor by `1 / log2(degree)`
(with degree at least two), sums those weights as `overlap`, and maps the sum to
`0.6 * overlap / (0.4 + 0.6 * overlap)`. Direct links still score one. Among eligible
directed bridges (or shared neighbors when there is no directed bridge), lower
degree precedes the stable path tie-break. Excluded documents cannot be bridges;
this prevents the configured capture inbox from supplying Brief evidence. Saved
daily cards remain stable until Another or a new day selects a replacement.
The Brief shows a plain relationship description; detailed scoring reasons stay
under Why this note. Related cards likewise keep graph reasons in their disclosure.

Passage associations are a read-only interpretation of the generated Markdown
shape: optional leading title, `From [[source]]`, literal escaped blockquote, and
thought text. Only a unique source resolved in the served snapshot is accepted.
At most 50 matching backlinks are rendered, and bodies over 128 KiB or quotations
over 8,000 characters are skipped. Client anchoring normalizes whitespace and NFC,
excludes controls/other annotations, and requires a unique quotation match. Missing
or ambiguous matches stay in a visible fallback section. Confirmed creation emits
an in-page event for immediate feedback; reload derives associations from notes,
not local storage. No new mutation route or source-note write is introduced.
