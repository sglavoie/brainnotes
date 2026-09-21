# Refactoring baseline

Measured at `HEAD 9d18e34` on 2026-09-01. This is the committed baseline every
later refactoring session compares against.

Reproduce with:

```sh
just metrics
```

`scripts/metrics.py` is a thin entrypoint over sibling `metrics_*.py` modules,
following the `vault_safety_*.py` convention already in `scripts/`. It is pure
measurement — it reads the repository and writes only to stdout. It is
deliberately **not** part of `just ci`: it is a report, not a gate.

Two details that look incidental and are not:

- The only file it creates is an ephemeral golangci-lint config inside a
  `tempfile.TemporaryDirectory()`, because golangci-lint v2 exposes no
  command-line flags for per-linter thresholds. Passing our own config also
  pins the thresholds to the ones below, so a repo-level `.golangci.yml` —
  Session S02's deliverable — cannot silently move the numbers this baseline
  records.
- The TypeScript complexity half is the one measurement that does **not** pin
  its own thresholds: it runs the repo's `eslint.config.js`, because that file
  is where those thresholds were first written (§6) rather than a config added
  after the fact. It needs `npm ci` to have run; without ESLint installed the
  section reports "unavailable" instead of failing.
- The recipe runs `python3 -B`. Without it, importing the sibling modules
  writes `scripts/__pycache__/` into the working tree, which this repo's
  `.gitignore` does not cover — only a user's *global* gitignore does, which is
  precisely the hazard that `.gitignore`'s own comment warns about.

**The initial number being ratcheted was 281** — source-level complexity
violations at this baseline. Test-file violations (271) were tracked but
explicitly out of scope: they buy
the 1.32 Go test ratio that makes the rest of the work safe.

---

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

`docs/internals/coverage-baseline.txt` records `go test -cover ./...` at this
same commit — all packages pass. This closes the audit §6 blind spot: the 1.32
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

## 5. The ratchet gate (S02)

`.golangci.yml` at the repo root now enforces the §2.4 thresholds — **funlen
100/50, cyclop 10, gocognit 20, nestif 4** — with the generated Unicode tables
and `internal/web/static/` excluded by path as well as by header.

| Recipe            | What it does                                | In `ci`? |
| ----------------- | ------------------------------------------- | -------- |
| `just lint-new`   | Fails on any violation in a **function** changed since the ratchet base | **yes**, and in `just lint` |
| `just lint-ratchet-selftest` | Proves the gate still catches a body-only complexity increase | no — a CI workflow step of its own |
| `just lint-full`  | Reports the whole-tree count, exit 0        | no — a report |
| `just install-lint` | Installs the pinned golangci-lint         | no — CI calls it |

### The ratchet floor (initial S02 measurement)

**The initial floor was 281 source-level violations and 271 test-file
violations, 552 total.** These
are the §2.4 numbers, re-measured through the committed `.golangci.yml` rather
than the ephemeral one `scripts/metrics.py` writes; both produce 552 with the
same per-linter split (cyclop 346, gocognit 165, nestif 35, funlen 6), which is
what makes the gate and the metrics report comparable.

The initial 552 is a count of *reported* issues, and both configs report with
`uniq-by-line` on. cyclop and gocognit both anchor to a function's declaration
line, so a function over both thresholds contributes one issue, not two —
`NewSearchCmd` is cyclop 80 *and* gocognit 231 and counts once. The gate is
unaffected either way: collapsing requires two issues on one line, so one of
them is always still reported and the run still fails.

**The initial 281 must fall and never rise.** The gate does not enforce that
directly — it enforces the stronger local property that no *new* code violates
the thresholds. Read the current report with `just lint-full`.

### Current report (session 27, 2026-09-21)

`just lint-full` and `just metrics` both report **443 total Go complexity
violations: 180 source and 263 test**. Their per-linter split also agrees:
cyclop 297, gocognit 124, nestif 18, funlen 4. The current report includes the
`synctransport` and `syncrecovery` tagged test files in both configurations;
`just lint-new` reports no violation in a changed function. The earlier tables
remain the source audit and S02 historical measurements rather than being
rewritten as though they had been captured from this tree.

### The unit of judgement is the function, not the line

