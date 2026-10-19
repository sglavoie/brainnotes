package vaultrecovery

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestRecoverImportClearsAPendingAttemptUnderTheReviewMarker is the dead end
// the refusal used to leave: a restored vault is quarantined, so no companion
// pass runs import recovery, and re-enrollment refuses while an attempt is
// pending. The refusal names the command, the command resolves the attempt, and
// the inventory can then be built.
func TestRecoverImportClearsAPendingAttemptUnderTheReviewMarker(t *testing.T) {
	r := newReviewPair(t)
	root := r.pair.Local.Root
	r.prepare(t, root)
	declarePendingAttempt(t, r)

	_, err := BuildInventory(root)
	if !errors.Is(err, vaultimport.ErrUnresolvedImport) || !strings.Contains(err.Error(), RecoverImportCommand) {
		t.Fatalf("inventory over a pending attempt: got %v, want a refusal naming %q", err, RecoverImportCommand)
	}

	report, err := RecoverImport(root)
	if err != nil {
		t.Fatalf("RecoverImport: %v", err)
	}
	if report.Outcome != string(vaultimport.OutcomeDiscarded) || report.Remaining != "" {
		t.Fatalf("recovery report = %+v, want the attempt discarded and nothing left", report)
	}
	if review, err := vaultsync.LoadRestoreReview(root); err != nil || review == nil {
		t.Fatalf("recovery lifted the quarantine: %+v, %v", review, err)
	}
	if _, err := BuildInventory(root); err != nil {
		t.Fatalf("inventory after recovery: %v", err)
	}
}

// TestRecoverImportReportsAHeldConflictItCannotDecide covers the unresolved
// work recovery has no answer for: a held package needs a person's choice.
func TestRecoverImportReportsAHeldConflictItCannotDecide(t *testing.T) {
	r := newReviewPair(t)
	holdConflict(t, r.pair.Local.Root, r.local)
	report, err := RecoverImport(r.pair.Local.Root)
	if err != nil {
		t.Fatalf("RecoverImport: %v", err)
	}
	if report.Outcome != string(vaultimport.OutcomeNone) || !strings.Contains(report.Remaining, "held on conflicts") {
		t.Fatalf("recovery report = %+v, want the held conflict named as remaining", report)
	}
}

// declarePendingAttempt writes the record a process killed between declaring an
// application and committing it leaves: a pending attempt naming a journal
// entry that was never created.
func declarePendingAttempt(t *testing.T, r *reviewPair) {
	t.Helper()
	builder, err := vaultsync.NewBuilder(r.peer, 1, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	builder.BeginOperation("", r.clock.Now())
	body, err := os.ReadFile(filepath.Join(r.pair.Local.Root, "Alpha.md"))
	if err != nil {
		t.Fatal(err)
	}
	builder.Write("Alpha.md", []byte("the peer's body\n"), vaultsync.Existing(vaultsync.HashBytes(body)))
	manifest, _, err := builder.PublishTo(t.TempDir())
	if err != nil {
		t.Fatalf("PublishTo: %v", err)
	}
	entryID, err := journal.NewEntryID(r.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{
		"version":   3,
		"vault_id":  r.local.VaultID,
		"device_id": r.local.DeviceID,
		"applied":   map[string]any{},
		"pending":   map[string]any{"journal_entry": entryID, "manifest": manifest},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vaultimport.StatePath(r.pair.Local.Root), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := vaultimport.LoadState(r.pair.Local.Root); err != nil || state.Pending == nil {
		t.Fatalf("the hand-written attempt does not load as pending: %+v, %v", state, err)
	}
}
