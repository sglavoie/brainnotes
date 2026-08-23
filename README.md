# bnotes (`bn`)

Brain Notes is a small, fast Go CLI for editing and querying a plain-Markdown
notes vault. It complements an editor (VS Code, Neovim) by handling the
operations editors do poorly: frontmatter-aware queries, wikilink-aware file
operations, and templated note creation.

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
`internal/web/static_src`; `just web-check` type-checks, runs frontend tests, and
rebuilds it. The generated JavaScript is committed so Go-only builds do not need
Node. Local `just build` and `just install` stamp this checkout path into the
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
  - [Query and research](docs/commands-query.md)
  - [Vault administration and recovery](docs/commands-vault.md)
  - [Note creation and navigation](docs/commands-notes.md)
  - [Local web server](docs/web-server.md)
    - [Related Notes explorer](docs/web-related-notes.md)
    - [Synthesis workbench](docs/web-synthesis.md)
    - [Health, Daily Fixes, and properties](docs/web-health.md)
  - [Maintenance and bulk edits](docs/commands-maintenance.md)
- [Shell completion and editor integration](docs/integrations.md)
- [Recipes, safety, performance, and migration notes](docs/workflows.md)
