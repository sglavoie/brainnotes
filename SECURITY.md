# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through
[GitHub private vulnerability reporting](https://github.com/sglavoie/brainnotes/security/advisories/new),
not in a public issue. Include the `bn --version` output, the serving mode
(local, `--hosted` or `--read-only`), and steps to reproduce.

You should get an acknowledgement within a week. Fixes land on `main`; there
are no maintained release branches.

## Scope and threat model

`bn` operates on a vault you own. Reports are most useful where it breaks one
of the guarantees it does make:

- **Vault integrity:** a write that bypasses the transaction, lock or
  storage-location guards, loses note bodies, or applies something other than
  what was previewed. See [Locking](docs/internals/locking.md) and
  [Web write classes](docs/internals/web-write-classes.md).
- **Browser server:** CSRF or same-origin bypass, a route that runs a process
  in `--hosted` mode, a write in `--read-only` mode, path traversal out of the
  vault, or an attachment served in a way a browser would execute. See
  [Serving modes](docs/internals/serving-modes.md) and
  [Web server security](docs/web-server-security.md).

`bn serve` has **no authentication**. Exposing it beyond loopback without an
authenticated reverse proxy or tunnel in front of every path is a
configuration problem, not a vulnerability. The same applies to an
`[agent].command` or editor command you configure yourself: `bn` runs what you
tell it to.
