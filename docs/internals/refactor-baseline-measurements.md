# Refactoring baseline: denominator, measurements, coverage

Part of the [refactoring baseline](refactor-baseline.md) (sections 1–4).

## 1. Denominator

`.refactorignore` at the repo root is the source of record for the exclusion
set. `scripts/metrics.py` parses it; it never hard-codes the patterns.

|          | Files | LOC     |
| -------- | ----- | ------- |
| Tracked  | 1163  | —       |
| **REAL** | 1093  | 213,900 |
| Excluded | 70    | —       |

| Language   | Files | LOC     | Source | Test   |
| ---------- | ----- | ------- | ------ | ------ |
| Go         | 773   | 159,624 | 68,882 | 90,742 |
| TypeScript | 196   | 37,096  | 23,844 | 13,252 |
| CSS        | 42    | 6,868   | 6,868  | 0      |
| Markdown   | 23    | 4,037   | 4,037  | 0      |
| Python     | 16    | 3,111   | 3,111  | 0      |
| HTML       | 13    | 2,085   | 2,085  | 0      |
| Other      | 30    | 1,079   | —      | —      |

### The 70 excluded files

| Pattern                                       | Files |
| --------------------------------------------- | ----- |
| `internal/web/static/app.js`                   | 1     |
| `internal/web/static/app.css`                  | 1     |
| `internal/web/static/*.min.js`, `*.vendor.txt` | 8     |
| `internal/template/unicode_*`                  | 11    |
| `internal/web/static_src/globals.d.ts`         | 1     |
| `**/testdata/**` ∪ `*.golden`                  | 47    |
| `package-lock.json`                            | 1     |
| **Total**                                      | 70    |

**Correction to audit §1.2.** The audit's table states 10 unicode files; there
are **11** on disk (`unicode_normalize.go`, `unicode_normalize_tables.go`,
`unicode_normalize_table_00..04`, `unicode_combining_class_table.go`,
`unicode_combining_class_table_00..02`). With 11 the rows sum to 70, matching
§1.1's total. Verify against the 70 total, not the audit's row sum — there is
no missing file to chase.

`**/testdata/**` and `*.golden` overlap: 47 testdata files, 42 goldens, union
47. Adding the rows without deduplicating those two double-counts.

### Denominator drift caused by this session

Every table in this file is measured on the tree **as the audit saw it** —
before S01's own instrumentation existed — which is what makes them directly
comparable to audit §2. S01 then adds itself to the repository: 9 new files
(`.refactorignore`, six `scripts/metrics*.py`, and these two docs) plus 16
lines in the `justfile`, about 1,150 LOC.

So the first `just metrics` run after S01 lands will report roughly **1,102
REAL files and ~215,050 LOC**, not 1,093 / 213,900. That is expected, and it
moves only §1, §2.1, and §2.2. **It cannot move any ratcheted number**: the
additions are Python and Markdown, while §2.4, §2.5, §2.6 and §2.7 all measure
Go and TypeScript only. No added file exceeds 500 LOC, so §2.1's headline
finding — exactly one REAL file over 500, and it is a CSS lookup table — still
holds after this session.

---

## 2. Measurements

### 2.1 File size

| Threshold | Files |
| --------- | ----- |
| >500 LOC  | 1     |
| >400 LOC  | 79    |
| >300 LOC  | 247   |
| >200 LOC  | 463   |

The one file over 500 is `internal/web/static_src/css/chroma.css` (870), a flat
syntax-highlight theme table — not a refactoring target. Nothing sits between
501 and 869. **File size is a dead metric in this repo**; the discipline is
hand-applied and should not be relaxed.

**This table is as of the audit. Since S03 there are two files over 500: this
one, now 696 lines** (see §6 — measured tables, the ESLint findings, and the
`node_modules` incident). No source file crossed the line, and no lint rule
enforces it in the first place (§2.9). The headline finding is about Go, CSS and
TypeScript, and it is unchanged; a measurement record was not shortened to keep
a statistic tidy.

Largest 10: `chroma.css` 870, `overview-search.ts` 500,
`vaultgit/intent_integration_test.go` 494, `web/related_api.go` 491,
`static_src/properties-workbench.ts` 489, `web/diagnostics.go` 488,
`vaultgit/harness_test.go` 488, `web/static_css_test.go` 487,
`query/export_test.go` 487, `docs/commands-maintenance.md` 487.