The gate was originally `golangci-lint run --new-from-merge-base={{ratchet_base}}`,
and that had a hole a review caught: `--new-from-merge-base` filters issues by
changed **line**, while cyclop, gocognit and funlen all report a function's
score on its **declaration** line. Editing only the body of an existing function
therefore leaves that function's own violation outside the diff range — the
function gets worse and the gate stays green. Measured, not inferred:
`contextOutput` in `internal/cli/search_output.go` took body changes after
`61f2296` and sat at gocognit 21 while `just lint-new` reported `0 issues.`

`just lint-new` is now `go run ./scripts/lintratchet -base {{ratchet_base}}`.
It asks golangci-lint for the whole-tree report with `--issues-exit-code=0`
(the violations are data; this gate decides which are in scope), maps each issue
onto its enclosing top-level `*ast.FuncDecl` with `go/parser`, and fails when
any line of that function differs from the base. The diff is
`git diff --no-prefix --unified=0 --diff-filter=d <base> -- '*.go'`, which
compares the **working tree** to the base — so an unstaged violation is caught —
plus untracked `.go` files taken whole, the same shape `scripts/lint_web.mjs`
uses on the TypeScript side.

Three properties are deliberate:

- **An untouched violation is still tolerated.** That is what keeps the floor
  above out of the way, and it is why this is not golangci-lint's
  `issues.whole-files`: whole-file granularity would make every untouched
  violation in a touched file the editor's problem, contradicting the line-shift
  tolerance S04–S08 relied on. A pre-existing violation that merely *shifts*
  line number is still outside the gate, because its function's lines did not
  change.
- **A function span starts at the `func` keyword, not at its doc comment**, so
  rewording a comment above a function does not put that function back inside
  the gate.
- **An issue outside every function declaration** — a score on a package-level
  function literal, say — falls back to a one-line span, which is the old
  line-anchored behaviour and the honest answer there.

`just lint-ratchet-selftest` (`scripts/lint_ratchet_selftest.py`) is the
regression test for exactly this hole. It builds a throwaway git repository
carrying the repo's own `.golangci.yml`, asserts the fixture's thresholds are
where it thinks they are, edits **only the body** of a pre-existing function
from 1 branch to 10 (gocognit 21), asserts the declaration line is *not* in the
diff, and then asserts the ratchet exits non-zero naming that function while an
untouched violation in the same file stays tolerated. It was mutation-tested in
both directions: reverting the enclosing-function lookup to line-anchored fails
the "body went over gocognit 20" assertion, and widening the changed-line test
to whole-file fails the "untouched function" one. It is a CI workflow step of
its own rather than part of `just ci`, because it runs golangci-lint a second
time against a synthetic tree.

Adopting the gate required decomposing one function: `contextOutput`, the
instance that exposed the hole, split into `matchingBodyLines`,
`contextWindows` and `writeContextWindows` with no behaviour change.

### The enforcement anchor, and why it is a commit

The gate is anchored by the `ratchet_base` variable in the `justfile`, pinned to
**`61f2296`** — the commit S01 recorded this baseline at.

The audit called for `--new-from-merge-base=main`. On a repository with feature
branches that is correct, but work here lands directly on `main`, and then
`git merge-base main HEAD` **is** `HEAD`: the diff range is empty and the gate
passes on anything. That was measured, not assumed. With a gocognit-28 function
committed to the branch the anchor names, golangci-lint reported `0 issues.` and
exited 0; anchored at `61f2296` the same tree exited 1 on the same function.

Naming the baseline commit keeps both workflows working: the diff is taken
against `61f2296` itself, which is an ancestor of `main` and of any branch cut
from it alike. The successor gate diffs against the commit directly rather than
against a merge base, so the failure mode above cannot recur at all — but the
anchor stays a commit for the same reason it became one, and both halves of the
ratchet still read it from the one `ratchet_base` line in the `justfile`.

`61f2296` is S01's commit, not the `9d18e34` this file's tables were measured
at. S01 added only Python, Markdown, and `justfile` lines, so the Go tree — and
therefore all of §2.4 — is byte-identical between the two.

**Sessions S04–S08 inherit this anchor.** Every line they touch is inside the
diff range, so a decomposed command must land *under* the thresholds; moving an
oversized function to a new file without decomposing it will fail the gate. That
is the intended behaviour.

Raise `ratchet_base` only together with the floor above. Lowering it can only
re-admit violations that are already excluded.

### CI

`.github/workflows/ci.yml` needs two things the gate did not previously require:

