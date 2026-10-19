package vaultimport

import (
	"context"
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
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
const lockFileName = vaultsync.ImportLockName

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
//
// It does take the checkpoint barrier, and takes it first, which is the order
// internal/checkpoint documents: the barrier sits above everything internal/vault
// owns, and joining it after this guard — or after any vault lock — is what would
// close a cycle against a checkpoint's settle pass. One import is therefore one
// join. That is wider than a single durable step and deliberately so: an import
// is a sequence of transitions whose intermediate states are meaningful only to
// the recovery pass, and a copy taken inside one would capture an attempt record
// rather than an import. It is still narrower than a synchronization pass, which
// is three separately re-drivable operations and stays three separate joins.
//
// A released import drops the barrier as well, so a caller that already defers
// release owes nothing new.
func lockImports(root string) (ctx context.Context, release func(), acquired bool, err error) {
	return lockImportsContext(context.Background(), root)
}

// lockImportsContext is lockImports for a caller that already owns the
// checkpoint admission — re-enrollment holds the checkpoint itself, and a fresh
// join from context.Background would wait on that hold rather than ride it.
func lockImportsContext(parent context.Context, root string) (ctx context.Context, release func(), acquired bool, err error) {
	noop := func() {}
	joinedCtx, barrier, err := checkpoint.JoinContext(parent, root)
	if err != nil {
		return context.Background(), noop, false, fmt.Errorf("vaultimport: %w", err)
	}
	guard, acquired, err := vault.TryLockPath(lockPath(root))
	if err != nil || !acquired {
		barrier()
		return context.Background(), noop, acquired, err
	}
	return joinedCtx, func() { guard(); barrier() }, true, nil
}

// lockPath is the guard's absolute path, derived from vaultsync.StateDir for the
// reason StatePath is: a literal `.bnotes/sync/` beside a derived one would let
// the two drift apart.
func lockPath(root string) string {
	return vaultsync.StateLockPath(root, lockFileName)
}
