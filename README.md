# bnotes (`bn`)

Brain Notes is a small, fast Go CLI for editing and querying a plain-Markdown
notes vault. It complements an editor (VS Code, Neovim) by handling the
operations editors do poorly: frontmatter-aware queries, wikilink-aware file
operations, and templated note creation.

One query language spans all three surfaces it can be typed into — `bn find -q`,
the `/library` query bar, and the `/graph` filter — with the same syntax, the
same semantics, and one execution path, so a saved view means exactly the same
thing at the terminal and in the browser.

## Install

```sh
go install github.com/sglavoie/brainnotes/cmd/bn@latest
```

Or, from a local clone:

```sh
just install
```

## Development

```sh
npm ci
just web-check
just lint
```

Run `npm ci` before frontend work or after package-lock changes. `just web-build`
regenerates the committed `internal/web/static/app.js` from
`internal/web/static_src`; `just web-check` type-checks, lints, runs frontend
tests, and rebuilds it. The lint step is `npm run lint:web` — ESLint over the
TypeScript files changed since the complexity baseline, so existing offenders do
not block while no new violation can land. The generated JavaScript is committed
so Go-only builds do not need Node. The stylesheet is generated the same way but by Go: `internal/web/static/app.css`
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
