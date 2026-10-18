# internal/frontmatter

[Back to the architecture map](../architecture.md).

- `internal/frontmatter` — parse/rewrite YAML frontmatter. `Map` preserves insertion order during parse but `Marshal` **always emits keys alphabetically sorted** — this is intentional and load-bearing for stable diffs. Values are Go-native types (string, int, float64, bool, time.Time, []any, map[string]any). Wikilink-typed properties are auto-wrapped in `[[ ]]` on write.
