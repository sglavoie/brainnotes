package noteops

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// binaryPayload is deliberately not text. A NUL byte, a lone 0xFF that is not
// valid UTF-8, and a CRLF are each something a helpful conversion somewhere
// between staging and disk would damage, and an attachment that arrives damaged
// is the one failure this path cannot report — it looks like a success.
var binaryPayload = []byte{0x00, 0x01, 0xff, 0xfe, '\r', '\n', 'P', 'N', 'G', 0x00}

// stageForCommit writes bytes into a private directory outside the vault and
// describes them the way the upload store does. The store's own tests cover the
// staging rules; what is needed here is a staged file with honest measurements.
func stageForCommit(t *testing.T, uploadID, relPath string, payload []byte) AttachmentCommit {
	t.Helper()
	source := filepath.Join(t.TempDir(), uploadID+".bin")
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatalf("stage %s: %v", uploadID, err)
	}
	return AttachmentCommit{
		UploadID:   uploadID,
		RelPath:    relPath,
		SourcePath: source,
		Size:       int64(len(payload)),
		Digest:     vault.HashBytes(payload),
	}
}

func assertAttachmentHolds(t *testing.T, root, rel string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read attachment %s: %v", rel, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("attachment %s holds %d bytes, want the %d submitted", rel, len(got), len(want))
	}
}

func assertNoAttachment(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Fatalf("%s is in the vault: %v", rel, err)
	}
}

// assertEntryCovers proves one undo entry names every path the save touched.
// Coverage is what makes the undo take the whole save back rather than the
// prose alone.
func assertEntryCovers(t *testing.T, root, entryID string, want ...string) {
	t.Helper()
	entry, err := journal.LoadEntry(JournalRoot(root), entryID)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	recorded := map[string]bool{}
	for _, file := range entry.Files {
		recorded[filepath.ToSlash(file.Path)] = true
	}
	for _, rel := range want {
		if !recorded[rel] {
			t.Fatalf("the undo entry does not cover %s; it covers %v", rel, recorded)
		}
	}
}

// The whole point of committing attachments with the note: one journal entry
// covering every path, so the vault never holds a note referring to files that
// are not there, nor files nothing refers to.
func TestSaveSourceCommitsAttachmentsWithTheNoteInOneOperation(t *testing.T) {
	root, cfg := sourceTestVault(t)
	const edited = "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n![[attachments/notes.pdf]]\n"
	photo := stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)
	pdf := stageForCommit(t, "bbbb1111", "attachments/notes.pdf", []byte("%PDF-1.7\n"))

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      edited,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		Attachments:  []AttachmentCommit{photo, pdf},
	})
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if got := readSourceContent(t, root); got != edited {
		t.Fatalf("the note on disk is not what was submitted:\n%q", got)
	}
	assertAttachmentHolds(t, root, "attachments/photo.png", binaryPayload)
	assertAttachmentHolds(t, root, "attachments/notes.pdf", []byte("%PDF-1.7\n"))

	if len(result.Attachments) != 2 {
		t.Fatalf("result named %d attachments, want 2", len(result.Attachments))
	}
	if result.Attachments[0].UploadID != "aaaa0000" || result.Attachments[0].RelPath != "attachments/photo.png" {
		t.Fatalf("result[0] = %+v", result.Attachments[0])
	}
	if got := sourceJournalEntryCount(t, root); got != 1 {
		t.Fatalf("journal entries = %d, want one operation covering the whole save", got)
	}
	assertEntryCovers(t, root, result.Journal.ID, "Alpha.md", "attachments/photo.png", "attachments/notes.pdf")
	// Staging is private and must stay that way: committing copies out of it and
	// never moves the staged file, or a failed commit would have consumed the
	// only copy the client could retry with.
	if _, err := os.Lstat(photo.SourcePath); err != nil {
		t.Fatalf("the commit consumed the staged file: %v", err)
	}
}

