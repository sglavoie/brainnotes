# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`bn` (Brain Notes) is a small Go CLI for editing and querying a plain-Markdown notes vault. It targets fast, vault-aware operations (frontmatter queries, wikilink-aware rename/move, templated note creation) that complement an editor rather than replace one. The README is the canonical user-facing spec — keep behavior aligned with it when changing commands.

## Common commands

The repo uses `just` for development tasks (see `justfile`):

- `just build` — build the `bn` binary in the repo root via `go build -o bn ./cmd/bn`.
- `just install` — `go install ./cmd/bn` (puts `bn` in `$GOPATH/bin`).
- `just test` — `go test ./...`.
- `just lint` — fails on `gofmt -l .` output, then runs `go vet ./...`. Run this before declaring work complete.
- `just fmt` — `gofmt -w .`.

Run a single test: `go test ./internal/cli -run TestFind_TagFilter` (standard Go test selectors). Integration tests in `internal/cli/integration_test.go` use golden files under `internal/cli/testdata/`.

Go module is `github.com/sglavoie/sglavoie_brainnotes` (note: the binary is published as `github.com/sglavoie/brainnotes/cmd/bn` per the README, but the local module path differs — keep imports using the local module path).

## Architecture

Layered packages, each with a focused responsibility. Commands compose primitives from the lower layers — keep the boundaries clean rather than reaching across them.

- `cmd/bn/main.go` — thin entrypoint, delegates to `internal/cli.Execute`.
- `internal/cli` — Cobra command tree. `root.go` wires persistent flags and uses `PersistentPreRunE` to load config into the command context (extract with `ConfigFromContext`). Each subcommand lives in its own file: `find.go`, `new.go`, `rename.go`, `move.go`, `set.go`, `unset.go`, `config.go`. `notes.go` centralizes positional-arg handling: it resolves note paths from args, globs, or stdin (`-` triggers stdin; `-0` toggles NUL separation) and enforces in-vault containment.
- `internal/config` — TOML loader with a documented precedence chain (`--config` flag → `BNOTES_CONFIG` → `XDG_CONFIG_HOME` → `~/.config/bnotes/config.toml`). Vault path has a parallel chain (`--vault` → `BNOTES_VAULT` → `vault` key). `DefaultIgnore` (`.git`, `.obsidian`, `Attachments`, `Archive`) is always merged in.
- `internal/vault` — `Vault` is a root path + config. `Walk` skips hidden dirs, configured ignores, and the templates dir automatically (template files contain unrendered placeholders that are not valid YAML, so they must never be walked). `Notes()` loads everything in memory — fine for the documented working size (~300 notes); switch to streaming `Walk` if scaling beyond a few thousand. `ResolveByTitle` does case-insensitive stem lookup and returns an ambiguity error listing all candidates.
- `internal/frontmatter` — parse/rewrite YAML frontmatter. `Map` preserves insertion order during parse but `Marshal` **always emits keys alphabetically sorted** — this is intentional and load-bearing for stable diffs. Values are Go-native types (string, int, float64, bool, time.Time, []any, map[string]any). Wikilink-typed properties are auto-wrapped in `[[ ]]` on write.
- `internal/wikilink` — Obsidian-style `[[Target#Heading|Alias]]` parsing and rewriting. **Code-block aware**: links inside fenced, indented, or inline code are masked out so rewrites never touch documentation samples or code snippets. Used by `rename` (rewrite all forms when title changes) and `move` (rewrite folder-prefixed forms only — bare `[[Title]]` links resolve by title alone in Obsidian and are deliberately left untouched).
- `internal/query` — composable `Predicate` functions over `*vault.Note` for the `find` command's frontmatter dimensions (tag/category/status/type/has/missing/folder/age/orphan). `And` is the identity-with-no-args combinator; queries with no flags match everything.
- `internal/template` — discovery and rendering for `bn new`. Template lookup is case-insensitive and tries `<name>`, `<name>.md`, `<name> Template.md`. Render helpers: `now`, `today`, `dateFormat`, `slug`, `title`, `lower`/`upper`, `default`, `env`, `wikilink`. The `.Vars` map holds repeatable `--var key=value` pairs.

## Implementation notes

- **Atomicity model**: file rewrites use temp-file + rename per file. There is no cross-file transaction — if `bn rename` or `bn move` errors mid-apply, prior writes stand. Mention this caveat in user-facing messages when relevant; rely on `git diff` for recovery.
- **List vs wikilink properties** are config-driven (`[frontmatter].list_properties`, `[frontmatter].wikilink_properties`). `bn set k=a,b,c` splits on comma only for keys in `list_properties`; `wikilink_properties` controls auto-`[[ ]]` wrapping. Tests rely on this — when adding new property semantics, thread them through config rather than hard-coding.
- **Type coercion in `bn set`**: `true`/`false` → bool, integer → int, `YYYY-MM-DD` → date, else string. `--type date|int|bool|raw` forces a type.
- **Stdin convention**: `-` as the sole note arg means "read paths from stdin". For `bn unset` with multiple notes, use `--` to separate notes from property names. `-0` enables NUL-separated I/O end-to-end (paths in *and* paths out from `bn find`).
- **`PersistentPreRunE` skips config loading** for `completion`, `help`, and `config` subcommands so they remain usable without a configured vault.
- Frontmatter rewrites must round-trip cleanly — preserve `Body` verbatim and only rewrite the frontmatter region. Tests under `internal/frontmatter/testdata` and `internal/cli/testdata` (golden files) catch regressions here.
