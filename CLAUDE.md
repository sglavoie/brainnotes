# Contributor instructions

Brain Notes (`bn`) is a Go CLI and browser workspace for a plain-Markdown vault.
The [README](README.md) is the user-facing entry point. Keep behavior and docs aligned.

## Start here

- [Development](docs/development.md): setup, generated assets, browser fixtures and checks.
- [Architecture](docs/internals/architecture.md): package responsibilities and links to focused contracts.
- [Implementation conventions](docs/internals/implementation-conventions.md): frontmatter, stdin, config loading and preview semantics.
- [Command rationale](docs/internals/contributor-commands.md): detailed validation and fixture invariants.

Read the contract for the area you change. Keep implementation detail there rather
than duplicating it in this entry point.

## Daily commands

| Task | Command |
| --- | --- |
| Install all three binaries together | `just install` |
| Build | `just build` |
| Frontend dependencies | `npm ci` |
| Rebuild committed web assets | `just web-build` |
| Frontend checks | `just web-check` |
| Required validation before completion | `just ci` |
| Browser changes | `npm run test:e2e` |
| Visual changes (macOS only: baselines are macOS renders) | `npm run test:e2e -- --project=visual-chromium` |
| Safe CLI mutation verification | `just vault-sandbox -- <bn args>` |

Edit TypeScript and CSS under `internal/web/static_src`, then regenerate the
committed assets. The complexity ratchets judge changed functions (Go) and changed
files (TypeScript); do not move their baseline or weaken thresholds to pass.
Use the justfile's filtered Go package list: npm dependencies can contain Go files.

## Boundaries to preserve

- Never test a mutation against an ambient vault. `BNOTES_VAULT` overrides a `cd`.
  Use `scripts/vault_sandbox.py`, `internal/vaultfixture`, or the browser fixtures.
- Keep all three binaries at the same version: `bn`, `bn-vault-guard`, `bn-vault-sync`.
- File writes go through `noteops.BeginOperation` or `vaultstate.Save`. A necessary
  exception must carry a `//bn:direct-write` reason naming the guarantee already
  established. New mutation entry points need a mutation-matrix row.
- Preserve bodies verbatim when changing frontmatter. Preview and apply must
  describe the same changes. Keep stale-revision, one-use receipt and idempotency checks.
- Never vacate an overwrite destination or bypass the storage-location guard.
  Darwin File Provider storage refuses mutations, with no override.
- Ask `s.caps` about serving capabilities; do not derive them from option flags.
- Run `just ci` and the relevant browser/transport gates before declaring completion.

## Read before changing

| Area | Contract |
| --- | --- |
| Files, transactions, locks, Git preflight | [Locking](docs/internals/locking.md) |
| Web mutations and preview receipts | [Web write classes](docs/internals/web-write-classes.md) |
| Hosted and read-only modes | [Serving modes](docs/internals/serving-modes.md) |
| CLI command structure | [CLI shape](docs/internals/cli-command-shape.md) |
| Related notes, context and recall state | [Recall](docs/internals/recall-context.md) |
| Graph behavior and layout | [Graph](docs/internals/graph-view.md) |
| Synthesis routes and frontend | [Synthesis](docs/internals/synthesis.md) |
| Complexity thresholds and baseline | [Refactor baseline](docs/internals/refactor-baseline.md) |
| Restore and enrollment | [Restore reenrollment](docs/internals/restore-reenrollment.md) |
| Synchronization protocol and workers | [Architecture package map](docs/internals/architecture.md) |
