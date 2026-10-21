package journal

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// largeRestorePayload is big enough that one copy of it is unmistakable in an
// allocation total, and small enough to stay a cheap test.
const largeRestorePayload = 8 << 20

// writeFiller creates a file of n deterministic non-zero bytes and returns them.
// Non-zero because a sparse file would not be read, copied, or hashed at the
// sizes this is used for, which is the whole point of measuring it.
func writeFiller(t *testing.T, path string, n int) []byte {
	t.Helper()
	payload := make([]byte, n)
	for i := range payload {
		payload[i] = byte(i%251 + 1)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return payload
}

// TestRestorePutsALargeFileBackWithoutHoldingIt is the bound on the undo path.
//
// applyRestore stages every target before it commits any of them, so a restore
// that read its snapshots would hold the whole entry at once — and an entry that
// committed attachments is exactly the one where that sum is measured in
// hundreds of megabytes. The correctness half is covered by the mixed-operation
// test above; what is asserted here is that the bytes never became resident.
func TestRestorePutsALargeFileBackWithoutHoldingIt(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	attachment := filepath.Join(vaultRoot, "attachments", "photo.png")

	payload := writeFiller(t, attachment, largeRestorePayload)

	rec, err := NewRecorder(vaultRoot, journalRoot, "bn attachment-delete")
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(attachment); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := os.Remove(attachment); err != nil {
		t.Fatalf("remove attachment: %v", err)
	}
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	runtime.ReadMemStats(&after)

	// One copy of the file is the regression being watched for; the ceiling is
	// well under that and far above an io.Copy buffer plus the churn of a
	// transaction, so it fails on residency and not on noise.
	allocated := after.TotalAlloc - before.TotalAlloc
	if ceiling := uint64(largeRestorePayload / 2); allocated > ceiling {
		t.Fatalf("restoring a %d byte attachment allocated %d bytes, want under %d",
			len(payload), allocated, ceiling)
	}

	restored, err := os.ReadFile(attachment)
	if err != nil {
		t.Fatalf("read restored attachment: %v", err)
	}
	if !bytes.Equal(restored, payload) {
		t.Fatalf("restored attachment is %d bytes and not the ones that were removed", len(restored))
	}
}

// TestRestoreCommitOutcomeKeepsAFinishedRestoreOutOfTheFailurePath is the test
// the carry in applyRestore exists for.
//
// The distinction is not cosmetic. noteops.Undo stops at the first error Restore
// hands back and never reaches Finalize, so classifying a leftover temp file as a
// failure leaves the restore's writes on disk with no undo manifest authorizing
// them — and an undo that removed a tracked note then looks to vaultgit like a
// deletion nothing explains, which latches blocked.json over the next mutation.
// Getting a real unlink to fail means arranging a filesystem failure; the
// classification is a pure function of the error, so it is tested as one, the
// same way vaultimport tests classifyCommit.
func TestRestoreCommitOutcomeKeepsAFinishedRestoreOutOfTheFailurePath(t *testing.T) {
	conflict := &vault.TxnConflictError{Path: "Notes/Alpha.md", Expected: "aaa", Actual: "bbb"}
	cleanup := &vault.TxnCleanupError{
		Path:         "Notes/Alpha.md",
		LeftoverPath: "Notes/.alpha.tmp",
		Err:          errors.New("permission denied"),
	}

	cases := []struct {
		name string
		err  error
		// carried says the restore finished and the error is housekeeping.
		carried bool
	}{
		{name: "a commit that succeeded carries nothing and fails with nothing"},
		{
			name:    "a temp file that could not be unlinked is not a failed restore",
			err:     cleanup,
			carried: true,
		},
		{
			name:    "a wrapped cleanup failure is still a cleanup failure",
			err:     fmt.Errorf("journal: restore e1: %w", cleanup),
			carried: true,
		},
		{
			// Nests the conflict, and vault's predicates are errors.As — so asking
			// the cleanup question first is safe only because this is not one.
			name: "a failed rollback is a failure, half-applied and needing a person",
			err: &vault.TxnRollbackError{
				Path:         "Notes/Alpha.md",
				Err:          conflict,
				Restored:     []string{"Notes/Beta.md"},
				Failed:       []string{"Notes/Alpha.md"},
				RollbackErrs: []error{errors.New("rename back refused")},
			},
		},
		{name: "a precondition that moved is a failure", err: conflict},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRestoreCommitOutcome(t, tc.err, tc.carried, cleanup)
		})
	}
}

// assertRestoreCommitOutcome is the table's body lifted out of the table: the
// slice literal, the loop and the three-way comparison are one function's
// complexity score, and the comparison is the half worth naming.
func assertRestoreCommitOutcome(t *testing.T, err error, carried bool, cleanup error) {
	t.Helper()

	got, fatal := restoreCommitOutcome(err)
	switch {
	case err == nil:
		if got != nil || fatal != nil {
			t.Fatalf("restoreCommitOutcome(nil) = (%v, %v), want (nil, nil)", got, fatal)
		}
	case carried:
		if fatal != nil {
			t.Fatalf("a finished restore was reported as a failure: %v", fatal)
		}
		if !errors.Is(got, cleanup) {
			t.Fatalf("carried = %v, want the cleanup failure itself", got)
		}
	default:
		if got != nil {
			t.Fatalf("carried = %v, want nothing carried out of a failure", got)
		}
		if !errors.Is(fatal, err) {
			t.Fatalf("fatal = %v, want the error itself", fatal)
		}
	}
}

// TestRestoreLeavesCleanupUnsetWhenNothingWasLeftBehind keeps the carried field
// from reading as "set on every restore": an ordinary one must answer nil, or a
// caller rendering the notice would announce a temp file on every undo.
//
// Deliberately narrow, and worth knowing when leaning on it: a clean commit
// answers on restoreCommitOutcome's first arm, so this catches an unconditional
// assignment and nothing else. Rearranging the two arms below it is the table
// test's job.
func TestRestoreLeavesCleanupUnsetWhenNothingWasLeftBehind(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	notePath := filepath.Join(vaultRoot, "Clean.md")
	recordOneRewrite(t, vaultRoot, journalRoot, notePath, "entry-clean")

	plan, err := Restore(vaultRoot, journalRoot, "entry-clean", RestoreOptions{})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if plan.Cleanup != nil {
		t.Fatalf("Cleanup = %v, want nil for a restore that left nothing behind", plan.Cleanup)
	}
}
