# internal/web/server.go

[Back to the architecture map](../architecture.md).

- `internal/web/server.go` serves embedded static files by default, but accepts an injected static filesystem. `internal/cli/serve.go` uses a `sourceCheckoutRoot` value stamped via justfile `-ldflags` first, then current-directory checkout detection, and injects disk static assets after a successful web build so the current `bn serve` process sees freshly generated browser JavaScript. It regenerates the stylesheet through `cssbuild.WriteBundle` before invoking esbuild, on a path that does not depend on the JavaScript build running at all, so a checkout with no Node toolchain still serves a current `app.css`.
