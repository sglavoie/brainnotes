# Architecture

Commands and HTTP handlers compose shared domain packages. Mutation planning
belongs in `noteops`; transactions, content history and sidecar persistence own
their respective durability guarantees. Browser controllers consume server
contracts rather than reproducing ranking, capability or configuration rules.

Start with the package you are changing. Each link contains its invariants,
important entry points and the reasons behind its boundaries. For cross-cutting
rules, read [locking](locking.md), [serving modes](serving-modes.md),
[web write classes](web-write-classes.md) and [CLI shape](cli-command-shape.md).

| Package | Responsibility |
| --- | --- |
| [cmd/bn/main.go](packages/cmd-bn-main-go.md) | CLI entry point |
| [internal/cli](packages/cli.md) | Command composition and output |
| [internal/config](packages/config.md) | Layered configuration, ownership and migration |
| [internal/vault](packages/vault.md) | Paths, walking, locks and file transactions |
| [internal/frontmatter](packages/frontmatter.md) | Ordered YAML frontmatter parsing and rewriting |
| [internal/wikilink](packages/wikilink.md) | Wikilink parsing and rewriting |
| [internal/query](packages/query.md) | Composable note predicates |
| [internal/queryexpr](packages/queryexpr.md) | Shared query expression language |
| [internal/recall](packages/recall.md) | Explainable ranking and context packets |
| [internal/agent](packages/agent.md) | Configured argv process execution |
| [internal/template](packages/template.md) | Note template discovery and rendering |
| [internal/web](packages/web.md) | HTTP handlers and write boundaries |
| [internal/web/static_src](packages/web-static_src.md) | Browser controllers and entry bundles |
| [internal/web/static_src/css](packages/web-static_src-css.md) | Stylesheet source and generation |
| [internal/web/server.go](packages/web-server-go.md) | Embedded and local static serving |
| [internal/cockpit](packages/cockpit.md) | Daily Fixes queue and review context |
| [internal/noteops](packages/noteops.md) | Shared note mutation plans and operations |
| [internal/properties](packages/properties.md) | Property statistics and schema intelligence |
| [internal/pins](packages/pins.md) | Pinned-note assignment planning |
| [internal/index](packages/index.md) | Regenerable note cache |
| [internal/vaultgit](packages/vaultgit.md) | Git content history and mutation safety |
| [internal/vaultstate](packages/vaultstate.md) | Durable sidecar persistence |
| [internal/vaultsync](packages/vaultsync.md) | Synchronization protocol and enrollment |
| [internal/vaultexport](packages/vaultexport.md) | Committed-history package production |
| [internal/vaultimport](packages/vaultimport.md) | Package validation, conflict planning and application |
| [internal/vaultsyncd](packages/vaultsyncd.md) | Background synchronization and status |
| [internal/audit](packages/audit.md) | Direct-write invariant checks |
| [internal/vaultfixture](packages/vaultfixture.md) | Isolated vaults, clocks and listeners for tests |
| [internal/syncthingbin](packages/syncthingbin.md) | Pinned transport binary and real transport harness |
| [internal/mutationmatrix](packages/mutationmatrix.md) | End-to-end mutation safety coverage |

Contributor setup and validation live in [Development](../development.md).