### 2.2 Package size (top 10 directories, non-recursive)

| Total LOC | Files | Source | Test   | Directory                     |
| --------- | ----- | ------ | ------ | ----------------------------- |
| 37,096    | 196   | 23,844 | 13,252 | `internal/web/static_src`      |
| 36,935    | 149   | 15,302 | 21,633 | `internal/web`                 |
| 25,950    | 147   | 9,017  | 16,933 | `internal/cli`                 |
| 11,808    | 54    | 5,883  | 5,925  | `internal/vaultgit`            |
| 11,099    | 63    | 4,576  | 6,523  | `internal/vault`               |
| 9,329     | 42    | 5,817  | 3,512  | `internal/noteops`             |
| 7,505     | 34    | 3,994  | 3,511  | `internal/queryexpr`           |
| 7,310     | 31    | 2,602  | 4,708  | `internal/query`               |
| 7,264     | 35    | 2,951  | 4,313  | `internal/config`              |
| 6,868     | 42    | 6,868  | 0      | `internal/web/static_src/css`  |

Grouping is by immediate directory, not subtree — that is why `static_src`
(196 `.ts` files) and `static_src/css` (42 `.css` files) are separate rows.

### 2.3 Churn × size (top 15)

Total history is 2026-05-18 → 2026-09-01, 183 commits. "Last 6 months" is the
entire history, so this measures development activity, not rework, and the
"unchanged in 12 months → low priority" adjustment is vacuous here.

```
12576 commits=32 loc=393 internal/config/config.go
10714 commits=22 loc=487 internal/web/static_css_test.go
10175 commits=25 loc=407 internal/web/server.go
 9580 commits=20 loc=479 internal/query/filter.go
 8085 commits=21 loc=385 internal/vault/vault.go
 7391 commits=19 loc=389 internal/query/filter_test.go
 7344 commits=17 loc=432 internal/config/validate.go
 7329 commits=21 loc=349 internal/config/defaults.go
 6980 commits=20 loc=349 internal/vault/note.go
 6435 commits=15 loc=429 internal/web/templates/index.html
 6390 commits=15 loc=426 internal/frontmatter/parse.go
 5797 commits=17 loc=341 internal/wikilink/parse.go
 5676 commits=12 loc=473 internal/frontmatter/model.go
 5625 commits=15 loc=375 internal/template/discover.go
 4488 commits=12 loc=374 internal/vaultgit/manager.go
```

### 2.4 Function complexity — Go

golangci-lint 2.13.2, thresholds **funlen 100/50, cyclop 10, gocognit 20,
nestif 4**, generated files excluded.

| Linter       | Total   | Source  | Test    |
| ------------ | ------- | ------- | ------- |
| cyclop       | 346     | 153     | 193     |
| gocognit     | 165     | 93      | 72      |
| nestif       | 35      | 33      | 2       |
| funlen       | 6       | 2       | 4       |
| **Total**    | **552** | **281** | **271** |

Independent cross-check: `gocyclo -over 10 .` reports **493** functions.

Ten worst source functions by cognitive complexity:

```
231  internal/cli/search.go:25            NewSearchCmd
 76  internal/cli/repair.go:15            NewRepairCmd
 75  internal/cli/rename_key.go:14        NewRenameKeyCmd
 64  internal/web/graph.go:53             buildGraphResponse
 57  internal/cli/find.go:19              NewFindCmd
 56  internal/schema/repair_coerce.go:64  coerceScalar
 52  internal/cli/new.go:29               NewNewCmd
 51  internal/backup/backup.go:59         Create
 50  internal/cli/rename.go:16            NewRenameCmd
 50  internal/cli/replace.go:27           NewReplaceCmd
```

`NewSearchCmd` is a 3× outlier over the next worst. **15 of 37 `New*Cmd`
constructors exceed gocognit 20**: search 231, repair 76, rename_key 75, find
57, new 52, rename 50, replace 50, unset 50, relink 46, links 40, related 37,
backlinks 35, open 32, undo 24, lint 22. All share one shape — the whole
command body as an anonymous `RunE:` closure inside the constructor.

Files with most source violations: `brief/service_selection.go` 5,
`template/discover.go` 5, `cli/search.go` 5, `backup/backup.go` 4,
`cli/backlinks.go` 4, `cli/related.go` 4, `config/validate.go` 4,
`recall/state.go` 4, `web/viewmodel.go` 4.

