# bnotes (`bn`)

Brain Notes is a Go CLI and browser workspace for a plain-Markdown notes vault.
Use it alongside an editor such as VS Code or Neovim, or run `bn serve` to
browse and edit your notes in the browser.

## What you can do

- **Capture and create:** send a thought to your inbox or create a note from a template.
- **Find and connect:** search Library, save views, explore related notes, and follow the link graph.
- **Read and edit:** read rendered Markdown, edit source, manage properties, and pin notes you return to often.
- **Review and recover:** inspect change history, preview bulk edits, and undo journaled changes.

One query language spans all three surfaces it can be typed into — `bn find -q`,
the `/library` query bar, and the `/graph` filter — with the same syntax, the
same semantics, and one execution path, so a saved view means exactly the same
thing at the terminal and in the browser.

## Install

```sh
git clone https://github.com/sglavoie/brainnotes.git
cd brainnotes
just install
```

## Development

```sh
npm ci
just web-check
just lint
```

Run `npm ci` before frontend work or after package-lock changes. `just web-build`
regenerates the committed, minified JavaScript bundles under `internal/web/static`
from `internal/web/static_src`; `just web-check` type-checks, lints, runs frontend
tests, and rebuilds it. The lint step is `npm run lint:web` — ESLint over the
TypeScript files changed since the complexity baseline, so existing offenders do
not block while no new violation can land. The generated JavaScript is committed
so Go-only builds do not need Node. The editor and Sync pages load `editing.js`
for CodeMirror; note reading loads only `app.js`. Each workspace loads its own
small page bundle. `internal/cli/web-build-args.json` is the shared entry manifest
for local builds, serve-time refresh, and CI drift checks. A Chromium CI job
exercises core journeys, responsive layouts, and throttled reading/search.
Visual baselines cover Library, reading, editing, confirmation, and empty/error
states in light/dark themes at desktop/mobile widths. Run these on macOS with
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

## Quick start

```sh
# Mark a directory as a vault root (writes brainnotes.toml there).
bn init /path/to/vault

# Run from inside the vault, or point bn at it explicitly.
export BNOTES_VAULT=/path/to/vault

# Open the browser workspace.
bn serve

# Find every book note that's still 'To Read'.
bn find --category Books --status "To Read"

# Or say it as one query expression: boolean composition, typed property
# comparison, and link-graph operators the flags cannot spell.
bn find -q 'near:[[Deep Work]]<=2 and status:wip and not folder:Archive'

# Save that as a [[views]] block in brainnotes.toml and run it by name.
bn find --view "Active work" --sort -modified

# Pipe to fzf for interactive selection.
bn find --category Books -i

# Revisit the current note through related notes and an inspectable context packet.
bn related Notes/Current.md -i
bn context Notes/Current.md --purpose questions

# Dump a passing thought into the inbox note without picking a file or a title.
bn capture "ask [[Bob]] about the migration"

# Set a property across many notes.
bn find --tag drafts | bn set - status=done
```

## Documentation

- [Configuration](docs/configuration.md)
- Command reference:
  - [Finding notes](docs/commands-query.md)
    - [Query expressions (`-q`)](docs/commands-query.md#query-expressions--q)
  - [Saved views](docs/commands-views.md)
  - [Research commands](docs/commands-research.md)
  - [Vault administration and recovery](docs/commands-vault.md)
  - [Note creation and navigation](docs/commands-notes.md)
  - [Local web server](docs/web-server.md)
    - [Related Notes explorer](docs/web-related-notes.md)
    - [Synthesis workbench](docs/web-synthesis.md)
    - [Health, Daily Fixes, and properties](docs/web-health.md)
  - [Maintenance and bulk edits](docs/commands-maintenance.md)
- [Shell completion and editor integration](docs/integrations.md)
- [Recipes, safety, performance, and migration notes](docs/workflows.md)
- Contributors: `docs/internals/` documents the architecture, locking model, serving modes and capabilities, web write classes, recall boundaries, graph view invariants, and synthesis workbench invariants.

### Read-only replica server

`bn --vault /path/to/replica serve --read-only --state-dir /path/to/activity --no-open`
serves a read-only note browser. Create the activity directory outside the vault first.
The server skips transaction recovery and Git safety initialization, keeps its note
index in memory, and permits only Daily Brief visits and Open/Another actions to
write external activity state. Open navigates to a rendered note in this mode.
Editor, agent, note/config writes and undo are blocked centrally.

Optionally supply `--replica-status-file /path/to/status.json` containing
`{"last_successful_sync":"2026-09-15T12:00:00Z"}`. The browser shows the last sync
and flags it stale after 15 minutes. `/api/ready` returns content-free readiness
and freshness, with stale content still available. Use an authenticated reverse
proxy for remote access; protect every path, including attachments and readiness.
The Pi deployment and verified synchronization tooling live in the sibling
`sglavoie_raspberry_pi/services/brainnotes` project.

### Hosted server

To write a new note in the browser, choose **New note** in the navigation,
enter a title and optional vault-relative folder, then select **Create and write**.
This saves a title-only note and opens the browser editor; use **Save** to store
your writing. Leave the folder blank to use the vault root. **Capture** remains
the quick-thought flow into your configured inbox. Note creation and editing are
available in local and hosted modes, but not on read-only replicas.

`bn --vault /path/to/vault serve --hosted --no-open` serves the vault itself over
the network. It is not a read-only replica with writes re-enabled: document
actions (capture, properties, pins, cockpit fixes, synthesis) stay available,
while every route that would start a process on the serving machine — the editor,
and the `[agent].command` routes behind Ask AI, daily review, and synthesis
generation — is refused, and the pages that would offer those actions say why
instead of pointing at a config file on the wrong machine. Open navigates to the
rendered note, as in read-only mode. Request bodies are bounded at 1 MiB (local
serving keeps 32 MiB), state-changing requests must be same-origin as well as
carry the CSRF token, responses are `private, no-store`, and an attachment whose
type a browser would execute is sandboxed and sent as a download.
`--read-only` and `--hosted` are mutually exclusive. There is still no auth
layer: put an authenticated reverse proxy or tunnel in front of every path.
