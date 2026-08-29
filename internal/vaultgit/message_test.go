package vaultgit

import (
	"strings"
	"testing"
	"time"
)

func TestAutosaveMessageIsContentFree(t *testing.T) {
	when := time.Date(2026, 8, 28, 14, 5, 9, 123456789, time.FixedZone("CEST", 2*60*60))
	got := autosaveMessage(when)
	if got != "brainnotes: autosave 2026-08-28T12:05:09Z" {
		t.Fatalf("autosaveMessage = %q", got)
	}
	// The same instant observed in another zone must produce the same message,
	// so two machines snapshotting the same moment agree.
	if autosaveMessage(when.UTC()) != got {
		t.Fatal("autosaveMessage must not depend on the observer's time zone")
	}
}

func TestJournalMessageShape(t *testing.T) {
	got := journalMessage("rename", "20260828T140509.000000000Z-abcd")
	if got != "brainnotes: rename [20260828T140509.000000000Z-abcd]" {
		t.Fatalf("journalMessage = %q", got)
	}
}

// The operation token is the only caller-supplied part of a commit message, and
// a caller could hand it something derived from user input. It is reduced to a
// short identifier rather than escaped, so anything unexpected disappears.
func TestSanitizeOperation(t *testing.T) {
	cases := []struct{ in, want string }{
		{"rename", "rename"},
		{"Rename", "rename"},
		{"  move  ", "move"},
		{"set property", "set-property"},
		{"set_property", "set-property"},
		{"", "mutation"},
		{"   ", "mutation"},
		{"!!!", "mutation"},
		{"---", "mutation"},
		{"bn set \"My Secret Note.md\" tags=private", "bn-set-my-secret-notemd-tagspriv"},
		{"rm -rf /; echo pwned", "rm-rf-echo-pwned"},
		{"multi\nline\nmessage", "multi-line-message"},
		{"tab\there", "tab-here"},
	}
	for _, tc := range cases {
		if got := sanitizeOperation(tc.in); got != tc.want {
			t.Fatalf("sanitizeOperation(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeOperationIsBounded(t *testing.T) {
	got := sanitizeOperation(strings.Repeat("abc", 200))
	if len(got) > 32 {
		t.Fatalf("sanitizeOperation produced %d characters", len(got))
	}
}

// A commit message must never carry a newline: it would let a caller forge
// extra message body, and it would break every one-line log format.
func TestSanitizedMessagesAreSingleLine(t *testing.T) {
	message := journalMessage("bad\noperation", "entry-1")
	if strings.ContainsAny(message, "\n\r") {
		t.Fatalf("commit message contains a line break: %q", message)
	}
}

func TestValidEntryID(t *testing.T) {
	valid := []string{
		"20260828T140509.000000000Z-abcd1234",
		"entry-1",
		"entry_1",
	}
	for _, id := range valid {
		if err := validEntryID(id); err != nil {
			t.Fatalf("validEntryID(%q) = %v, want nil", id, err)
		}
	}
	invalid := []string{
		"",
		"   ",
		"../escape",
		"..",
		"entry/1",
		`entry\1`,
		"entry 1",
		"entry;rm -rf",
		strings.Repeat("a", 129),
	}
	for _, id := range invalid {
		if err := validEntryID(id); err == nil {
			t.Fatalf("validEntryID(%q) = nil, want an error", id)
		}
	}
}

func TestErrorHelpers(t *testing.T) {
	deletion := unexplainedDeletionError("commit-pending", []string{"Inbox/gone.md"})
	if !IsUnexplainedDeletion(deletion) || !Blocks(deletion) {
		t.Fatalf("an unexplained deletion must be a blocking failure: %v", deletion)
	}
	if !strings.Contains(deletion.Error(), "Inbox/gone.md") {
		t.Fatalf("the error must name the path: %v", deletion)
	}

	busy := safetyErrorf(KindBusy, "preflight", "another process holds the lock")
	if !IsBusy(busy) || Blocks(busy) {
		t.Fatal("a busy vault is retryable and must not latch")
	}
	push := safetyErrorf(KindPushFailed, "push", "the remote is unreachable")
	if !IsPushFailure(push) || Blocks(push) {
		t.Fatal("a push failure must never latch the mutation block")
	}
	stale := safetyErrorf(KindStaleRequest, "commit-journal", "the working tree moved on")
	if Blocks(stale) {
		t.Fatal("a stale request must not latch")
	}
	state := safetyErrorf(KindRepositoryState, "open", "HEAD is detached")
	if Blocks(state) {
		t.Fatal("a repository-state failure is fixable by the user and must not latch")
	}
}

func TestRecoveryInstructionsExistForEveryBlockingKind(t *testing.T) {
	for _, kind := range []Kind{KindUnexplainedDeletion, KindAmbiguousChange, KindRefConflict, KindCommitFailed} {
		steps := recoveryFor(kind, []string{"Inbox/gone.md"})
		if len(steps) == 0 {
			t.Fatalf("%q has no recovery instructions", kind)
		}
		for _, step := range steps {
			if strings.ContainsAny(step, "\n\r") {
				t.Fatalf("%q recovery step spans lines: %q", kind, step)
			}
		}
	}
}
