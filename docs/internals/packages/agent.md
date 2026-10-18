# internal/agent

[Back to the architecture map](../architecture.md).

- `internal/agent` — shared argv-only process runner. Callers own context timeouts; stdin/stdout remain byte-exact; diagnostics are bounded and sanitized. There is no shell, provider SDK, persistence, or implicit network behavior. `bn context --agent` sends its canonical Markdown preview, while the web daily-review path sends only its separately redacted review payload.
