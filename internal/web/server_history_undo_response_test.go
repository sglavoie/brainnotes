package web

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// A leftover temp file is not a failed restore, and neither message may read as
// one. The content is back, journaled and snapshotted; the only action anybody
// can take is deleting the named file, so the only requirement is that it is
// named rather than swallowed.
func TestUndoSuccessMessage_NamesALeftoverWithoutCallingItAFailure(t *testing.T) {
	plan := &journal.RestorePlan{Changes: []journal.RestoreChange{{Path: "Source.md"}}}
	if got := undoSuccessMessage(plan); got != "Undo restored 1 path." {
		t.Fatalf("clean message = %q", got)
	}

	plan.Cleanup = &vault.TxnCleanupError{
		Path:         "Source.md",
		LeftoverPath: "Source.md.tmp",
		Err:          errors.New("permission denied"),
	}
	got := undoSuccessMessage(plan)
	if !strings.HasPrefix(got, "Undo restored 1 path.") {
		t.Fatalf("message stopped reporting the restore: %q", got)
	}
	if !strings.Contains(got, "Source.md.tmp") {
		t.Fatalf("message never names the file it asks somebody to delete: %q", got)
	}
}

// If a cleanup failure ever does come back as an error, the restore did *not*
// finish: journal.Restore carries the housekeeping case out on the plan so the
// operation still reaches Finalize, and the only way to arrive here is that it
// did not — meaning the writes landed with no manifest authorizing them, which is
// what vaultgit refuses the next mutation over. The message must not say the
// restore finished.
func TestUndoRestoreError_DoesNotCallAnUnfinalizedRestoreFinished(t *testing.T) {
	err := undoRestoreError(&vault.TxnCleanupError{
		Path:         "Source.md",
		LeftoverPath: "Source.md.tmp",
		Err:          errors.New("permission denied"),
	})
	var opErr *noteops.Error
	if !errors.As(err, &opErr) {
		t.Fatalf("undoRestoreError returned %T, want a *noteops.Error", err)
	}
	if opErr.Code != noteops.CodeInternal {
		t.Fatalf("code = %q, want %q", opErr.Code, noteops.CodeInternal)
	}
	if strings.Contains(opErr.Error(), "finished") {
		t.Fatalf("the message claims the restore finished: %q", opErr.Error())
	}
	if !strings.Contains(opErr.Error(), "not recorded") {
		t.Fatalf("the message never says the restore was not recorded: %q", opErr.Error())
	}
}

// A restore that could not put displaced content back has one actionable fact:
// the file that content now lives in. The answer the client sees must carry it,
// not only the error chain behind it.
func TestUndoRestoreError_NamesTheRecoveryPath(t *testing.T) {
	recovery := &vault.TxnRecoveryError{
		Path:         "Source.md",
		RecoveryPath: ".txn-custody-1.tmp",
		Err:          &vault.TxnConflictError{Path: "Source.md", Expected: "missing", Actual: "file"},
	}
	for name, cause := range map[string]error{
		"recovery": recovery,
		"rollback": &vault.TxnRollbackError{
			Path:         "Source.md",
			Err:          recovery,
			Failed:       []string{"Other.md"},
			RollbackErrs: []error{errors.New("permission denied")},
		},
	} {
		err := undoRestoreError(cause)
		if got := noteops.CodeOf(err); got != noteops.CodeInternal {
			t.Fatalf("%s: code = %q, want %q", name, got, noteops.CodeInternal)
		}
		if message := noteops.MessageOf(err); !strings.Contains(message, recovery.RecoveryPath) {
			t.Fatalf("%s: message hides the recovery path: %q", name, message)
		}
	}
}

// A cache the server could not refresh must not be reported as a failed restore:
// the content is back on disk, and answering with the reload's error would both
// say otherwise and throw away the one mention a leftover temp file ever gets.
func TestUndoResponseWithReloadNotice_KeepsTheRestoreAsTheAnswer(t *testing.T) {
	plan := &journal.RestorePlan{
		Changes: []journal.RestoreChange{{Path: "Source.md"}},
		Cleanup: &vault.TxnCleanupError{
			Path:         "Source.md",
			LeftoverPath: "Source.md.tmp",
			Err:          errors.New("permission denied"),
		},
	}
	base := undoResponseForPlan(plan)

	if got := undoResponseWithReloadNotice(base, nil); got.Message != base.Message {
		t.Fatalf("a successful reload changed the message: %q", got.Message)
	}

	got := undoResponseWithReloadNotice(base, errors.New("config: reload failed"))
	if !got.OK {
		t.Fatal("a stale cache was reported as a failed restore")
	}
	if !strings.HasPrefix(got.Message, base.Message) {
		t.Fatalf("the restore's own outcome was replaced: %q", got.Message)
	}
	if !strings.Contains(got.Message, "Source.md.tmp") {
		t.Fatalf("the leftover file was dropped along the way: %q", got.Message)
	}
	if !strings.Contains(got.Message, "config: reload failed") {
		t.Fatalf("the reload failure went unmentioned: %q", got.Message)
	}
}
