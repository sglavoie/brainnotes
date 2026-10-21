# Contributing

Thanks for your interest in Brain Notes. Bug reports, documentation fixes and
focused pull requests are all welcome. For a larger change, open an issue first
so we can agree on the shape before you invest the time.

## Getting set up

[Development](docs/development.md) lists the prerequisites (Go, just, Node.js,
Python 3, golangci-lint) and the first commands to run. The
[architecture overview](docs/internals/architecture.md) maps the packages and
links to the contract for each area. Read the contract for the area you change.

## Ground rules

- **Never test a mutation against a real vault.** `BNOTES_VAULT` overrides your
  working directory. Use `just vault-sandbox -- <bn args>`,
  `internal/vaultfixture`, or the browser fixtures.
- **Run `just ci` before opening a pull request.** Browser changes also need the
  relevant `npm run test:e2e` suite. The
  [pull request template](.github/pull_request_template.md) lists the gates per area.
- **Edit TypeScript and CSS under `internal/web/static_src`**, then run
  `just web-build`. The bundles under `internal/web/static` are committed and
  CI checks they are up to date.
- **Do not loosen the complexity ratchet** (thresholds or baseline) to make a
  change pass. Split the function instead. See
  [Refactor baseline](docs/internals/refactor-baseline.md).
- **Keep docs aligned with behavior.** A user-visible change updates the README
  or the matching page under `docs/`.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/)
(`feat(web): …`, `fix(cli): …`, `docs: …`), as in the existing history.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
