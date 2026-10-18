# internal/template

[Back to the architecture map](../architecture.md).

- `internal/template` — discovery and rendering for `bn new`. Template lookup is case-insensitive and tries `<name>`, `<name>.md`, `<name> Template.md`. Render helpers: `now`, `today`, `dateFormat`, `slug`, `title`, `lower`/`upper`, `default`, `env`, `wikilink`. The `.Vars` map holds repeatable `--var key=value` pairs.
