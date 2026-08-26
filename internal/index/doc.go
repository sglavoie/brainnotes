// Package index is a persistent cache of note bytes, keyed by the revision
// they were read at. It exists so a repeated vault load pays a stat per note
// instead of a read per note.
//
// # What it does not save
//
// The parse. Every Load turns each walked note's bytes into a fresh
// *vault.Note, cached or not, so a warm load still runs frontmatter.Parse once
// per note — see the entry type for why sharing a parsed Note across snapshots
// is not on the table. BenchmarkDirectLoad, BenchmarkColdIndexLoad and
// BenchmarkWarmIndexLoad time the three paths over a 500-note vault, and
// TestLoadCostsAreOneReadPerMissAndOneParsePerNote counts the reads and the
// parses each one performs.
//
// # Layering
//
// index may import vault, config and frontmatter and nothing else from this
// module. It consumes a *vault.Vault and produces []*vault.Note, then stops —
// the same one-way contract internal/properties holds by deliberately not
// depending on internal/web. It must never import query, queryexpr,
// properties, schema, noteops, cockpit or web: every one of those is a
// consumer of the note slice this package hands back, and importing one would
// turn a leaf cache into a layer with opinions about what the notes are for.
// TestPackageLayering pins that, over non-test files only — the parity test
// deliberately reaches up into queryexpr to evaluate real queries against both
// load paths, which is a test-only dependency and creates no import cycle.
//
// # On-disk state
//
// State lives in <vault>/.bnotes/index/ — a subdirectory rather than loose
// files in .bnotes/ — for two reasons. index.lock must stay out of
// .bnotes/locks/, whose filenames are sha256(vault-relative path) and belong
// to the note namespace; a lock file there that is not a note's would be an
// alias waiting to be misread. And a one-directory footprint makes
// `rm -rf .bnotes/index` the whole documented repair.
//
// # Self-healing
//
// This package deliberately inverts the precedent set by internal/recall's
// state store, which fails closed and never auto-repairs (see
// docs/internals/recall-context.md). That is right for recall.json, which
// holds irrecoverable user intent — dismissals, snoozes, open history — that
// no rebuild can reconstruct. The index holds zero user intent: every byte in
// it is a pure function of the vault on disk. So a state file this package
// cannot trust is discarded and rebuilt silently, and two rules follow from
// that:
//
//   - An index failure is never a request error. Anything unreadable,
//     undecodable, versioned wrong, rooted wrong, or internally inconsistent
//     degrades to a direct load of the vault, logged once per failure class
//     per process.
//   - Disk-write failures are ignored entirely. A read-only vault or a full
//     disk costs the next process its warm start; it does not make this one
//     wrong.
//
// # Locking
//
// The three rules are stated in full in docs/internals/locking.md; the short
// form is that the index never enters the vault lock hierarchy, its own lock
// is a try-lock that skips the disk write on contention, and it is never
// refreshed inside a transaction.
package index
