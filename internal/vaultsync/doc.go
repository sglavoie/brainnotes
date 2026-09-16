// Package vaultsync owns the synchronization protocol and the durable state
// that two enrolled replicas of one vault exchange changes through.
//
// It is a leaf: it imports internal/config (for the enrollment marker path it
// shares with the configuration loader), internal/vault (for the storage
// location guard, the directory fsync, and — for the enrollment record alone —
// the path-continuous replacement), and internal/textnorm. It imports nothing
// above them, and in particular it does not import internal/vaultgit,
// internal/journal, or internal/noteops — deciding what a package *contains*
// must not depend on what the content history happens to track.
//
// # What is here
//
//   - The enrollment record under <vault>/.bnotes/sync/enrollment.json: the
//     vault identity both replicas share, this replica's own device identity,
//     the enrollment epoch, and the peers this replica accepts from.
//   - The wire format, [Format]: an immutable, self-describing package carrying
//     operation identity, origin sequence, per-path causal predecessors,
//     expected existence and content hashes, resulting hashes, a payload
//     inventory, and dependencies on earlier packages.
//   - The portable-content inclusion policy, [Classify]: which vault paths may
//     travel between devices at all.
//   - Durable, immutable package publication into a device's outgoing channel.
//   - Receipts, which acknowledge *durable application* and nothing weaker.
//
// # What is deliberately not here
//
// No transport. Syncthing moves the channel directories and never writes a live
// vault; this package never opens a socket and never shells out.
//
// No application. Nothing here writes a note, resolves a conflict, or touches
// the content history. A package is read, validated, and handed on; the layer
// that applies it owns the journal entry that makes the application undoable,
// and hands the identity of that entry back here to mint a receipt.
//
// No authentication. Device pairing is Syncthing's trust boundary. The identity
// checks here answer "is this package addressed to this vault, from a device
// this vault enrolled, under the epoch this vault is living in" — which is a
// routing and staleness question, not a cryptographic one. A receiver must not
// read [Manifest.OriginDevice] as proof of who produced the bytes.
//
// # The portable scope is not the Git scope
//
// internal/vaultgit decides what BrainNotes may add to the vault's *local*
// content history, and it deliberately includes machine-local material: the
// brief, cockpit, and recall state files, and every undo-journal entry. Those
// belong in a local history — they are how a bad day is recovered — and they
// must never travel, because they describe one machine's activity rather than
// the vault's content. [Classify] is therefore a separate policy with a
// narrower answer, and two tests pin the divergence so the two cannot be quietly
// unified: TestPortableScopeIsNarrowerThanTheGitScope names each machine-local
// path and asserts both directions of it, and TestPortableImpliesTracked asserts
// the containment over every path either test knows about, so a new portable
// path that the content history does not track fails without anyone having to
// remember to add a case.
package vaultsync