### 2.4 Function complexity — TypeScript (proxy)

> **Superseded by §6 below.** S03 added `eslint.config.js` and the real numbers
> are recorded there. This section is the pre-ESLint proxy, kept because it is
> what the ranked worklist in the audit was built from and because the gap
> between the two is itself a finding.

The substitute was a brace-depth top-level-function length scan over non-test
`static_src/*.ts`.

| Metric                          | Baseline |
| ------------------------------- | -------- |
| Top-level functions detected    | 567      |
| Functions ≥60 lines             | **50**   |

```
445  overview-search.ts:56              initOverviewSearchResults
434  properties-workbench.ts:56         createPropertyWorkbench
310  daily-fixes.ts:48                  initDailyFixes
296  related-controller.ts:86           createRelatedController
285  facets.ts:81                       initFacets
277  query-bar-controller.ts:86         initQueryBarController
254  daily-brief.ts:38                  initDailyBrief
228  synthesize-create-actions.ts:30    createSynthesisCreateActions
215  properties-detail.ts:45            initPropertyDetail
213  synthesize-context-actions.ts:27   createSynthesisContextActions
```

**Divergence from audit §2.4: 50 here vs 58 in the audit.** The audit's
one-off scanner mis-measured functions whose signature spans several lines
*and* carries a `= {}` default parameter — it latched onto the `{}` of the
default value as the body brace. `scripts/metrics.py` tracks paren depth and
requires the body `{` to end its line, which recovers two of the largest
functions in the codebase that the audit's list omits entirely:
`createPropertyWorkbench` (434) and `initQueryBarController` (277). Every one
of the audit's ten named entries reproduces here at the same file, line, and
length. **Treat 50 as the baseline and the audit's 58 as superseded.**

### 2.5 Duplication

Normalized 12-line sliding-window hash: comments and blank lines stripped,
whitespace collapsed, windows with fewer than 6 distinct lines discarded. A
**cluster** is one distinct normalized window appearing in ≥2 REAL files.

| Corpus            | Clusters |
| ----------------- | -------- |
| Go source         | **176**  |
| Go tests          | **242**  |
| TypeScript source | **13**   |

Largest Go source pairs:

```
25 windows  internal/web/brief_api.go         <-> internal/web/brief_insight.go
23 windows  internal/brief/state_file.go      <-> internal/recall/state_persistence.go
19 windows  internal/cli/notes.go             <-> internal/noteops/mutation_path.go
18 windows  internal/brief/state_validate.go  <-> internal/recall/state.go
13 windows  internal/cli/backlinks.go         <-> internal/cli/links.go
13 windows  internal/cli/rename_target.go     <-> internal/noteops/rename.go
```

**Divergence from audit §2.5: 176/242/13 here vs 129/198/12 in the audit.**
This is the one §2 measurement that does not reproduce, and the reason is that
the audit describes its scanner in prose only and never defines "cluster". No
reading of that prose lands on 129: counting distinct duplicated window hashes
gives 176, and merging overlapping runs into single regions gives 30. The
audit's own §6 already labels this number a **floor** (`dupl`/`jscpd`/CPD were
not installed, and exact hashing cannot see renamed clones). The definition
above is now pinned in `scripts/metrics.py`, so from here the number is
comparable session to session even though it is not comparable to the audit.

What *does* reproduce is everything the audit drew conclusions from: the pair
ranking, and the headline `brief/state_file.go ↔ recall/state_persistence.go`
at exactly 23 windows. The rank-3 target (three hand-rolled sidecar stores over
one `vaultstate.Save`, ~1,825 LOC across `brief/state*.go`, `recall/state*.go`,
`cockpit/state.go`) is confirmed, and `brief/state_validate.go ↔
recall/state.go` measures *larger* here (18 vs 11) than the audit claimed.

Per audit §3.3, two of these pairs are **drift risks, not consolidation
targets**: `cockpit/queue.go ↔ web/diagnostics.go` and
`noteops/frontmatter_multi_lowfd_test.go ↔ frontmatter_multi_test.go`. Add a
conformance test; do not merge.

### 2.6 Dead code and over-export

| Measurement                                   | Baseline |
| --------------------------------------------- | -------- |
| `deadcode ./...` (from binary entrypoints)     | **81**   |
| `deadcode -test ./...`                         | **32**   |
| Go exports never referenced cross-package      | **243** of 865 (28%) |
| TS unique exported symbol names                | 1,060 (1,082 declarations) |
| TS exports referenced only in own file         | **210** (109 values + 101 types) |

