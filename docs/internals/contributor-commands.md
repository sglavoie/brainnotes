# Contributor commands and validation

Detailed rationale for the commands summarized in [Development](../development.md).
The repo uses `just` for development tasks; the `justfile` is the source of truth
for every recipe below.

## Build and install

- `just help` — `just --list`, the default recipe: every public recipe with its
  one-line summary.
- `just clean` — remove the `bn` binary `just build` left in the repo root.
- `just build` — build the `bn` binary in the repo root with
  `go build -ldflags '<source checkout stamp>' -o bn ./cmd/bn`. The stamp records
  this checkout path (see `bn serve` below).
- `just install` — `go install`, with the same stamp, for all three binaries
  (`$GOPATH/bin`): `bn`, plus the two private companions `bn-vault-guard` and
  `bn-vault-sync`. The companions are installed with `bn` and never separately,
  because each drives the same internal packages the CLI does — one a version
  behind its CLI is two processes disagreeing about what a snapshot or a package
  means.
- `bn serve` — local `just build`/`just install` binaries remember this checkout
  path, rebuild web assets here before serving, and serve static assets from
  `internal/web/static` on disk. The serve-time build prefers the native
  `node_modules/esbuild/bin/esbuild` binary to avoid requiring `node`/`npm` when
  dependencies are already installed; unstamped binaries fall back to
  current-directory checkout detection, then embedded assets.

The Go module is `github.com/sglavoie/sglavoie_brainnotes`; the README installs
from a clone of `github.com/sglavoie/brainnotes`. Keep imports on the module
path.

## Go tests and formatting

- `just test` — `go test` over the filtered package list (`go_packages`, which
  drops `node_modules`; see below).
- `just test-race` — `go test -race` over the packages with the most concurrency:
  `internal/vault`, `vaultgit`, `noteops`, `web`, `vaultsyncd` and `checkpoint`,
  filtered the same way. It is slower than `just test`, so it is a separate
  recipe and its own CI job rather than part of `just ci`.
- `just fmt` — `gofmt -w` on the files `gofmt -l` reports, skipping
  `node_modules`.
- `just vet` — `go vet` alone over the filtered package list; `just lint` and
  `just ci` run it inside `_go-static`.
- `just coverage` — `go test -cover` over the filtered package list, written to
  `docs/internals/coverage-baseline.txt` with the commit and date it was taken
  at. A snapshot, not a gate; a failing package leaves the file untouched.
- Run a single test with standard Go selectors, for example
  `go test ./internal/cli -run TestFindByTag`. Integration tests in
  `internal/cli/integration_test.go` use golden files under
  `internal/cli/testdata/`.

**`node_modules` is excluded from every Go tool by hand.** An npm dependency
(`flatted`, via `eslint`) ships Go source, and Go's `./...` matcher skips
`testdata`/`vendor`/dot-directories but not `node_modules` — so it silently
joined the module and moved the recorded complexity floor from 552 to 555. The
`justfile`'s `go_packages` / `gofmt_check` substitutions, `.golangci.yml`'s path
exclusion, and the `gocyclo`/`deadcode` filters in `scripts/metrics_*.py` all
encode that one decision. After adding an npm dependency, compare
`just lint-full` and `just metrics` before and after installation on the same Go
source tree; their Go issue totals must agree and must not move merely because
`node_modules` changed.

## Frontend

- `npm ci` — install pinned frontend tooling before changing TypeScript source or
  running frontend checks. Every devDependency is an exact version (`.npmrc`
  sets `save-exact`).
- `just web-build` — build the entry points in `internal/cli/web-build-args.json`
  into committed bundles under `internal/web/static`, and concatenate
  `internal/web/static_src/css/` into committed `internal/web/static/app.css`.
- `just web-check` — frontend typecheck, `npm run lint:web`, Vitest/jsdom tests,
  then the bundle and stylesheet build. It rewrites the committed bundles.
- Vitest helpers shared by many suites (`waitForExpect`, `jsonResponse`) live in
  `internal/web/static_src/shared.test-helpers.ts`. Only test files import it, so
  no bundle entry reaches it.
- Vitest runs test files in shared workers (`isolate: false` in
  `vitest.config.js`), so one jsdom window serves many files. Before and after
  each file, `internal/web/static_src/vitest-setup.test-helpers.ts` resets the
  module graph, removes listeners on window, document, `<html>`, `<head>` and
  `<body>`, cancels pending timers, animation frames and MutationObservers,
  restores real timers, stubbed or reassigned globals, and clears the DOM, URL,
  Web Storage and cookies. If a test changes other shared state, such as a
  prototype, it must restore it itself. Check order independence with
  `npx vitest run internal/web/static_src --sequence.shuffle --sequence.seed=N`.

