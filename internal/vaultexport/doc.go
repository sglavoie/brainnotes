// Package vaultexport turns this vault's committed content history into
// packages the peer replica can apply.
//
// It is the producer half of synchronization. internal/vaultsync decides what a
// package is and who may accept one; internal/vaultgit owns the committed
// history; this package is the only place that reads the second and writes the
// first, which is why it sits above both rather than inside either. It never
// applies anything and never writes into the incoming channel.
//
// Five properties define it, and each is a refusal rather than a convention.
//
// Only committed bytes travel. Every byte a package carries is read from a Git
// blob through internal/vaultgit's read-only History, never from the working
// tree. A note being typed, a file a transaction has not yet renamed into place,
// and an external edit the autosave schedule has not picked up are all invisible
// here — not filtered out, but unreachable. That is the whole of "uncommitted
// edits remain pending", and it holds without this package knowing anything
// about editors, timers, or locks.
//
// Observing history does not disturb it. The exporter opens vaultgit.History,
// not vaultgit.Manager. It takes no safety lock, resolves no recovery intent,
// touches no private index, and never calls into the autosave watch state, so
// the schedule on which external edits are snapshotted is exactly what it would
// have been had no export run. An exporter that had to commit before it could
// read would make synchronization a reason for the vault's own history to
// change shape, and the two must stay independent.
//
// One commit is one operation. A commit is the vault's own record that a set of
// paths changed together — internal/noteops commits exactly one journal entry's
// paths — so the operation boundary a peer needs already exists and is not
// re-derived here. Packages are never batched across commits: the protocol
// refuses a package that changes one path twice, and merging two commits that
// both touched a note would either lose that refusal or hit it as a hard
// failure. The cost is one package per commit; the benefit is that a boundary
// this package publishes is a boundary the vault actually recorded.
//
// Origin provenance is durable and derived. An operation's identity is derived
// from the vault identity and the commit object name rather than minted, so a
// producer that published and then crashed before recording the fact
// republishes the same operation under the same identity. Causal predecessors
// come from a persisted per-path ledger of which operation last touched each
// path, so ordering is causal and never temporal. An operation that arrived from
// the peer is recorded in that same ledger and never exported back: applying a
// package produces a commit like any other, and without the ledger the exporter
// would faithfully send the peer its own change back.
//
// Private state never travels. Every path is filtered through
// vaultsync.Portable, which excludes `.bnotes/` wholesale — including this
// package's own cursor, the undo journal that authorizes its deletions, and the
// channels themselves. The exporter's state file lives under `.bnotes/sync/` for
// exactly that reason: it is inside the directory whose exclusion is the
// portable policy's first rule.
//
// Three things halt rather than degrade. A history rewrite — a cursor the branch
// no longer contains, a branch that changed under the cursor, a merge in a range
// that has to replay linearly — is refused, because the alternative is telling
// the peer a story the vault no longer tells. A deletion travels only when the
// commit that performed it carries the journal entry proving it was intended,
// which is the same evidence internal/vaultgit demands before it will commit a
// deletion at all; here it is read back out of the commit rather than off the disk,
// so the proof and the deletion are the same durable object. And two paths that
// are one name on a case-folding or normalizing replica never both travel —
// whether they are one file there, or one of them a file and the other a
// directory the second needs. vaultsync refuses both pairs inside one package,
// and this producer refuses them across packages too, by asking the path ledger
// what it has already sent. That one is the halt no edit at the tip clears — the
// clash is in a commit that already happened — so it names both paths and asks
// for a rename.
package vaultexport