`deadcode -test` by package: `cli` 13, `noteops` 13, `schema` 3, `index` 1,
`properties` 1, `web` 1.

Go over-export by package: `noteops` 60, `vaultgit` 44, `cli` 40, `brief` 24,
`recall` 22, `config` 9, `queryexpr` 8, `vault` 7.

TS over-export by file: `api-properties.ts` 13, `graph-controller-labels.ts` 9,
`api-cockpit.ts` 8, `overview-search-render.ts` 7.

The TS figure is a regex export/reference scan (`knip` is not installed), so it
has error bars both ways: dynamic access is counted as unused, re-export chains
are counted as used.

**Expect these numbers to rise before they fall.** Decomposition over-exports,
so the export-narrowing sessions are sequenced *after* the decomposition work.

### 2.7 Test ratio

|            | Test LOC | Source LOC | Ratio    |
| ---------- | -------- | ---------- | -------- |
| Go         | 90,742   | 68,882     | **1.32** |
| TypeScript | 13,252   | 23,844     | **0.56** |
| Combined   | 103,994  | 92,726     | **1.12** |

Test code is 49% of REAL LOC and carries 271 of 552 complexity violations.

Volume is not quality — see `coverage-baseline.txt` below, which closes the
audit §6 "test quality" blind spot.

### 2.8 Debt markers

**4 matches for `TODO|FIXME|HACK|XXX`, all false positives. Zero real markers.**

```
docs/commands-research.md:51            bn search "TODO" --context 2      (doc example)
internal/cli/search.go:55               bn search "TODO" --context 2      (help text)
internal/vaultgit/commit_pending_test.go:24  "Inbox/TODO*.md",            (fixture path)
internal/vaultgit/git.go:127            `TODO*.md` would be read as a glob (comment)
```

Debt in this repo is structural, not annotated. There is no suppression list to
mine: zero `//nolint`, zero `eslint-disable`, zero `@ts-ignore`. The only
annotation in use is the repo's own 15 `//bn:direct-write` markers, which *add*
required justification rather than silence a check — leave them exactly as they
are.

---

## 3. Test coverage

`docs/internals/coverage-baseline.txt` recorded `go test -cover ./...` at this
same commit — all packages pass. The figures below are from that capture; the
file itself is now regenerated with `just coverage`, which uses the justfile's
`node_modules`-filtered package list. This closes the audit §6 blind spot: the 1.32
Go test ratio is a volume number, and sessions S15–S18 and S24–S25 both assume
the safety tests are as strong as they are large.

The assumption largely holds. The safety-critical packages are well covered:
`vaultgit` 82.7%, `vault` 79.7%, `web` 79.9%, `cli` 84.7%, `config` 94.3%,
`wikilink` 96.7%.

**Three findings the LOC ratio hid:**

- **`internal/vaultstate` has no test file of its own — 0.0%.** It is the
  documented single write path for durable sidecar state (`brief.json`,
  `cockpit.json`, `recall.json`), and it is exercised only indirectly through
  `mutationmatrix`, `brief`, `recall`, and `cockpit`. **S15–S18 replace three
  hand-rolled stores with one shared store over exactly this package**, so its
  direct coverage should be raised before that work, not after.
- **`internal/noteops` 63.2% and `internal/properties` 63.4%** are the lowest
  real numbers in the repo. `noteops` is the shared note-mutation layer and
  also carries 60 of the 243 cross-package-unused exports, so S10–S11 are
  narrowing an API that is only two-thirds covered.
- `internal/textnorm` and `internal/web/cssbuild/gen` also have no tests of
  their own; both are small and the CSS generator is covered by the drift test
  in `internal/web`.

---

## 4. How later sessions use this file

1. Re-run `just metrics` and diff against the tables above.
2. **The initial ratchet number is 281** (source complexity violations). It
   must fall, never rise. The initial 271 test-file violations are out of scope
   by decision.
3. Record measured deltas here as sessions land, rather than asserting
   improvement. Sessions S02 and S03 append their gate numbers to this file.
4. Where a number diverges from the source audit, this file wins — the audit
   was written in plan mode and could not install `dupl`, `jscpd`, `knip`, or
   ESLint. The two known divergences (§2.4 TypeScript, §2.5 duplication) are
   documented inline above with their cause.

---

