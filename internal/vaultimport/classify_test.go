package vaultimport

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// TestClassifyCommitOrdersTheArmsByWhatTheyCost is the test the ordering in
// classifyCommit exists for, and it cannot be written against a real FileTxn.
//
// Two of these types nest inside the conflict that caused them —
// *vault.TxnRollbackError wraps it and *vault.TxnRecoveryError wraps a join
// containing it — so vault.IsTxnConflict, which is an errors.As predicate,
// answers yes to a failed rollback and to displaced content as readily as to the
// plain precondition failure. Which one an applier reports is therefore decided
// by the order the questions are asked in, and the two cases below that assert
// the refusal is *not* an ErrConflict are what pin it: moving the conflict arm
// above them turns a half-applied package into "nothing changed" and fails here.
// The cleanup and lock-busy arms do not nest and would classify in any order;
// they are covered because the behaviour matters, not the position.
//
// Provoking all five through a live transaction would mean arranging five
// different filesystem failures. The classification is a pure function of the
// error, so it is tested as one.
func TestClassifyCommitOrdersTheArmsByWhatTheyCost(t *testing.T) {
	const packageID = "pkg-1"
	conflict := &vault.TxnConflictError{Path: "Notes/Alpha.md", Expected: "aaa", Actual: "bbb"}

	cases := []struct {
		name string
		err  error
		// applied says the import went on and the failure is housekeeping.
		applied bool
		// sentinel is what the refusal must report, or nil when the error is
		// returned as it is.
		sentinel error
	}{
		{
			name:    "a temp file that could not be unlinked is not a failed import",
			err:     &vault.TxnCleanupError{Path: "Notes/Alpha.md", LeftoverPath: "Notes/.note-x.tmp", Err: errors.New("permission denied")},
			applied: true,
		},
		{
			name: "displaced content is reported as it is, so its recovery path survives",
			err:  &vault.TxnRecoveryError{Path: "Notes/Alpha.md", RecoveryPath: "Notes/Alpha.md.recovered", Err: conflict},
		},
		{
			name: "a failed rollback is reported as it is, half-applied and needing a person",
			err: &vault.TxnRollbackError{
				Path:         "Notes/Alpha.md",
				Err:          conflict,
				Restored:     []string{"Notes/Beta.md"},
				Failed:       []string{"Notes/Alpha.md"},
				RollbackErrs: []error{errors.New("rename back refused")},
			},
		},
		{
			name: "a busy lock read nothing and wrote nothing, so it is contention rather than disagreement",
			err:  &vault.TxnLockBusyError{Path: "Notes/Alpha.md"},
		},
		{
			name:     "a plain precondition failure is the one that becomes ErrConflict",
			err:      conflict,
			sentinel: ErrConflict,
		},
		{
			name: "anything else is passed through unchanged rather than guessed at",
			err:  errors.New("the disk is full"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanup, refusal := classifyCommit(packageID, tc.err)
			if tc.applied {
				requireCarried(t, cleanup, refusal, tc.err)
				return
			}
			if cleanup != nil {
				t.Fatalf("a failed import carried %v out as housekeeping", cleanup)
			}
			requireRefused(t, refusal, tc.err, tc.sentinel, packageID)
		})
	}
}

// TestClassifyCommitReportsASuccessfulCommitAsNeither covers the arm every
// applied package takes.
func TestClassifyCommitReportsASuccessfulCommitAsNeither(t *testing.T) {
	cleanup, refusal := classifyCommit("pkg-1", nil)
	if cleanup != nil || refusal != nil {
		t.Fatalf("a successful commit produced cleanup %v and refusal %v", cleanup, refusal)
	}
}

// requireCarried checks the one outcome that is not a failure: the import went
// on and the housekeeping problem came out beside it, unwrapped, so the message
// naming the leftover file reaches the person who has to delete it.
func requireCarried(t *testing.T, cleanup, refusal, want error) {
	t.Helper()
	if refusal != nil {
		t.Fatalf("a cleanup failure refused the import: %v", refusal)
	}
	if !errors.Is(cleanup, want) {
		t.Fatalf("carried %v, want the cleanup failure itself", cleanup)
	}
}

// requireRefused checks a refusal against the sentinel it must carry, and
// against the diagnosis it must still contain either way.
func requireRefused(t *testing.T, refusal, cause, sentinel error, packageID string) {
	t.Helper()
	if refusal == nil {
		t.Fatal("a failed commit was reported as a success")
	}
	if !errors.Is(refusal, cause) && !strings.Contains(refusal.Error(), cause.Error()) {
		t.Fatalf("%v lost the failure %v that caused it", refusal, cause)
	}
	if sentinel == nil {
		if errors.Is(refusal, ErrConflict) {
			t.Fatalf("%v was reported as a conflict, which says nothing changed", refusal)
		}
		if !errors.Is(refusal, cause) {
			t.Fatalf("%v is not the failure it was given, so errors.As on it would miss", refusal)
		}
		return
	}
	requireIs(t, refusal, sentinel, "classifying a precondition failure")
	if !strings.Contains(refusal.Error(), packageID) {
		t.Fatalf("%v does not name the package it refused", refusal)
	}
}
