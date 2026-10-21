# Development

## Prerequisites

| Tool | Version | Used for |
| --- | --- | --- |
| [Go](https://go.dev/dl/) | the `go` line in `go.mod` (1.26.2) or newer | every build, test and Go gate |
| [just](https://just.systems/man/en/) | any recent release | every recipe below |
| [Node.js](https://nodejs.org/) | `.nvmrc` (22); `package.json` `engines` accepts `^22.22.2`, `^24.15.0` or `>=26` | `npm ci`, frontend checks, bundle builds |
| Python 3 | the `python3` on `PATH` | `just ci` (`scripts/test_*.py`), `just vault-sandbox`, `just metrics`, the self-tests |
| golangci-lint | pinned in the `justfile` (`golangci_version`) | `just lint-new` inside `just lint` and `just ci`; install with `just install-lint` |
| Playwright browsers | pinned by `package-lock.json` | `npm run test:e2e`; install once with `npm run test:e2e:install` |

`just install-lint` runs `go install`, so the Go install directory
(`$(go env GOBIN)` if set, otherwise `$(go env GOPATH)/bin`) must be on `PATH`. The complexity ratchet fails rather than skips when
golangci-lint is missing.

## Getting started

```sh
npm ci
just web-check
just lint
```

Run commands from the repository root. Run `npm ci` before frontend work or after
package-lock changes. [Contributor commands](internals/contributor-commands.md)
describes every recipe in detail.

## Frontend assets

`just web-build` regenerates the committed, minified JavaScript bundles under
`internal/web/static` from `internal/web/static_src`; `just web-check`
type-checks, lints, runs frontend tests, and rebuilds them. The lint step is
`npm run lint:web` — ESLint over the TypeScript files changed since the
complexity baseline, so existing offenders do not block while no new violation
can land.

TypeScript runs side by side, as the TypeScript 7 release recommends:
`@typescript/native` is TypeScript 7 and provides the `tsc` that
`npm run typecheck:web` uses, while the `typescript` name is an alias for
`@typescript/typescript6` because typescript-eslint loads the TypeScript 6
compiler API. Drop the alias once typescript-eslint supports TypeScript 7.

The generated JavaScript is committed so Go-only builds do not need Node. The
editor and Sync pages load `editing.js` for CodeMirror; note reading loads only
`app.js`. Each workspace loads its own small page bundle.
`internal/cli/web-build-args.json` is the shared entry manifest for local builds,
serve-time refresh, and CI drift checks.

The stylesheet is generated the same way but by Go: `internal/web/static/app.css`
is the concatenation of the parts under `internal/web/static_src/css/` in the
order `index.css` lists them, rebuilt by `just web-build` (or
`go run ./internal/web/cssbuild/gen`) and checked for drift by the Go test suite (`just test`, and so `just ci`),
so editing CSS needs no Node at all. Edit the parts, never the generated file.

Local `just build` and `just install` stamp this checkout path into the binary
so `bn serve` can refresh web assets from this source tree even when launched
from another directory.

## Browser suites

`npm run test:e2e:pr` runs the declared PR Chromium journeys, an explicit hosted
creation and save profile, the read-only profile, and the dedicated performance
profiles. `npm run test:e2e:weekly` runs all local and read-only regressions plus
the hosted cross-browser qualification; the weekly workflow runs that full suite.
A plain `npm run test:e2e` runs every project. The 1,000- and 10,000-note
performance vaults are seeded and served only when the selected projects include
a performance project, so `npm run test:e2e -- --project=visual-chromium` skips
them.

Assign every new spec in `internal/web/e2e/suites.js`: `npm run lint:e2e`
rejects missing, duplicate, or stale assignments before fixture setup, and
`just ci` runs it too. The Playwright projects consume that same catalog, so the
CI commands need no filename allow-list.

## Visual baselines

Visual baselines cover Library, Brief, reading, editing, management pages,
conflict and restore comparisons, confirmation, and empty/error states in both
light and dark themes at both desktop and mobile widths. Run these on macOS with
`npm run test:e2e -- --project=visual-chromium`; after an intentional design
change, append `--update-snapshots`, inspect the changed images in
`internal/web/e2e/visual-baselines`, and commit them. CI compares on macOS to
avoid cross-platform font differences. A weekly and manually runnable
qualification workflow exercises Chromium, Firefox, and WebKit at both widths.

## README demos

`just demo` re-records `docs/assets/demo-web.svg` and `docs/assets/demo-cli.svg`
from the sample vault in `scripts/demo/vault`; `just demo web` or `just demo cli`
records one. Both are animated SVGs: each frame is stacked in one image and CSS
shows one at a time, so the README plays them with no script and the terminal
stays crisp text. The browser frames are JPEG screenshots inside a drawn window
(`scripts/demo/svg_slideshow.mjs`); the terminal frames are real `bn` output
rendered by `scripts/demo/render_terminal_svg.py`, which needs
[uv](https://docs.astral.sh/uv/). The terminal steps also use
[jq](https://jqlang.org/), and the tour needs the Chromium from
`npm run test:e2e:install`. The tour and the terminal steps live in
`scripts/demo/record.mjs` and run against a throwaway copy of the vault under
the system temporary directory, never against `$BNOTES_VAULT`. Re-record after
a visible UI change, look at the result, and commit the SVGs. A tour step that
cannot find its element fails the run and saves a screenshot.

## Before submitting a change

Run `just ci` for formatting, Go vet and tests, the complexity gates, frontend
checks, generated-bundle drift, the browser suite's static checks, the Python
script tests, and `go mod tidy` drift. Run `just test-race` after changing
concurrent Go code; CI runs it as its own job. Run `npm run test:e2e` for browser
changes.
Use the disposable fixtures provided by the browser suite; never test mutations
against an ambient vault. See [architecture](internals/architecture.md) and the
relevant internal contract before changing persistence or serving behavior.

For layout changes, check the initial viewport as well as screenshots taken
after scrolling. Library has separate entry and results baselines; reading has entry and scrolled
baselines. History must show its latest entry and restore action on entry. Settings
confirmation must remain beside the edited field and visible after preview. The editor
fixture includes long frontmatter, substantial prose, and an attachment link;
check that prose and Save fit at 320 × 568 and 390 × 640. The reduced-height
writing check models keyboard space, but does not emulate an operating system
keyboard; include a real-device check when qualifying a mobile release. The mobile editor must show editable text on entry and keep
Save reachable while writing. Property forms and Library selection controls
should appear when they are useful, with keyboard access to each disclosure.
On phones, Format, Find, and Save share the first editor toolbar row when they
fit; routine “Ready to edit” text does not consume a row, while save outcomes
and errors remain visible below those controls.

## Mobile and accessibility polish

`accessibility.spec.js` runs axe WCAG A/AA checks and 320 px/200% reflow checks
for Library, reading, and editing in the PR Chromium suite. It also audits open
Library controls, reading actions and property editing, editor formatting and a
confirmation dialog, plus search failure and a pending-write reminder. For a
manual screen reader pass, use the
[browser UI qualification](operations/ui-qualification.md) with a disposable
vault. Record the exact build, device, OS, browser, assistive technology,
observed results, and failures with release evidence. Automation cannot verify
spoken order or announcement quality.

`screen-polish.spec.js` checks usable mobile Properties filters, actual properties
on entry, reading content near the top, Library action placement, Health's real
queue and read-error recovery, and exact MiB settings through save/reload. Normal
Health visual baselines must load tasks; deliberate read failures have separate
baselines. Visual approval must preserve these usability assertions as well as
pixel consistency.

`polish-refinement.spec.js` checks two visible Library results, collapsed controls
with visible constraints, touch target sizes, keyboard disclosures, enlarged text,
forced-color focus, and editor prose/Save in a reduced viewport. The ordinary
editor and qualification suites also exercise dirty drafts, save failures and IME.
These browser checks do not emulate an operating system keyboard.

For physical-device qualification, use the same
[browser UI qualification](operations/ui-qualification.md). The
[earlier mobile editor record](operations/mobile-editor-qualification.md) is
historical evidence; its missing device and browser versions mean it cannot
qualify a new build. The [2026-10-19 browser UI record](operations/ui-qualification-2026-10-19.md)
captures a historical worktree's physical-phone and VoiceOver pass. Qualify the
final release commit with the launcher's `--release --record` options; preserve
its build identity and completed observation fields with the release. Never test
against an ambient personal vault.

## Vault performance

The dedicated `performance-chromium-1000` and `performance-chromium-10000`
projects use isolated, deterministic vaults with 1,000/10,000 linked research
notes plus a long writing note. They never seed the ordinary browser fixtures.
Both PR and weekly browser gates run them. To run just these checks:

```sh
npm run test:e2e -- '--project=performance-*'
```

`continuity-refinement.spec.js` covers reading-to-source passage continuity,
mobile Edit/Preview position, Library selection recovery and revalidation,
keyboard Graph navigation, and bounded prose at a wide desktop viewport. Graph
joins the light/dark, desktop/mobile visual matrix; its payload and final node
positions are fixed for reproducible screenshots, while behavior tests use the
real graph API.

Measurements cover search, expression filtering, and typing into a long source
note at 4× CPU throttling and 100 ms network latency. The reading page check also
records the HTML document, every resource transfer, total transfer, and first
contentful paint. On 2026-10-19, local Chromium measured 100 ms to first paint
without throttling and 1,928 ms with 4× CPU throttling and 100 ms latency. The
reading gate allows 1,500 ms and 3,500 ms respectively; the throttled note body
must also become visible within 5,000 ms. These ceilings leave room for CI
variation while catching a blank first screen that a transfer-size check misses.
Each test attaches JSON measurements with corpus size and long
tasks. Inspect those artifacts when a budget fails; do not raise a ceiling
merely to pass a slower run.

[Back to the README](../README.md)
