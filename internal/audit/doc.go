// Package audit holds source-level invariants that no runtime test can hold.
//
// It has no production code. What lives here is the check that keeps the vault's
// safety contract from being routed around: a test that reads every production
// Go file in the repository and fails when a new one writes to the filesystem
// directly instead of going through a layer that enforces the storage-location
// policy, the path-continuous replacement, and the Git snapshot.
//
// # The direct-write annotation
//
// A production file outside the approved persistence packages may still need to
// touch the filesystem — a rename is a rename, and no wrapper can perform one
// for you. Those calls are allowed, but they must say so:
//
//	//bn:direct-write <reason>
//
// The marker goes either on the line immediately above the call, or in the doc
// comment of the function containing it, which is the usual choice when a
// function's whole job is one guarded write. The reason is for the next reader,
// and it should answer the only question worth asking here: what already
// guaranteed this write is allowed to happen? Naming the guard — "guarded by
// BeginOperation above", "inside a FileTxn that holds the lock" — is a good
// answer. "Needed to create the file" is not.
//
// The approved packages carry the guarantee themselves and need no annotation:
// internal/vault owns the location policy and the replacement primitives,
// internal/vaultgit owns the content history, internal/vaultstate owns durable
// sidecar state, internal/journal owns the undo manifests, internal/index owns a
// regenerable cache, and internal/backup owns archive extraction. Adding a
// package to that list is a decision about where safety lives, so it is made by
// editing approvedPackages with a reason, not by annotating call sites one at a
// time.
//
// # The checkpoint barrier
//
// The second invariant here is internal/checkpoint's participant list, which is
// the same kind of claim: a checkpoint promises that a copy of the vault holds
// no half-finished write, and that promise is only as true as the set of writers
// that agree to join the barrier. A writer that stops joining removes the
// promise for the whole vault while every test of the barrier itself keeps
// passing, and nothing shows it until a copy taken during that write is
// restored.
//
// So barrierDoors names every door, with a sentence saying what would be caught
// half-done without it, and the check runs in both directions: every listed door
// must still reach the barrier, and every production call to checkpoint.Join,
// checkpoint.JoinContext, or checkpoint.Hold must be inside a listed door. A new writer package is
// therefore a prompt to write that sentence rather than a silent gap.
//
// # The journal handoff annotation
//
// noteops.BeginOperation joins the barrier and hands the caller a hold that
// lasts until Release, so every call site needs a deferred release. A
// constructor that opens a journal and returns it cannot defer anything, and
// says so:
//
//	//bn:journal-handoff <who releases it>
//
// Same shape and same limits as the direct-write marker: it asks for a name, not
// for a proof. What it prevents is the obligation quietly disappearing — a
// forgotten Release does not fail anything, it leaves that vault's barrier
// permanently unavailable, so the next backup drains until it times out with
// nothing to say about which command is responsible.
//
// Test files are exempt entirely. A fixture writes to a temp directory that is
// not a vault and has nothing to protect.
package audit
