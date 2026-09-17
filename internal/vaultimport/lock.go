package vaultimport

import (
	"path"
	"path/filepath"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// lockFileName is the guard every import in a vault takes, beside the record it
// guards.
//
// It lives in the synchronization state directory rather than in `.bnotes/locks`
// because it is not a note-path lock: it excludes *whole imports*, not writers of
// one file, and the directory it sits in is the one this package already owns and
// the content history already excludes.
const lockFileName = "import.lock"

// lockImports takes the vault's import guard without waiting.
//
// Two properties make this the right primitive, and both are load-bearing.
//
// It is wait-free, so a second import refuses instead of queuing. That is not a
// convenience: whoever holds this lock may be halfway through an application, and
// a waiter would come out of its wait into a vault whose state file has changed
// underneath the package it already read.
//
// And it is a *liveness* test. The unresolved attempt in the state file cannot
// say whether the process that wrote it is still running — that is exactly what a
// crash makes unknowable — so recovery needs a second signal, and a flock is the
// one signal an operating system withdraws when a process dies. A held lock means
// a live import and the attempt is none of our business; a free lock beside a
// pending attempt means the writer is gone and the attempt is ours to resolve.
//
// It deliberately does not go through vault.LockPaths. That call takes the shared
// vault guard, and a large package's transaction escalates to the *exclusive*
// guard, which the shared one this call would be holding blocks — the whole
// import would deadlock against itself. vault.TryLockPath reaches the same flock
// primitive with no guard above it.
func lockImports(root string) (release func(), acquired bool, err error) {
	return vault.TryLockPath(lockPath(root))
}

// lockPath is the guard's absolute path, derived from vaultsync.StateDir for the
// reason StatePath is: a literal `.bnotes/sync/` beside a derived one would let
// the two drift apart.
func lockPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), lockFileName)))
}