// One undo, the whole save. An undo that took the prose back and left the
// uploaded files would leave the vault holding attachments nothing refers to,
// which is exactly what committing them together is for.
func TestUndoOfASaveTakesBackItsAttachmentsToo(t *testing.T) {
	root, cfg := sourceTestVault(t)
	const edited = "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n"

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      edited,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		Attachments:  []AttachmentCommit{stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)},
	})
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if _, err := journal.Restore(root, JournalRoot(root), result.Journal.ID, journal.RestoreOptions{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readSourceContent(t, root); got != sourceFixture {
		t.Fatalf("undo did not put the note back:\n%q", got)
	}
	assertNoAttachment(t, root, "attachments/photo.png")
}

// A save that cannot land must land nothing. The transaction stages every path
// and checks every precondition before it touches a destination, so a stale
// revision has to leave the attachment in staging rather than half-committed.
func TestSaveSourceWithAStaleRevisionWritesNoAttachment(t *testing.T) {
	root, cfg := sourceTestVault(t)

	_, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n",
		BaseRevision: vault.HashBytes([]byte("some other version of this note")),
		Attachments:  []AttachmentCommit{stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)},
	})
	if !IsSourceRevisionConflict(err) {
		t.Fatalf("SaveSource err = %v, want a revision conflict", err)
	}
	assertSourceUnchanged(t, root)
	assertNoAttachment(t, root, "attachments/photo.png")
	if got := sourceJournalEntryCount(t, root); got != 0 {
		t.Fatalf("a refused save left %d journal entries", got)
	}
}

// A retry after a lost response plans fresh against the note it now finds, and
// the attachment it carries is already exactly where it belongs. Re-writing it
// would fail the transaction's create-only precondition, so the honest answer is
// that this upload is committed — which is what lets the client stop holding it.
func TestSaveSourceTreatsAnAlreadyCommittedAttachmentAsDone(t *testing.T) {
	root, cfg := sourceTestVault(t)
	photo := stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)
	writeCreateTestFile(t, root, "attachments/photo.png", string(binaryPayload))

	const edited = "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n"
	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      edited,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		Attachments:  []AttachmentCommit{photo},
	})
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].RelPath != "attachments/photo.png" {
		t.Fatalf("result named %+v, want the already-committed attachment", result.Attachments)
	}
	assertAttachmentHolds(t, root, "attachments/photo.png", binaryPayload)
}

// The other half of that check. An attachment is not a note: nobody pinned a
// revision to it, so there is no precondition under which replacing somebody
// else's file would be safe.
func TestSaveSourceRefusesToOverwriteADifferentFileAtTheDestination(t *testing.T) {
	root, cfg := sourceTestVault(t)
	writeCreateTestFile(t, root, "attachments/photo.png", "somebody else's picture")

	_, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n",
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		Attachments:  []AttachmentCommit{stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)},
	})
	if got := CodeOf(err); got != CodeConflict {
		t.Fatalf("SaveSource err = %v (code %s), want a conflict", err, got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "attachments", "photo.png")); string(got) != "somebody else's picture" {
		t.Fatalf("the existing file was touched: %q", got)
	}
	assertSourceUnchanged(t, root)
}

// A converged note is normally the end of a save. It cannot be, while an upload
// it carries is still only in staging: there is no note write left to carry the
// file, and answering success would send the client away from bytes that are
// about to be swept.
func TestSaveSourceRefusesToConvergeOverAnUncommittedAttachment(t *testing.T) {
	root, cfg := sourceTestVault(t)

	_, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      sourceFixture,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		Attachments:  []AttachmentCommit{stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)},
	})
	if got := CodeOf(err); got != CodeConflict {
		t.Fatalf("SaveSource err = %v (code %s), want a conflict", err, got)
	}
	assertNoAttachment(t, root, "attachments/photo.png")
}

// A replay answers from the record, so the record has to remember what landed.
// Without it a client whose response was lost would be told its note is saved
// and left with no word on the files it uploaded.
func TestSaveSourceReplayReportsTheAttachmentsThatLanded(t *testing.T) {
	root, cfg := sourceTestVault(t)
	const edited = "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n"
	req := SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      edited,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		SaveID:       "save-with-an-attachment",
		Attachments:  []AttachmentCommit{stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)},
	}
	if _, err := SaveSource(root, cfg, req); err != nil {
		t.Fatalf("first SaveSource: %v", err)
	}
	// Move the note on, so the resubmission is answered by the record rather than
	// by convergence.
	if _, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      "---\ntitle: Alpha\n---\n\nsomebody kept typing\n",
		BaseRevision: vault.HashBytes([]byte(edited)),
	}); err != nil {
		t.Fatalf("intervening SaveSource: %v", err)
	}

	replay, err := SaveSource(root, cfg, req)
	if err != nil {
		t.Fatalf("replayed SaveSource: %v", err)
	}
	if !replay.Replayed {
		t.Fatalf("result = %+v, want a replay", replay)
	}
	if len(replay.Attachments) != 1 || replay.Attachments[0].UploadID != "aaaa0000" {
		t.Fatalf("the replay named %+v, want the upload the original save committed", replay.Attachments)
	}
}

