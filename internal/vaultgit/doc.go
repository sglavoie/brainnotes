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
// # The five invariants
//
//  1. **A deletion is never committed unless something proves it was intended.**
//     Additions and modifications only ever add content to the history, so they
//     need no proof. A deletion removes the last copy from the tip, so it needs
//     one: an *uncommitted* finalized BrainNotes journal record whose `Before`
//     hash equals the content the deletion would drop and whose `After` state is
//     `missing`. Anything else — an unexplained delete, a file replaced by a
//     directory, an unmerged index, a rename Git reported ambiguously — blocks.
//     See [Manager.CommitPending] and classify.go.
//
//  2. **A local safety failure is durable and fails closed.** An unexplained
//     deletion, a classification ambiguity, a ref compare-and-swap that cannot
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
//
// # Scope
//
// [InScope] is a fixed, in-code policy — not a read of `.gitignore` — deciding
// which vault-relative paths BrainNotes may *add* to the history. Anything
// already tracked in HEAD is in scope regardless, so a path can never fall out
// of the history because a policy narrowed underneath it. See scope.go.
package vaultgit
