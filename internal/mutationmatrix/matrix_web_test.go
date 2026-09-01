package mutationmatrix

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// TestWebSafetyFailuresAreDistinguishable500s pins the one HTTP convention the
// status code alone cannot carry.
//
// A refusal before the write and a snapshot failure after it are both "the
// server could not do this safely", and both are 500. But they are opposite
// answers to the only question the person on the other end has — did my note
// change? — so the response has to distinguish them, and the code and the
// message are the only things that can. A regression that collapsed the two
// would still return 500 and would still look fine in a status assertion.
func TestWebSafetyFailuresAreDistinguishable500s(t *testing.T) {
	h := newHarness(t, nil)
	h.breakSnapshots()

	failed := postCapture(t, h, "the capture that landed")
	if code := noteops.CodeOf(failed); code != noteops.CodeSnapshotFailed {
		t.Fatalf("a snapshot failure answered %q, want %q (%v)", code, noteops.CodeSnapshotFailed, failed)
	}
	if !strings.Contains(failed.Error(), "completed and is on disk") {
		t.Fatalf("the response must say the capture stands; it said %q", failed)
	}
	if !strings.Contains(h.readFile("Inbox.md"), "the capture that landed") {
		t.Fatal("the response said the capture is on disk and it is not")
	}

	h.repairSnapshots()
	vaultgit.ForgetShared(h.root)

	refused := postCapture(t, h, "the capture that never started")
	if code := noteops.CodeOf(refused); code != noteops.CodeSafety {
		t.Fatalf("a refusal answered %q, want %q (%v)", code, noteops.CodeSafety, refused)
	}
	if strings.Contains(refused.Error(), "completed and is on disk") {
		t.Fatalf("a refusal before the write must not claim the capture happened: %q", refused)
	}
	if strings.Contains(h.readFile("Inbox.md"), "never started") {
		t.Fatal("a refused capture was written anyway")
	}
}
