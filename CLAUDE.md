# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`bn` (Brain Notes) is a small Go CLI for editing and querying a plain-Markdown notes vault. It targets fast, vault-aware operations (frontmatter queries, wikilink-aware rename/move, templated note creation) that complement an editor rather than replace one. The README is the canonical user-facing spec — keep behavior aligned with it when changing commands.

## Common commands

The repo uses `just` for development tasks (see `justfile`):

- `just build` — build the `bn` binary in the repo root via `go build -o bn ./cmd/bn`.
- `just install` — `go install ./cmd/bn` (puts `bn` in `$GOPATH/bin`).
- `just test` — `go test ./...`.
- `npm ci` — install pinned frontend tooling before changing TypeScript source or running frontend checks.
- `just web-build` — bundle `internal/web/static_src/main.ts` into committed `internal/web/static/app.js`.
- `just web-check` — run frontend typecheck, Vitest/jsdom tests, and the browser bundle build.
- `just lint` — fails on `gofmt -l .` output, runs `go vet ./...`, then runs `just web-check`.
- `just ci` — the full validation envelope: `gofmt -l` check, `go vet ./...`, `go test ./...`, frontend typecheck + Vitest, and a bundle-drift check that hashes `internal/web/static/app.js`, rebuilds it, and byte-compares the result against those on-disk bytes. That compares the bundle against its own source rather than against `git`, so it passes on an uncommitted tree and fails only when the checked-in bundle is genuinely stale. Run this before declaring work complete (`scripts/health.py` runs exactly this).
- `just fmt` — `gofmt -w .`.
- `bn serve` — local `just build`/`just install` binaries remember this checkout path, rebuild web assets here before serving, and serve static assets from `internal/web/static` on disk. The serve-time build prefers the native `node_modules/esbuild/bin/esbuild` binary to avoid requiring `node`/`npm` when dependencies are already installed; unstamped binaries fall back to current-directory checkout detection, then embedded assets.

Run a single test: `go test ./internal/cli -run TestFind_TagFilter` (standard Go test selectors). Integration tests in `internal/cli/integration_test.go` use golden files under `internal/cli/testdata/`.

Go module is `github.com/sglavoie/sglavoie_brainnotes` (note: the binary is published as `github.com/sglavoie/brainnotes/cmd/bn` per the README, but the local module path differs — keep imports using the local module path).

## Architecture

Layered packages, each with a focused responsibility. Commands compose primitives from the lower layers — keep the boundaries clean rather than reaching across them. One-line map below; the full per-package contract, with the invariants each one is required to hold, is in `docs/internals/architecture.md` — **read it before adding a package or moving responsibility between existing ones.**

- `cmd/bn/main.go` — thin entrypoint, delegates to `internal/cli.Execute`.
- `internal/cli` — Cobra command tree; `root.go` loads config into the command context, `notes.go` centralizes multi-note positional/stdin handling, `target_resolution.go` is the shared NFC-aware single-note resolver.
- `internal/config` — TOML loader for the vault-local `brainnotes.toml`, which *is* the vault-root marker: resolving the root and locating the config are one operation. Saved views are a `[[views]]` array of tables validated **shape-only** — `config` cannot import `queryexpr` without a cycle, so a view whose query rotted still loads and every layer above must render it disabled with its error.
- `internal/vault` — root path + config; `Walk` skips hidden dirs, configured ignores, and the templates dir (template files hold unrendered placeholders that are not valid YAML and must never be walked). Also owns locking and `FileTxn`.
- `internal/frontmatter` — parse/rewrite YAML frontmatter. `Map` preserves insertion order on parse, but `Marshal` **always emits keys alphabetically sorted** — intentional and load-bearing for stable diffs.
- `internal/wikilink` — Obsidian-style `[[Target#Heading|Alias]]` parsing/rewriting. **Code-block aware**: links inside fenced, indented, or inline code are masked out.
- `internal/query` — composable `Predicate`s over `*vault.Note` for `find`, plus the path-aware wikilink graph reused by recall ranking. `GraphMetrics` adds bounded graph traversal (degree, connected components, hop-bounded BFS) and the six graph predicates lower onto `Reachable`. `And` with no args matches everything.
- `internal/queryexpr` — the vault query expression language (lexer, AST, parser, `ParseError`). A sibling of `internal/query`, never files inside it: it may import `query`, `config`, `schema`, `frontmatter`, `vault`, `wikilink`, `textnorm` and nothing above them.
- `internal/recall` — deterministic contextual retrieval, no persistent index; ranking, context bundles, and the `<vault>/.bnotes/recall.json` state store.
- `internal/agent` — shared argv-only process runner. No shell, provider SDK, persistence, or implicit network behavior.
- `internal/template` — discovery and rendering for `bn new`; case-insensitive lookup, `.Vars` from repeatable `--var key=value`.
- `internal/web` — HTTP server, page handlers, and mutating APIs. `static_src/` is strict TypeScript bundled by esbuild into the committed `internal/web/static/app.js`, so Go builds need no Node.
- `internal/cockpit` — the shared Daily Fix Cockpit core (queue, state, fixer, review context) consumed by both `bn doctor` and the web cockpit APIs, so CLI and web stay identical.
- `internal/noteops` — the shared note-mutation layer used by both CLI commands and the web APIs; `preview.go` defines the canonical `NotePreview` model every `--dry-run` and every web preview renders through.
- `internal/properties` — the reusable property index behind the web workbench. Deliberately does not depend on `internal/web`.
- `internal/pins` — the shared pinned-list core; plans assignments only, never writes. It is a **leaf package**: it must not import `internal/noteops`, because `noteops` → `query` → `pins` would close a cycle.
- `internal/index` — a persistent cache of note bytes in `<vault>/.bnotes/index/`, so a repeated vault load stats each note instead of re-reading it. It saves the read and **not** the parse: a warm load still parses a fresh `*vault.Note` (and so re-runs `frontmatter.Parse`) for every walked note. Another **leaf package**: it may import `vault`, `config`, `frontmatter` and nothing else — it produces `[]*vault.Note` and stops. It walks via `vault.Walk` (never a hand-rolled `filepath.WalkDir`), trusts a cached entry only when size and mtime match *and* the mtime is older than a 2s non-configurable `staleWindow`, and builds a fresh `*vault.Note` per snapshot. It **self-heals silently** on any unusable state file — the deliberate inverse of `internal/recall`'s fail-closed store, because the index holds no user intent — so an index failure is never a request error and a failed disk write is ignored.

