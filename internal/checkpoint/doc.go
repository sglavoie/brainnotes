// Package checkpoint bounds the window in which a vault can be copied whole.
//
// # What a checkpoint promises
//
// While a checkpoint is held, every write that returned success to its caller is
// durable and complete, and no write is part-way through its own steps. Work an
// interrupted process left behind is either finished — internal/vault rolls a
// prepared transaction forward unconditionally, and Hold drives that pass before
// it returns — or represented by its own durable recovery record, which the
// copy captures along with everything else. A vault restored from such a copy
// therefore either holds a change or does not, and in the second case it holds
// the record that says how to finish or undo it.
//
// That is a statement about acknowledged work, not about wall-clock instants. A
// write that was still running when the checkpoint was announced is waited for,
// not cut off; a write that starts afterwards waits for the checkpoint. Nothing
// is lost either way, and no writer is stranded: both waits are bounded and both
// ends report a retryable refusal rather than blocking forever.
//
// # Why a plain lock is not enough
//
// The barrier is one flock that writers hold shared and a checkpoint holds
// exclusive. flock has no writer preference, so on a busy vault the exclusive
// acquisition could be starved indefinitely by a stream of overlapping shared
// holders. The durable record at .bnotes/checkpoint.json is what closes that:
// a checkpoint announces its intent there first, and a writer that has not yet
// joined waits for the announcement to clear before it tries the flock. Writers
// already inside finish, the shared holders drain to zero, and the exclusive
// acquisition completes. Correctness comes from the flock; liveness comes from
// the record.
//
// # Why there is a second lock
//
// The record alone cannot say whether the client that wrote it is still alive,
// and try-locking the barrier cannot tell "a checkpoint holds it exclusively"
// apart from "another writer holds it shared". So a checkpoint client also holds
// checkpoint.claim.lock exclusively for its whole life, and writers only ever
// try-lock it. A record beside a free claim lock is the leavings of a dead
// client: writers ignore it and proceed, and the next Hold — which owns the
// claim lock and is therefore the only authority on the matter — overwrites it.
// This is the same shape internal/vaultimport uses for import.lock and
// import.json, and for the same reason.
//
// # Lock order
//
// The barrier sits above everything internal/vault owns:
//
//	checkpoint.claim.lock  (checkpoint clients only, try-locked by writers)
//	checkpoint.lock        (shared by writers, exclusive by a checkpoint)
//	vault.lock             (shared, or exclusive for an oversized commit)
//	per-path locks         (sorted order)
//	the Git safety lock    (last, and never the reverse)
//
// A writer joins the barrier before it takes any vault lock, which is what keeps
// the order acyclic: a checkpoint holding the barrier exclusively takes vault
// locks during its settle pass, and a writer holding vault.lock exclusively
// while waiting on the barrier would close the cycle.
//
// # Re-entrancy
//
// Joining is re-entrant per vault root within one process, counted rather than
// nested: bn serve runs concurrent writers in one process, and an import calls
// through noteops.BeginOperation from inside a join it already holds. The first
// joiner takes the shared flock and the last releaser drops it. Hold refuses
// with ErrNested when the calling process is already inside a join, because the
// exclusive acquisition would otherwise wait out its drain budget against its
// own shared holder.
//
// # What participates
//
// Every door through which this program makes a durable change joins the
// barrier: journaled note mutations (noteops.BeginOperation), configuration and
// activity sidecars (vaultstate.Save), upload staging (internal/attachments),
// content history written by the autosave watcher (vaultgit.CommitPending),
// synchronization state (vaultexport, vaultsync enrollment, vaultsyncd worker
// records), and imports (vaultimport). TestEveryWriterJoinsTheBarrier is what
// keeps that list from silently losing a member.
//
// A synchronization pass is deliberately not one join. A pass is import, then
// export, then retirement, and each of those is separately durable and
// separately re-drivable by design; holding the barrier across all three would
// let one slow network round-trip exhaust every checkpoint's drain budget. The
// contract is about steps, not about passes.
package checkpoint