- `fetch-depth: 0` on the `ci` job's checkout. The default shallow checkout has
  one commit, so `61f2296` would not exist and nothing could be diffed against
  it. `scripts/lintratchet` checks that the base resolves before doing anything
  else and says so in those words when it does not. The `vault-native` job does
  not run the gate and stays shallow.
- `just install-lint`, which installs `golangci-lint` at the version pinned by
  `golangci_version` in the `justfile`. Installing through the recipe keeps the
  version in one place rather than one in the workflow and one on each machine.

`just lint-new` **fails** when `golangci-lint` is missing rather than skipping.
A gate that quietly does nothing when a tool is absent reads as green and
enforces nothing.

### Suppressions remain at zero

§2.8 records zero `//nolint` in the repository. Turning the linters on
introduced none, and the whole design of the gate is what makes that possible:
because existing violations are outside the diff range, no file needed
silencing to get CI green. The only annotation in use is still the repo's own
15 `//bn:direct-write` markers, which add required justification rather than
remove a check. **A `//nolint` added later is a signal that a threshold is
wrong, or that the code is — not that the directive was needed.**

---

## 6. The TypeScript ratchet gate (S03)

`eslint.config.js` at the repo root is the frontend half of the same gate. It
covers `internal/web/static_src/**/*.ts` and enables four rules:

| ESLint rule                        | Setting | Go counterpart in `.golangci.yml` |
| ---------------------------------- | ------- | --------------------------------- |
| `complexity`                       | 10      | `cyclop` 10                       |
| `max-lines-per-function`           | 100     | `funlen` 100                      |
| `max-depth`                        | 4       | `nestif` 4                        |
| `@typescript-eslint/no-unused-vars` | error  | — (`deadcode`, §2.6, is separate) |

There is **no ESLint equivalent of `gocognit`**, so cognitive complexity stays
unmeasured on the TypeScript side. `complexity` is cyclomatic only.

Dependencies: `eslint` and `typescript-eslint` in `devDependencies`.
`typescript-eslint` 8.69 declares `typescript >=4.8.4 <6.1.0`, which covers the
repo's pinned `^6.0.3`. Only the parser and the one rule from that plugin are
used — no `recommended` preset — so no type-aware linting runs and the whole
tree lints in about two seconds.

### The measured baseline — this closes the audit §6 TypeScript blind spot

Whole tree, 196 files (`npm run lint:web:full`, also printed by `just metrics`):

| Rule                                | Total   | Source | Test    |
| ----------------------------------- | ------- | ------ | ------- |
| `@typescript-eslint/no-unused-vars`  | 199     | 4      | 195     |
| `complexity`                         | 61      | 52     | 9       |
| `max-lines-per-function`             | 66      | 29     | 37      |
| `max-depth`                          | **0**   | 0      | 0       |
| **Total**                            | **326** | **85** | **241** |

`*.test.ts` is the split, matching `scripts/metrics_corpus.py`, so
`*.test-helpers.ts` counts as source.

**52 of 139 source files and 39 of 57 test files carry at least one violation**
— 91 of 196 files in total. That number matters because the gate is per file
(see below).

Ten worst source functions by cyclomatic complexity:

```
28  related-controller.ts:218
23  overview-search-elements.ts:33
22  synthesize.test-helpers.ts:163
20  daily-brief-render.ts:192
20  daily-brief-actions.ts:142
18  note-insight.ts:52
18  graph-controller-view.ts:173
17  related-controller.ts:332
17  properties-detail.ts:45
17  overview-search-render.ts:152
```

Two results are worth stating on their own:

- **`max-depth` is zero across the entire tree.** The §6 blind spot was worded
  as "deeply nested but short TS functions — callback pyramids and promise
  chains in `graph*.ts` / `synthesize-*.ts` that a length proxy scores as
  fine." That hypothesis is **wrong**: nothing in this codebase nests blocks
  four deep. The complexity here is flat and wide, not deep.
- **199 unused variables, 195 of them in test files**, almost all unused
  imports. `tsc --strict` does not catch these (`noUnusedLocals` is off), so
  they had never been reported. They are dead code, not style.

### The proxy was wrong, and S19–S22 must be re-scoped

This is the review question S03 was asked to answer, measured rather than
argued. `just metrics` prints it as "Source `complexity` violations outside any
proxy-flagged (≥60 line) function".

