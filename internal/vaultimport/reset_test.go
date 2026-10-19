package vaultimport

import (
	"context"
	"errors"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestResetAfterReviewRefusesARecordHoldingAnInterruptedApplication is the
// discard re-enrollment must not make. The attempt is the only thing that can
// roll a half-applied package back; an empty record written over it leaves the
// peer's bytes on the disk with nothing to explain them.
func TestResetAfterReviewRefusesARecordHoldingAnInterruptedApplication(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	interruptAfterCommit(t, r, dir)
	pending := importState(t, r).Pending

	if err := RequireResolved(r.root()); !errors.Is(err, ErrUnresolvedImport) {
		t.Fatalf("RequireResolved over a pending attempt: got %v, want ErrUnresolvedImport", err)
	}
	if err := ResetAfterReviewContext(context.Background(), r.root()); !errors.Is(err, ErrUnresolvedImport) {
		t.Fatalf("reset over a pending attempt: got %v, want ErrUnresolvedImport", err)
	}
	if got := importState(t, r).Pending; got == nil || got.JournalEntry != pending.JournalEntry {
		t.Fatalf("the refused reset changed the attempt: %+v", got)
	}

	if _, err := Recover(r.root(), Options{Now: r.clock.Now}); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if err := RequireResolved(r.root()); err != nil {
		t.Fatalf("RequireResolved after recovery: %v", err)
	}
	if err := ResetAfterReviewContext(context.Background(), r.root()); err != nil {
		t.Fatalf("reset after recovery: %v", err)
	}
	if state := importState(t, r); len(state.Applied) != 0 || state.Pending != nil {
		t.Fatalf("reset left %+v", state)
	}
}

// TestResetAfterReviewTakesTheImportGuard pins that the reset is one more
// writer of import.json and so excludes imports the way they exclude each other:
// a reset an import could interleave with would be written over by that
// import's next state write.
func TestResetAfterReviewTakesTheImportGuard(t *testing.T) {
	r := enroll(t)
	release, acquired, err := vault.TryLockPath(lockPath(r.root()))
	if err != nil || !acquired {
		t.Fatalf("hold the import guard: acquired=%v err=%v", acquired, err)
	}
	defer release()
	if err := ResetAfterReviewContext(context.Background(), r.root()); !errors.Is(err, ErrImportBusy) {
		t.Fatalf("reset under a held import guard: got %v, want ErrImportBusy", err)
	}
}

// TestRequireResolvedNamesHeldConflicts covers the other unresolved shape: a
// package held on a conflict, whose record says where every contested version
// was kept.
func TestRequireResolvedNamesHeldConflicts(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), vaultsync.Existing(vaultsync.HashBytes([]byte("a version nobody holds\n"))))
	})
	if _, err := r.apply(t, dir); !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply of a contested package: got %v, want ErrConflict", err)
	}
	if held := importState(t, r).Conflicts; len(held) != 1 {
		t.Fatalf("held conflicts = %d, want 1", len(held))
	}
	if err := RequireResolved(r.root()); !errors.Is(err, ErrUnresolvedImport) {
		t.Fatalf("RequireResolved over a held conflict: got %v, want ErrUnresolvedImport", err)
	}
}
