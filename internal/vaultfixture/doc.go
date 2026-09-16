// Package vaultfixture builds the disposable, isolated vaults that verification
// runs are allowed to write to.
//
// It exists because the isolation rules were previously expressible only inside
// one package's test files. internal/cli/vault_isolation_test.go enforced them
// for CLI tests, scripts/vault_sandbox.py enforced them for manual runs, and
// every other package rebuilt its own fixture by hand — so a package that
// forgot a rule simply did not have it. Two-vault synchronization work needs
// the same fixtures from several packages at once, which is the point at which
// "copy the helper" stops being viable.
//
// Four things are isolated here, and each one is a way a verification run has
// or could reach something it must not:
//
//   - The vault environment. $BNOTES_VAULT overrides a working directory, so
//     `cd $scratch && bn ...` is not isolation. ScrubEnv removes it; Vault.Use
//     points it at a fixture and proves the process resolves that fixture.
//   - Storage. Every fixture is a real Git repository under the system
//     temporary directory, and IsolationError refuses anything that is not.
//   - Time. Clock is explicit and moves only when a test advances it, so
//     ordering assertions do not depend on how long a test took to run.
//   - Ports. ListenLoopback binds 127.0.0.1:0 and verifies the bound address is
//     loopback, so a fixture server is never reachable off the machine and two
//     fixtures never contend for a fixed port.
//
// Everything here takes a *testing.T. The package is imported only by tests.
package vaultfixture