**19 of the 52 source `complexity` violations sit in functions the length proxy
never flagged.** 17 are short top-level functions — 23 to 58 lines, every one
of them under the proxy's 60-line cut. The other 2 the proxy's parser does not
detect as functions at all.

```
 22  synthesize.test-helpers.ts:163  -- proxy detected no enclosing function --
 18  graph-controller-view.ts:173    -- proxy detected no enclosing function --
 16  properties-value-control.ts:101 scalarValueError               (25 lines)
 16  graph-controller-details.ts:132 renderGraphDetails             (34 lines)
 16  daily-brief-actions.ts:92       runAnother                     (49 lines)
 15  query-bar-render.ts:42          renderQueryBarError            (47 lines)
 15  query-bar-model.ts:69           reduceQueryBar                 (58 lines)
 15  query-bar-completions.ts:199    splitFieldAndValue             (46 lines)
 15  daily-fixes-model.ts:66         fixKindLabel                   (34 lines)
 13  synthesize-model.ts:87          readSynthesisConfig            (31 lines)
 12  overview-search-render.ts:69    renderPreviewResultRow         (44 lines)
 12  graph.ts:175                    computeNormalizedGraphDegrees  (41 lines)
 12  graph-controller-labels.ts:258  labelIDsForCurrentState        (53 lines)
 12  api-facets.ts:56                normalizeFacetOptions          (36 lines)
 11  related-actions.ts:49           normalizeRelatedItemResponse   (29 lines)
 11  note-pin.ts:63                  initNotePinAction              (55 lines)
 11  graph-controller-labels.ts:311  visualFocusState               (48 lines)
 11  graph-controller-labels.ts:197  cullCollidingLabels            (58 lines)
 11  api-properties.ts:300           buildPropertyDetailURL         (23 lines)
```

The two undetected ones are a parser limitation worth knowing about before
trusting the proxy again: `collapseStage` in `graph-controller-view.ts` is a
top-level arrow whose multi-line signature carries an object-literal **return
type**, and the `;` inside that type sits at paren depth 0, which
`ts_body_open` reads as the end of a declaration. The `synthesize.test-helpers`
hit is an arrow nested inside a `vi.fn(...)` call, which the proxy — top-level
functions only — never looks inside.

The audit's rank-4 target list — `overview-search.ts`, `daily-fixes.ts`,
`related-controller.ts`, `facets.ts`, `daily-brief.ts` and five more — was
built by sorting functions by **length**. Those files are still real targets;
`max-lines-per-function` independently confirms 29 source functions over 100
lines and reproduces the proxy's ten longest at the same file and function. But
a plan that decomposes only them leaves roughly **a third of the measured
complexity untouched**, in files that never appear on the length list at all:
`properties-value-control.ts`, `daily-fixes-model.ts`, `query-bar-*.ts`,
`graph-controller-details.ts`, `api-facets.ts`, `api-properties.ts`.