## Implementation notes

- **Property semantics are config-driven** through one unified model: each `[frontmatter.properties.<name>]` section declares `type` (`string`/`wikilink`/`date`/`datetime`/`int`/`float`/`bool`/`list`), `items` (required for lists, naming the element kind — lists do not nest), optional `required`, and an optional `allowed` allow-list. There are no implicit runtime defaults, so the starter config spells out `categories`, `tags`, `topics`, `org`, `type`, `parent`, `area`, and `pinned`. `bn set k=a,b,c` splits on comma only for a property declared `type = "list"`, and auto-`[[ ]]` wrapping happens only for a `wikilink` type or a list with `items = "wikilink"`. Undeclared properties remain legal and are left alone by lint/repair. Tests rely on this — when adding new property semantics, extend `PropertyDefinition` rather than hard-coding key names.
- **Type coercion in `bn set`**: `true`/`false` → bool, integer → int, `YYYY-MM-DD` → date, else string. `--type date|int|bool|raw` forces a type.
- **Stdin convention**: `-` as the sole note arg means "read paths from stdin". For `bn unset` with multiple notes, use `--` to separate notes from property names. `-0` enables NUL-separated I/O end-to-end (paths in *and* paths out from `bn find`).
- **`PersistentPreRunE` skips config loading** for `completion`, `help`, and `init` so they remain usable outside a vault. `config` deliberately does *not* skip: it reports the resolved vault, so it needs the load to have happened.
- Frontmatter rewrites must round-trip cleanly — preserve `Body` verbatim and only rewrite the frontmatter region. Tests under `internal/frontmatter/testdata` and `internal/cli/testdata` (golden files) catch regressions here.
- **`--dry-run` output has one canonical format** across every mutating command (`set`/`unset`/`replace`/`repair`/`relink`/`rename`/`move`). Each changed file is rendered by `internal/noteops`'s `RenderNotePreview` (`internal/noteops/preview.go`): a `--- <relpath>` header, then one line per change — `path: <old> -> <new>` for a rename/move, `frontmatter[<key>]: <old> -> <new>` for a property change, and `body:<line>: <old> -> <new>` for a body line. The same `NotePreview` model backs the web preview APIs, so CLI dry-run and web previews cannot drift. Preview-before-apply is mandatory for cockpit file changes: the web `/api/fixes/apply` requires a one-use `preview_id` from `/api/fixes/preview`, and bulk note actions require `/api/notes/bulk/preview` first.
- **Body diffs in `rename` / `move` dry-run** run the whole-body rewrite first and then diff line-by-line. Per-line rewriting would break `wikilink.Parse`'s code-fence masking, because a fence opener on one line is invisible to the next line's parse — the dry-run would report phantom changes for masked links that the apply path (which uses whole-body rewrite) correctly leaves alone.

## Deep reference

These invariants used to live inline here and are load-bearing. Each file is short; open the matching one **before** you touch that area, not after a review flags it.

- Writing a note, taking a lock, or committing a `vault.FileTxn` → `docs/internals/locking.md` (atomicity model, per-path flock, the shared vault guard and escalation, custody).
- Changing `bn related`, `bn context`, `internal/recall`, or the recall sidecar → `docs/internals/recall-context.md` (read/write boundary, context privacy boundary, selection invariant, state recovery).
- Adding or changing any mutating web API route → `docs/internals/web-write-classes.md` (preview-gated vs. narrow-unpreviewed, the property/capture/pin classes, the property intelligence cache).
- Changing any `/api/synthesis/*` route, the `/synthesize` page, or `internal/noteops/synthesis.go` → `docs/internals/synthesis.md` (read-only context pair, preview/apply write class, frontend module split and persistence rules).
- Adding a package or moving responsibility between packages → `docs/internals/architecture.md`.
