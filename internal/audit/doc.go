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
// Test files are exempt entirely. A fixture writes to a temp directory that is
// not a vault and has nothing to protect.
package audit