// TestSaveSourceRefusesAnUploadTheCallerCouldNotFind is the first half of the
// unresolved-upload rule: a save that is really writing a note must not store a
// link to a file nothing is going to put there.
func TestSaveSourceRefusesAnUploadTheCallerCouldNotFind(t *testing.T) {
	root, cfg := sourceTestVault(t)
	_, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:               "Alpha.md",
		Content:               "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n",
		BaseRevision:          vault.HashBytes([]byte(sourceFixture)),
		UnresolvedAttachments: []string{"bbbb1111"},
	})
	if got := CodeOf(err); got != CodeNotFound {
		t.Fatalf("code = %q (err %v), want %q", got, err, CodeNotFound)
	}
	if !strings.Contains(err.Error(), "bbbb1111") {
		t.Fatalf("error = %q, want it to name the upload the client has to send again", err)
	}
	assertSourceUnchanged(t, root)
}

// TestSaveSourceReplaysEvenWhenTheUploadIsNoLongerStaged is the other half, and
// the reason the refusal is placed where it is: the save whose staging is gone
// is very often the save that already succeeded and dropped the staged copy, so
// a retry that names the same ids is answered rather than refused.
func TestSaveSourceReplaysEvenWhenTheUploadIsNoLongerStaged(t *testing.T) {
	root, cfg := sourceTestVault(t)
	const edited = "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n"
	req := SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      edited,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		SaveID:       "save-whose-staging-was-cleared",
		Attachments:  []AttachmentCommit{stageForCommit(t, "cccc2222", "attachments/photo.png", binaryPayload)},
	}
	if _, err := SaveSource(root, cfg, req); err != nil {
		t.Fatalf("first SaveSource: %v", err)
	}

	// The retry is the same request seen by a caller whose staging lookup now
	// fails, which is exactly what the first save's cleanup caused.
	retry := req
	retry.Attachments = nil
	retry.UnresolvedAttachments = []string{"cccc2222"}
	replay, err := SaveSource(root, cfg, retry)
	if err != nil {
		t.Fatalf("retried SaveSource: %v", err)
	}
	if !replay.Replayed {
		t.Fatalf("result = %+v, want a replay", replay)
	}
	if len(replay.Attachments) != 1 || replay.Attachments[0].UploadID != "cccc2222" {
		t.Fatalf("the replay named %+v, want the upload the original save committed", replay.Attachments)
	}
	assertAttachmentHolds(t, root, "attachments/photo.png", binaryPayload)
}

func TestSaveSourceRefusesAttachmentDestinationsThatAreNotAttachments(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		code ErrorCode
	}{
		{"a note", "attachments/notes.md", CodeInvalidRequest},
		{"a note by another spelling", "attachments/notes.MarkDown", CodeInvalidRequest},
		{"hidden vault state", ".bnotes/uploads/stolen.png", CodeForbidden},
		{"a hidden file", "attachments/.profile", CodeForbidden},
		{"the git directory", ".git/hooks/pre-commit", CodeForbidden},
		{"an escape", "../outside.png", CodeForbidden},
		{"an absolute path", "/etc/passwd", CodeForbidden},
		{"nothing at all", "", CodeInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, cfg := sourceTestVault(t)
			_, err := SaveSource(root, cfg, SaveSourceRequest{
				RelPath:      "Alpha.md",
				Content:      "---\ntitle: Alpha\n---\n\nbody\n",
				BaseRevision: vault.HashBytes([]byte(sourceFixture)),
				Attachments:  []AttachmentCommit{stageForCommit(t, "aaaa0000", tc.rel, binaryPayload)},
			})
			if got := CodeOf(err); got != tc.code {
				t.Fatalf("SaveSource(%q) err = %v (code %s), want %s", tc.rel, err, got, tc.code)
			}
			assertSourceUnchanged(t, root)
			if got := sourceJournalEntryCount(t, root); got != 0 {
				t.Fatalf("a refused save left %d journal entries", got)
			}
		})
	}
}

// An attachment aimed through a symlink would write outside the vault while
// every relative path in the request still looked contained.
func TestSaveSourceRefusesAnAttachmentReachedThroughASymlink(t *testing.T) {
	root, cfg := sourceTestVault(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "attachments")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      "---\ntitle: Alpha\n---\n\nbody\n",
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		Attachments:  []AttachmentCommit{stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)},
	})
	if got := CodeOf(err); got != CodeForbidden {
		t.Fatalf("SaveSource err = %v (code %s), want forbidden", err, got)
	}
	if _, err := os.Lstat(filepath.Join(outside, "photo.png")); !os.IsNotExist(err) {
		t.Fatalf("the save wrote outside the vault: %v", err)
	}
}