## Lint and complexity gates

- `just lint` — `_go-static` (fails on `gofmt -l` output outside `node_modules`,
  then `go vet`), `just lint-new`, `just lint-static`, then `just web-check`.
- `just lint-new` — the **function-complexity ratchet**:
  `go run ./scripts/lintratchet -base {{ratchet_base}}` over `.golangci.yml`
  (funlen 100/50, cyclop 10, gocognit 20, nestif 4). The unit of judgement is the
  **function, not the line**: it takes the whole-tree report, maps each violation
  onto its enclosing top-level function with `go/parser`, and fails when any line
  of that function differs from the base — uncommitted and untracked work
  included.
  - That replaces `golangci-lint --new-from-merge-base`, which filtered by changed
    *line* while cyclop/gocognit/funlen report a score on the *declaration* line,
    so editing only a body made a function worse with the gate staying green
    (measured: `contextOutput` sat at gocognit 21 and `just lint-new` reported
    `0 issues.`).
  - `ratchet_base` is a *commit*, not the branch `main`, because work lands
    directly on `main` and `merge-base main HEAD` is then `HEAD` — an empty diff
    range that passes on anything.
  - Violations in functions nobody touched — the floor recorded in
    `docs/internals/refactor-baseline.md` — are still tolerated, so a
    pre-existing violation that merely shifts line number stays out of the way;
    deliberately not `issues.whole-files`.
  - It **fails** rather than skips when `golangci-lint` is absent
    (`just install-lint` installs the pinned version). Read the baseline doc
    before changing a threshold or moving `ratchet_base`.
- `just lint-ratchet-selftest` — the ratchet's regression test
  (`scripts/lint_ratchet_selftest.py`): it builds a throwaway git repository with
  the repo's own `.golangci.yml`, edits only the *body* of a pre-existing function
  from under to over the thresholds, and proves the gate exits non-zero and names
  the function — then proves an untouched violation in the same file is still
  tolerated. Deliberately outside `just ci` (it runs golangci-lint a second time
  against a synthetic tree) and wired into `.github/workflows/ci.yml` as its own
  step, so the gate cannot silently stop enforcing.
- `just lint-full` — the same config over the whole tree, exit 0. A report, not a
  gate; it is how the remaining count is measured. Deliberately absent from
  `just lint` and `just ci`, like `just metrics`.
- `just install-lint` — `go install` the golangci-lint version pinned as
  `golangci_version` in the `justfile`; CI calls the same recipe, so the two
  cannot drift onto different linters.
