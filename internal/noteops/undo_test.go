package noteops

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// TestUndoForwardsTheAuthorizedStateIntoTheTransaction covers the middle hop of
// the pin's journey, which nothing else does.
//
// The web layer's test stops at UndoOptions, and journal's tests start at
// RestoreOptions, so dropping Require from the one line between them would leave
// both green — and would silently un-pin every forced restore the browser
// performs. That matters because the pin is the *only* check inside the
// transaction's locks: everything the preview compared ran before this call, so
// a companion write landing in between changes the bytes without changing the
// conflict set, and Force is precisely the flag that stops that from being
// noticed. Here the journaled post state still matches on disk — so the restore
// gets past every earlier refusal — and only the authorization disagrees.
func TestUndoForwardsTheAuthorizedStateIntoTheTransaction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	note := filepath.Join(root, "Alpha.md")
	entryID := recordOneUndoableRewrite(t, root, note)

	_, err := Undo(root, entryID, UndoOptions{
		Command: "bn web undo",
		Require: map[string]vault.FileExpectation{
			// Bytes this path never held, standing in for the version somebody was
			// shown before another process rewrote the file.
			"Alpha.md": vault.ExpectHash(sha256Hex("somebody else's edit\n")),
		},
	})
	if err == nil {
		t.Fatal("the restore was performed against a state it was not authorized for: the pins never reached the transaction")
	}
	if !vault.IsTxnConflict(err) {
		t.Fatalf("Undo error = %v, want a transaction conflict", err)
	}
	if got := readFileString(t, note); got != "after\n" {
		t.Fatalf("Alpha.md = %q, want the refused restore to have left %q", got, "after\n")
	}
}

// TestUndoWithNoAuthorizationRestoresTheRecordedState is the other half: the
// refusal above must come from the pin disagreeing, not from supplying one at
// all. A caller that names no authorization — every CLI undo — still works.
func TestUndoWithNoAuthorizationRestoresTheRecordedState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	note := filepath.Join(root, "Alpha.md")
	entryID := recordOneUndoableRewrite(t, root, note)

	plan, err := Undo(root, entryID, UndoOptions{Command: "bn undo"})
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if plan.Cleanup != nil {
		t.Fatalf("plan.Cleanup = %v, want nothing left behind by an ordinary restore", plan.Cleanup)
	}
	if got := readFileString(t, note); got != "before\n" {
		t.Fatalf("Alpha.md = %q, want %q", got, "before\n")
	}
}

// recordOneUndoableRewrite leaves the note holding its journaled post state, so
// a restore of the returned entry reaches the transaction with nothing else to
// object to.
func recordOneUndoableRewrite(t *testing.T, root, note string) string {
	t.Helper()

	writeFileString(t, note, "before\n")
	rec, err := journal.NewRecorder(
		root,
		JournalRoot(root),
		"bn set Alpha.md status=done",
		journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-undo-pin", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(note); err != nil {
		t.Fatalf("capture: %v", err)
	}
	writeFileString(t, note, "after\n")
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return entry.ID
}

func writeFileString(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