**Consequence for the session plan: S19–S22 must be split from this ESLint
list, not from §2.4's length ranking, and the phase gate should be `npm run
lint:web:full` reaching zero source violations rather than
`max-lines-per-function` alone.** S23 (TS export narrowing) gains a real
starting number too: the 199 `no-unused-vars` hits are a stricter, non-regex
signal than §2.6's export/reference scan.

### The gate, and why it is per file rather than per line

| Script                   | What it does                                            | In `ci`? |
| ------------------------ | ------------------------------------------------------- | -------- |
| `npm run lint:web`       | Lints only the `.ts` files changed since `ratchet_base` | **yes**, and in `just web-check` / `just lint` |
| `npm run lint:web:full`  | Reports the whole-tree count, exit 0                    | no — a report |

`scripts/lint_web.mjs` selects the files; `eslint.config.js` holds the rules.
The base commit is read from the `ratchet_base` line in the `justfile`, so the
Go gate and this one cannot end up on different anchors. The file list is
`git diff --diff-filter=d <base>` (which compares the **working tree** to the
base, so an unstaged violation is caught) plus untracked files under
`internal/web/static_src`.

The Go half judges a changed *function* (`scripts/lintratchet`, above), which
needs an AST. ESLint has no new-code mode at all, and this gate has no Go-style
parse of the TypeScript to fall back on. The alternatives were a generated
per-file override list —
which has to be maintained and rots silently — or this. The consequence is
deliberate: **editing one line of a file that still contains a violation makes
that whole file the editor's problem.** With 91 of 196 files currently
carrying one, that is a real cost, and it is the point — those files are
exactly what Phase 5 and Phase 6 exist to decompose. Everything the gate can
fail to establish is fatal rather than skipped, for the same reason
`just lint-new` fails when `golangci-lint` is missing.

Measured behaviour, not asserted: with nothing changed the gate lints zero
files and exits 0 in ~0.2 s. A new file with a complexity-12 arrow and one
unused local exits 1 naming both. Appending a comment to an already-clean file
(`api-types-core.ts`) exits 0; appending one to `overview-search.ts` exits 1 on
that file's four pre-existing violations.

### CI

`just ci` inlines the frontend steps and never calls `just web-check`, so
`npm run lint:web` is added to **both** recipe bodies. A gate wired into only
one of them is half-enforced — the same duplication `lint-new` already has.

The workflow needed no change: `npm ci` already installs the dev dependencies,
and the `ci` job already checks out with `fetch-depth: 0` for the Go gate,
which this gate needs for exactly the same reason.

### Suppressions remain at zero

No `eslint-disable` of any kind was introduced, and there is still no
`@ts-ignore` / `@ts-expect-error` / `@ts-nocheck` anywhere. The gate design is
what makes that possible on this side too: existing violations sit in files no
one is changing, so nothing had to be silenced to get CI green.

### An npm dependency moved the Go floor: `node_modules` is now excluded

Installing ESLint pulled in `flatted` (via `flat-cache`, ESLint's lint cache),
and **`flatted` ships a Go port of itself** at
`node_modules/flatted/golang/pkg/flatted/flatted.go`. Go's `./...` matcher skips
`testdata`, `vendor`, and dot/underscore directories — but **not
`node_modules`** — so from the moment `npm ci` ran, every Go tool in this repo
treated that third-party package as ours:

| S03 measurement                 | Contaminated | Corrected |
| ------------------------------- | ------------ | --------- |
| §5 complexity floor (`lint-full`) | 555 / 284 source | **552 / 281** |
| `gocyclo -over 10` cross-check    | 495          | **493** |
| `deadcode ./...`                  | 88           | **81**  |
| `deadcode -test ./...`            | 39           | **32**  |

`flatted.go`'s `Stringify` scores gocognit 74 and would have ranked fourth-worst
in the whole repository. This was caught by re-running the numbers after the
install rather than by any check — nothing failed, the floor simply moved.

The fix is one decision applied in four places, all of which name each other:

- `.golangci.yml` and the ephemeral config in `scripts/metrics_complexity.py`
  exclude `node_modules/` by path, so both the gate and the report agree.
- `scripts/metrics_complexity.py` passes `gocyclo -ignore node_modules/`, and
  `scripts/metrics_exports.py` drops `node_modules/` lines from `deadcode`.
- The `justfile` defines `go_packages` (`go list ./... | grep -v
  '/node_modules/'`) and `gofmt_check`, used by `test`, `vet`, `lint`, and `ci`.
  Neither `go` nor `gofmt` has an exclude flag, so a substitution is the only
  way to say it.

`internal/audit`'s repository walk already skipped `node_modules` by name
(`direct_write_test.go`, `skipDir`), which is the same judgement reached earlier
for the same reason.

**Any future npm dependency can do this again.** The check is cheap: after
adding one, compare `just lint-full` and `just metrics` before and after the
dependency installation on the same Go source tree. Their Go issue totals must
agree with each other and must not move merely because `node_modules` changed.
The table above remains the historical S03 reading; the current result is under
"Current report" in §5.

### Denominator drift caused by this session

`eslint.config.js` and `scripts/lint_web.mjs` add roughly 190 LOC of JavaScript
to the corpus, and `package.json` / `package-lock.json` grow. As with S01 this
moves only §1 and §2.1–§2.2 and cannot move a ratcheted number: neither file is
Go, and neither is under `internal/web/static_src`, so §2.4 is untouched by
them. `node_modules` was never in the denominator — `scripts/metrics.py` builds
its corpus from `git ls-files`, which is why §1 was the one family of numbers
the `flatted` contamination above could not reach.

After S01 and S03, `just metrics` reports **1,103 REAL files / 215,722 LOC**
against §1's audit-time 1,093 / 213,900. The one visible consequence is in
§2.1: this file is now the second REAL file over 500 lines, noted there.
