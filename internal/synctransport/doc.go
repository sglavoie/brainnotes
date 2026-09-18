// Package synctransport runs the real transport between two fixture vaults.
//
// Everything else in this repository's synchronization stack is tested against a
// transport that is a function call: internal/vaultsyncd's fixture copies one
// directory onto another and mirrors removals, synchronously, in the test's own
// process. That fixture is honest about what it is and it cannot be wrong about
// timing, because it has none. It is also the only place where this design could
// be wrong in a way no test would notice, because every claim the architecture
// makes about the transport — that packages arrive in an order nobody chose,
// that a disconnected peer is a delay rather than a failure, that a restart
// resumes, that the device which published a package is the only one whose
// removal of it means anything — is a claim about a program this repository does
// not write.
//
// So this package starts two actual Syncthing processes, on loopback, each with
// its own identity, database and configuration, and points them at two
// disposable vaults. What they carry is produced by internal/vaultexport and
// consumed by internal/vaultimport, unmodified. The tests that use it assert on
// vault contents, which is the only evidence that survives being wrong about
// Syncthing.
//
// # The shape that is being asserted
//
// Each device owns exactly one channel. Its own `.bnotes/sync/outgoing` is
// declared to Syncthing as a send-only folder, and the same folder arrives at
// its peer as `.bnotes/sync/incoming`, declared receive-only. There are two such
// folders, one per direction, and no third. A live vault directory is never
// named in a Syncthing configuration at all — not the notes, not the Git
// repository, not `.bnotes` above `sync` — and
// TestRealTransportNamesOnlyStagingDirectories asserts that by reading the
// configuration back out of the running processes.
//
// The directionality is what makes retirement mean something. A receive-only
// folder that loses a file locally does not tell its peer to lose it: Syncthing
// records the difference as a local deviation and keeps offering the file. That
// is exactly the semantics internal/vaultsyncd's retirement worker needs, and it
// is why a receiver deleting a package it has applied is not a way to retire it.
// TestRealTransportCleanupIsSenderOwned is that sentence as a test.
//
// # Why this is not in `just ci`
//
// These tests spend most of their time waiting for two other processes, and they
// need a binary that has to be downloaded once. They are behind the
// `synctransport` build tag and run from `just test-sync-transport`, which
// provisions the binary first and hands the path over in BNOTES_SYNCTHING_BIN.
// Only the test files carry the tag; this package does not, so gofmt, go vet and
// the complexity ratchet cover the harness on every ordinary run. A test that
// cannot find the binary fails, and says how to get one — it never skips, because
// a gate that quietly passes when its subject is absent is not a gate.
package synctransport
