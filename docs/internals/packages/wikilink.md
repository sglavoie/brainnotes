# internal/wikilink

[Back to the architecture map](../architecture.md).

- `internal/wikilink` — Obsidian-style `[[Target#Heading|Alias]]` parsing and rewriting. The table form `[[Target\|Alias]]` parses with the backslash dropped from `Target` (or `Heading`) and `Link.EscapedPipe` set; rewrites go through `Link.WithTarget`, which writes the `\|` back, rather than `FormatLink`. **Code-block aware**: links inside fenced, indented, or inline code are masked out so rewrites never touch documentation samples or code snippets. Used by `rename` (rewrite all forms when title changes) and `move` (rewrite folder-prefixed forms only — bare `[[Title]]` links resolve by title alone in Obsidian and are deliberately left untouched).
