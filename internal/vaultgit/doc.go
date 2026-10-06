// Package vaultgit is the internal Git safety engine for a note vault.
//
// It exists because of one incident: a note was lost when a write vacated its
// destination path and synchronizing storage propagated the vacancy back as a
// deletion. `internal/vault` now refuses to vacate a path; this package is the
// layer below that, the one that makes every version of every note recoverable
// even when a write, a filesystem, or an external tool does something nobody
// anticipated. It snapshots the vault into a local Git history after every
// change and pushes that history to a private remote.
//
// It is not a Git porcelain and has no user-facing surface. Nothing here reads
// or writes the vault's notes: it stages exact paths, builds a tree, writes a
// commit, and moves one ref.
//
// # The six invariants
//
//  1. **External changes, including deletions, are recoverable in Git.**
//     Missing tracked regular files are recorded automatically. Older commits
//     retain their content; no journal is needed to delete from an editor or
//     another device. Journaled operations additionally preserve pre-mutation
//     bytes for undo. Type changes and ambiguous repository states still refuse.
//
//  2. **A local safety failure is durable and fails closed.** A classification
//     ambiguity, a ref compare-and-swap that cannot
//     be retried safely, or a failed local commit writes `blocked.json`, and
//     [Manager.Preflight] refuses every later mutation until a person clears it.
//     If writing the marker itself fails, the condition that caused it is still
//     on disk, so the next preflight re-derives the same verdict from the
//     working tree. The marker is an optimization for reporting, never the
//     mechanism.
//
//  3. **Remote availability is not a correctness input.** A push failure is
//     retried with backoff and recorded in `push.json`. It never writes
//     `blocked.json` and never prevents a local commit, because a vault that
//     stops accepting edits when GitHub is down is a worse vault. Conversely,
//     [Manager.Push] is never called from the immediate mutation path — a note
//     save must not wait on a network round trip.
//
//  4. **The user's index is theirs.** Every operation stages into a private
//     temporary index seeded from HEAD and deleted afterwards, so a snapshot can
//     never absorb whatever the user happened to have staged, and a `git add`
//     they ran by hand is never committed by us. After a successful commit the
//     real index is refreshed for the exact committed paths, and only for the
//     paths whose real-index entry still matched the *old* HEAD — a path the
//     user deliberately staged is left exactly as they left it. That refresh is
//     cosmetic (it keeps `git status` from showing our own commit as a staged
//     revert) and its failure is never an operation failure.
//
//  5. **Nothing from a note reaches a command line, a commit message, or a
//     log.** Git is invoked as an argv array with a controlled environment and
//     never through a shell. Paths are handed to `update-index` NUL-delimited on
//     stdin, so a leading dash, a newline, or a comma in a note name is data
//     rather than syntax. Commit messages are `brainnotes: <operation>
//     [<journal-id>]` and `brainnotes: autosave <RFC3339 UTC>` — fixed shapes
//     with a sanitized operation token. In particular `journal.Entry.Command`
//     holds the full `bn` invocation, including note names and property values,
//     and must never be used to build a message.
//
//  6. **An interrupted snapshot is distinguishable from one that never
//     started.** Invariant 2 covers failures the engine *observed*; a process
//     that is killed observes nothing and writes nothing, and the state it
//     leaves behind would otherwise look exactly like a healthy vault. So every
//     snapshot writes and fsyncs `intent.json` before its first index write,
//     rewrites it with the new commit's object name *before* `update-ref` is
//     attempted, and removes it only after the ref has moved and the directory
//     naming it is durable. A record found at startup means an operation began
//     and never finished, and mutations stay refused until recovery proves the
//     recorded commit is reachable from the branch — or a person clears it with
//     [Manager.ClearBlock]. Proof is reachability, not equality, so a later
//     commit by another writer does not un-land the snapshot underneath it.
//     There is no second way to prove it, which is why an unreadable record
//     refuses rather than self-heals. See intent.go.
//
// # Locking
//
// All operational state lives under `<git-dir>/brainnotes-safety/`, which is
// inside `.git` and therefore never tracked. Two flock-based locks live there:
// `safety.lock` serializes mutation snapshots and `push.lock` serializes remote
// attempts. Both are leaf locks:
//
//   - This package never acquires a vault lock. It calls no `vault.LockPaths`
//     and no `FileTxn`, so it cannot participate in a cycle with the note lock
//     hierarchy, and a caller may hold vault path locks across a call into here.
//     The ordering rule is one-way and stated positively: **vault locks first,
//     safety lock last, never the reverse.**
//   - The two locks are never held at the same time. That is what keeps a slow
//     push (a network round trip with no useful bound) from stalling the
//     immediate mutation path, which is the deadlock the watcher and the
//     mutation path would otherwise contend for.
//   - [Open] takes `safety.lock` in exactly one case: a recovery intent is
//     present and has to be adjudicated. It is a wait-free acquisition like
//     every other in this package, and a busy lock means the record belongs to
//     a live operation, so opening leaves it alone. On a healthy vault Open
//     still takes nothing and mutates nothing.
//   - `safety.lock` — and only it — is taken behind the vault's checkpoint
//     barrier, which sits above every lock named here and every lock
//     internal/vault owns. Everything that lock guards writes the content
//     history, and a copy taken between a note landing and its commit holds a
//     change the history does not explain. The full order is stated once, in
//     internal/checkpoint: barrier, then vault locks, then safety lock. The
//     push lock stays outside it, because a push is a network round trip that
//     changes nothing on this disk and an unreachable remote would otherwise
//     exhaust every checkpoint's drain budget.
//
// # Scope
//
// [InScope] is a fixed, in-code policy — not a read of `.gitignore` — deciding
// which vault-relative paths BrainNotes may *add* to the history. Anything
// already tracked in HEAD is in scope regardless, so a path can never fall out
// of the history because a policy narrowed underneath it. See scope.go.
//
// # What a snapshot may leave out
//
// Exactly one thing, and only on one entry point. [Manager.CommitPending] scans
// the working tree and offers to record whatever it finds, so an addition it
// cannot read comes back as a [SkippedPath] rather than a failure: nobody asked
// for that path in particular, and leaving it out costs the history nothing it
// held a moment ago.
//
// Every other entry point is a caller naming paths — "these exact paths are now
// the truth" — and those snapshots fail closed instead. Recording some of a
// named set would answer a question nobody asked and hand the caller a success
// the history does not back, which is the failure this package exists to
// prevent. [CommitResult.Unrecorded] is how a caller confirms that for itself.
//
// A path the engine can read and Git still refuses to stage is neither case: it
// is the repository failing, it would hit the rest of the pass too, and it is
// fatal everywhere, external scan included.
package vaultgit
