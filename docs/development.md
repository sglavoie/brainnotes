# Development

```sh
npm ci
just web-check
just lint
```

Run commands from the repository root. Run `npm ci` before frontend work or after package-lock changes. `just web-build`
regenerates the committed, minified JavaScript bundles under `internal/web/static`
from `internal/web/static_src`; `just web-check` type-checks, lints, runs frontend
tests, and rebuilds it. The lint step is `npm run lint:web` — ESLint over the
TypeScript files changed since the complexity baseline, so existing offenders do
not block while no new violation can land. The generated JavaScript is committed
so Go-only builds do not need Node. The editor and Sync pages load `editing.js`
for CodeMirror; note reading loads only `app.js`. Each workspace loads its own
small page bundle. `internal/cli/web-build-args.json` is the shared entry manifest
for local builds, serve-time refresh, and CI drift checks. `npm run test:e2e:pr`
runs the declared PR Chromium journeys, an explicit hosted creation and save profile,
the read-only profile, and the dedicated
performance profiles. `npm run test:e2e:weekly` runs all local and read-only regressions plus
the hosted cross-browser qualification. The weekly workflow runs that full suite.
Assign every new spec in `internal/web/e2e/suites.js`: `lint:e2e` rejects missing,
duplicate, or stale assignments before fixture setup. The Playwright projects
consume that same catalog, so the CI commands need no filename allow-list.
Visual baselines cover Library, Brief, reading, editing, management pages,
conflict and restore comparisons, confirmation, and empty/error states in both light and dark themes at both desktop and mobile widths. Run these on macOS with
`npm run test:e2e -- --project=visual-chromium`; after an intentional design change,
append `--update-snapshots`, inspect the changed images in
`internal/web/e2e/visual-baselines`, and commit them. CI compares on macOS to avoid
cross-platform font differences. A weekly and manually runnable qualification
workflow exercises Chromium, Firefox, and WebKit at both widths. The stylesheet is generated the same way but by Go: `internal/web/static/app.css`
is the concatenation of the parts under `internal/web/static_src/css/` in the
order `index.css` lists them, rebuilt by `just web-build` (or
`go run ./internal/web/cssbuild/gen`) and checked for drift by `go test ./...`,
so editing CSS needs no Node at all. Edit the parts, never the generated file. Local `just build` and `just install` stamp this checkout path into the
binary so `bn serve` can refresh web assets from this source tree even when
launched from another directory.

## Before submitting a change

Run `just ci` for formatting, Go vet and tests, the complexity gates, frontend
checks, and generated-bundle drift. Run `npm run test:e2e` for browser changes.
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

## Mobile and accessibility polish

`accessibility.spec.js` runs axe WCAG A/AA checks and 320 px/200% reflow checks
for Library, reading, and editing in the PR Chromium suite. For a manual screen
reader pass, use a disposable vault and the same three pages. With VoiceOver and
Safari on macOS, move through landmarks and headings, search the Library, open a
result, then open the editor and save a small change. Confirm every control has an
understandable name and state, status updates are announced, and keyboard focus
stays on the active task. Record the browser, OS, assistive technology version,
pages, and any failures in the browser UI pull request checklist or with release
evidence. Automation cannot verify spoken order or announcement quality.

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

For physical-device qualification, open the updated workspace on an iOS or Android
phone using a disposable vault. In the editor, focus prose, type at the end of a
long note, switch Edit/Preview, open Format and Find, and save. Repeat in landscape
and with larger system text. Confirm the caret and Save remain reachable while the
keyboard is open, no toolbar obscures the selected text, and failed saves keep the
draft. Record the device, OS/browser version, orientation, and result with release
evidence; never perform this qualification against an ambient personal vault.

## Vault performance

The dedicated `performance-chromium-1000` and `performance-chromium-10000`
projects use isolated, deterministic vaults with 1,000/10,000 linked research
notes plus a long writing note. They never seed the ordinary browser fixtures.
Both PR and weekly browser gates run them. To run just these checks:

```sh
npm run test:e2e -- '--project=performance-*'
```

Measurements cover search, expression filtering, and typing into a long source
note at 4× CPU throttling and 100 ms network latency. Each test attaches JSON
measurements with corpus size and long tasks. Inspect those artifacts when a
budget fails; do not raise a ceiling merely to pass a slower run.

[Back to the README](../README.md)


`continuity-refinement.spec.js` covers reading-to-source passage continuity,
mobile Edit/Preview position, Library selection recovery and revalidation,
keyboard Graph navigation, and bounded prose at a wide desktop viewport. Graph
joins the light/dark, desktop/mobile visual matrix; its payload and final node
positions are fixed for reproducible screenshots, while behavior tests use the
real graph API.