- `just lint-phase-gate` — fails if any non-test file under `internal/cli`
  carries a complexity violation, reading the whole-tree report because the
  ratchet cannot see old code. `just lint-phase-gate-selftest`
  (`scripts/lint_phase_gate_selftest.py`) proves it catches a violating
  non-constructor function. Both are outside `just ci` (only one golangci-lint
  may run at a time) and run as their own CI steps; see
  [CLI command shape](cli-command-shape.md#verifying-a-decomposition) for the
  rationale.
- `just metrics` — `scripts/metrics.py`: file size, complexity, duplication and
  dead-code measurements, compared against
  [refactor-baseline.md](refactor-baseline.md). A read-only report on stdout,
  absent from `just lint` and `just ci`.
- `just lint-static` — staticcheck and `unused` through the same pinned
  golangci-lint, over the whole tree and both sync build tags, with its own
  config, `.golangci-staticcheck.yml`. It has no baseline: any finding fails. It
  is a separate config because the ratchet, `lint-full` and `lint-phase-gate`
  count every issue `.golangci.yml` reports as a complexity violation. A
  deliberate use of a deprecated API (an equivalence test against it, say)
  carries both `//lint:ignore` and `//nolint:staticcheck` with a reason.
- `npm run lint:web` — the **TypeScript half of the same ratchet**:
  `eslint.config.js` (complexity 10, max-lines-per-function 100, max-depth 4,
  `@typescript-eslint/no-unused-vars`) over `internal/web/static_src/**/*.ts`.
  ESLint has no per-changed-line mode, so `scripts/lint_web.mjs` restricts it to
  the `.ts` files changed since `ratchet_base` — read from the `justfile` so the
  two gates cannot drift onto different anchors — including unstaged and
  untracked ones. That granularity is per *file* — coarser than the Go half's
  per-function judgement: touching a file that still has a violation means
  fixing that file. It **fails** rather than skips when ESLint is missing
  (`npm ci`). `npm run lint:web:full` is the whole-tree report, exit 0, like
  `just lint-full`.
- `npm run lint:e2e` — the browser suite's static gate: `scripts/check_browser_suites.mjs`
  (every spec assigned to exactly one suite in `internal/web/e2e/suites.js`), the
  unit tests for that checker and for the performance-project selection, and
  ESLint over `internal/web/e2e` and `playwright.config.js`. It needs no browser.

`lint` and `ci` share `_go-static` and the frontend `_web-static`
(typecheck, `lint:web`, Vitest) as private recipes, so a gate added to one cannot
be forgotten in the other.

## The CI envelope

`just ci` runs, in order:

1. `_go-static`: the `gofmt -l` check and `go vet` over the filtered packages.
2. `just lint-new`.
3. `just lint-static`.
4. `go test` over the filtered packages.
5. `_web-static`: frontend typecheck, `npm run lint:web`, Vitest.
6. `npm run check:web-bundles`: rebuilds every entry in
   `internal/cli/web-build-args.json` into a scratch directory and byte-compares
   each bundle against the committed file under `internal/web/static`. That
   compares the bundles against their own source rather than against `git`, so it
   passes on an uncommitted tree and fails only when a checked-in bundle is
   genuinely stale. Unlike `just lint`, it never rewrites the committed bundles.
7. `npm run lint:e2e`.
8. `python3 -B -m unittest discover -s scripts -p 'test_*.py'`: every Python
   script test, not a hand-kept list.
9. `go mod tidy -diff`: fails when `go.mod` or `go.sum` is not tidy.

Run it before declaring work complete.
`app.css` drift is checked by `go test` itself.

The GitHub workflow additionally runs the ratchet and phase-gate self-tests, the
phase gate, `just test-race`, the browser PR suite, the macOS visual baselines
and the native `internal/vault` matrix as their own steps or jobs.

## Vault isolation

**Never verify a mutating command against an ambient vault.** `$BNOTES_VAULT` is
normally set in a developer's shell and it **overrides a `cd`**, so
`cd $scratch && bn rename-key ...` writes to the real vault — which is how a
verification run once renamed a frontmatter key in 214 real notes.

- Use `just vault-sandbox -- <bn args>` (`scripts/vault_sandbox.py`): it scrubs
  `$BNOTES_VAULT`, builds a throwaway vault under the system temp directory, asks
  `bn config` which root it *actually* resolved, and refuses with exit 2 unless
  that root is the sandbox.
- The test half of the same rule is **`internal/vaultfixture`**, which states the
  three refusals once as `IsolationError` (a real vault by name, anything outside
  the system temporary directory, anything outside the asserted sandbox) and
  offers disposable Git-backed vault `Pair`s, an `Epoch`-anchored clock, and
  loopback-only listeners.
- `internal/cli/vault_isolation_test.go` delegates the refusals to it — `TestMain`
  scrubs the variable for the whole package and `markVaultRoot`/`useVaultRoot`
  route through `IsolationError`. The `Pair`/`Clock` helpers were deliberately
  ahead of the two-vault synchronization work and `internal/vaultsync`'s tests
  are now their first caller, while `ReserveLoopbackAddr` has its first caller in
  `internal/synctransport`, which must hand a reserved address to a child process
  before it can bind (`ListenLoopback` and `LoopbackHost` are still used only by
  this package's own tests).
- `just test-e2e` is the third caller of the *rule*, launching its servers
  through `scripts/vault_sandbox.py` rather than re-implementing the guard in
  JavaScript.

**Do not write a fourth copy of these refusals**; add a caller.

## Browser suite

`just test-e2e` / `npm run test:e2e` is the **real-browser gate**
(`internal/web/e2e/`, Playwright, `playwright.config.js` at the repo root). Page-level
logic stays in `internal/web/static_src/*.test.ts`; this suite covers what jsdom
structurally cannot. It is **deliberately outside `just ci`**: it needs browser
binaries `npm ci` does not install (`npm run test:e2e:install`), and a CI step
that skips when a dependency is missing is a gate that has stopped gating.

### Fixture and servers

- `npm run test:e2e` runs `npm run lint:e2e`, then `e2e/run.mjs`, which runs
  `prepare.mjs` and then `playwright test` with the arguments npm appended.
- `prepare.mjs` builds an *unstamped* `bn` and, under the system temp directory,
  a local/peer vault pair, a separate hosted vault, a state directory for the
  read-only profile (which serves the peer), and — only when needed — the
  1,000- and 10,000-note performance vaults. It seeds each `brainnotes.toml` with
  a marker line so `/api/config/status`'s `source_hash` identifies which vault a
  server actually loaded, and reserves one loopback port per server.
- The performance vaults are prepared and served only when the `--project`
  filters can select a `performance-chromium-*` project, or when there is no
  filter at all (`e2e/performance-selection.mjs`, using Playwright's own
  wildcard matching). `BN_E2E_PERFORMANCE=1` or `=0` overrides that.
- The config launches each `bn serve` through `scripts/vault_sandbox.py` with
  `cwd` set to the fixture, so `serveWebProjectRoot` finds no checkout and the
  suite tests the **embedded** bundle rather than one it just regenerated into
  the working tree.
- Projects: `local-chromium-regression` (the `pr` suite, plus `weekly` unless
  `BN_E2E_SUITE=pr`), `read-only-chromium-regression`, `hosted-chromium-pr`, the
  hosted qualification projects in Chromium, Firefox and WebKit at desktop and
  mobile widths, `visual-chromium` and the performance projects. Every spec is
  assigned in `internal/web/e2e/suites.js`. One worker runs everything, because
  several specs write to the same vaults and then assert on Git state.

### Origin guard and containment

- `fixture.js` installs an always-on `page.route` origin guard, so a request
  leaving the fixture origin is aborted and recorded whether or not the spec
  asked for the guard — and it *parses* rather than prefix-matches, because
  `http://<fixture-origin>@evil.example/` starts with the fixture origin and is a
  request to somebody else.
- `fixture.js` also owns the shared `gitStatus(vault)` predicate, whose
  `MACHINE_LOCAL` exclusion list (`.bnotes/index/`, `.bnotes/locks/`,
  `.bnotes/source-saves/`, `.bnotes/captures/`, `.bnotes/sync/`,
  `.bnotes/uploads/`) must stay in step with `internal/vaultgit/scope.go` — a
  **new** `.bnotes/` file is deliberately not covered and fails there.
- Excluding `.bnotes/uploads/` takes the staging tree out of every `gitStatus`
  assertion, so `editor-journey.spec.js` reads it directly (`stagedLeftovers`)
  after each save and after a cancel: `Store.Forget` swallows its failures by
  design, and nothing else in this suite would see bytes left behind.

### Core specs

- `smoke.spec.js` covers only what jsdom structurally cannot: the shipped bundle
  executing, rendered HTML parsed by an engine, every seeded note listed on
  `/library`, and a wikilink that actually navigates.
- `isolation.spec.js` covers containment, including a quick capture typed into
  the browser, asserted to land in the fixture vault and not in the peer.
- `guards.spec.js` tests the harness's own two predicates directly.
- `editor.spec.js` covers the `/edit` page for the same reason — the bundled
  CodeMirror mounting, keystrokes and Ctrl/⌘ + S reaching `/api/source/save`, a
  toolbar command and a `[[` completion editing the document, and the typed bytes
  landing in the fixture note and in nothing else.
- `editor-journey.spec.js` covers the attachment and note-management halves of
  that page — a file chooser the browser opened, a drop and a paste onto the
  element CodeMirror owns, a server-chosen destination arriving back
  percent-encoded in a mounted document, the `<details>` nothing inside is
  clickable until it is open, the shared application confirmation dialog in
  front of a trash and an undo, and the dirty gate in both of its answers —
  refusing a rename past unsaved text, and offering the "anyway" button for the
  actions that may proceed over a draft (a capture, taken over unsaved text, with
  the draft still in the editor afterwards and absent from the file) — each
  journey ending in the same `gitStatus` assertion.
- The two editor specs share helpers and work on **different notes**
  (`Alpha.md` is `editor.spec.js`'s, which renames it away and recreates it),
  because one worker runs over one vault pair.

### History restore

`history.spec.js` covers the `/history` workspace — where each recorded change
came from, filtered by origin and by path, and then a restore previewed,
confirmed and *checked against the disk*. The Vitest suite stops at the request
the page issued, not at whether the bytes came back.

- The restore test makes **two** changes to its template and recovers the
  second: undoing a creation only has to delete a path, which would leave the
  central claim — that the bytes somebody chose come back — untested, while
  undoing a rewrite has to put those bytes back, and `readFileSync` is what says
  whether it did.
- Containment is a **before/after comparison**: the peer's existence and its
  `gitStatus` are read before the restore and re-asserted equal afterwards, while
  the served vault is asserted *clean*, which says the restore was journaled and
  snapshotted like any other mutation. The new `bn web undo` entry is asserted by
  **its own identifier** rather than by its command, since the journal holds
  several saves of the same template.
- The path filter narrows to a directory matching nothing and only then widens
  back, because an entry already on screen passes a filter that does nothing.
- It works on its own `Templates/` files, one per test, created and deleted by
  the spec. That directory is excluded from note queries, so writing there cannot
  disturb the seeded-note listing `smoke.spec.js` checks, and it is inside both
  `vaultgit.InScope` and `vaultsync.Portable`, which is what makes the panel's
  "travels to your other replica" claim checkable. The spec checks that
  *classification*; it synchronizes nothing.
- Each template write, and each template *open*, is awaited by its own status
  sentence: the bytes and the revision are fetched and then written into the
  editor, so typing before that lands is typing into a draft about to be
  replaced.

### Sync review

`sync.spec.js` covers the `/sync` review surface — five held packages resolved in
a browser (a text conflict merged by typing into the mounted editor, an
attachment and a divergent rename accepted from the peer, a delete-edit and a
configuration conflict kept), with the receipt count under
`.bnotes/sync/outgoing/receipts/` and the status panel's own sentences as the
assertion that status follows **durable acknowledgment** rather than delivery.

- Its holds are seeded by `scripts/syncfixture`, a Go program `prepare.mjs` runs
  after both vaults are committed: a hold is only real once
  `internal/vaultimport` has refused to apply a package, and a package only once
  `internal/vaultsync`'s `Builder` has produced one, so a state file written by
  hand would agree with the harness instead of with the engine.
- The contested content it writes into the served vault is dated `seededAt`
  (2024-02-01) so those notes sort behind `SEED_NOTES` on `/library`, and none of
  its paths joins `SEED_NOTES`, because a conflict on `Alpha.md` would contest the
  note `editor.spec.js` renames away.
- The holds are consumed by resolving them, so the file is
  `test.describe.configure({ mode: "serial" })` and deliberately ordered: the
  review assertions come before the resolutions that spend them.

### Lessons the editor specs encode

- A synthetic `drop`/`paste` is dispatched at
  **`[data-source-surface] .cm-content`**, not at the surface — the surface is
  where the listeners are, so dispatching there never travels through CodeMirror
  and the capture-phase registration and `stopPropagation` in
  `source-attachment-panel.ts` become unobservable.
- CodeMirror answers a dropped file by reading it through a `FileReader` and
  inserting its bytes as text *and selecting them*, which is asynchronous and
  self-erasing — so the drop is checked against the event, not the document:
  `sendFiles` registers a witness listener on `.cm-content`, which runs if and
  only if CodeMirror's own did, and reports whether the event reached the target
  phase.
- A management action is awaited by **its own status sentence** before the next
  one is clicked, because a mutation holds the Git safety lock for its whole
  operation and that lock is taken *without waiting*, so a second request arriving
  inside the first is refused rather than queued (`vaultgit`'s preflight
  `TryLockPath` → `KindBusy` → `noteops.classifySafetyError` → `CodeConflict` →
  `409`; `vault.IsTxnLockBusy` is a different thing — a five-second path-lock
  wait that has timed out — and is not what refuses here).

### Linting the suite

This directory is plain ESM JavaScript, so `npm run lint:web` (a ratchet scoped
to `static_src`) does not see it — `npm run lint:e2e` applies the same ESLint
rules here, and both `npm run test:e2e` and `just ci` run it.

## Synchronization gates

- `just test-sync-transport` — the **real-transport gate**: two actual Syncthing
  processes on loopback, over two disposable vaults, carrying real `vaultexport`
  packages. It is the synchronization counterpart of `just test-e2e` and is
  outside `just ci` on the same terms (it waits on other processes and needs a
  pinned binary downloaded once; a gate that skips when its subject is absent is
  not a gate). Scenarios are in `internal/vaultsyncd/realtransport_test.go` behind
  the `synctransport` build tag; the harness is `internal/synctransport` and the
  pin is `internal/syncthingbin`. See their entry under Architecture.
- `just test-sync-recovery` — the **distributed recovery gate**: fixed-seed
  offline delivery schedules and capacity failures compose with every ordinary
  synchronization package test, then with `just test-sync-transport`. The long
  randomized scenario is behind the `syncrecovery` build tag; the fast
  import-state ENOSPC cases remain in ordinary test runs. Use it for changes to
  synchronization durability, conflict recovery, receipts, retirement, or
  transport behavior.
