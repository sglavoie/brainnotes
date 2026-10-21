package noteops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/frontmatter"
	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestSaveSourceWritesTheSubmittedBytesVerbatim(t *testing.T) {
	root, cfg := sourceTestVault(t)
	base := vault.HashBytes([]byte(sourceFixture))

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      sourceEdit,
		BaseRevision: base,
	})
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if !result.Changed || result.Created || result.Replayed {
		t.Fatalf("result = %+v, want a plain change", result)
	}
	stored, err := os.ReadFile(filepath.Join(root, "Alpha.md"))
	if err != nil {
		t.Fatalf("read the saved note: %v", err)
	}
	// Byte equality, not "parses the same". status comes after title in the
	// submission and must still come after it on disk.
	if string(stored) != sourceEdit {
		t.Fatalf("the note on disk is not what was submitted:\n%q", stored)
	}
	if want := vault.HashBytes([]byte(sourceEdit)); result.Revision != want {
		t.Fatalf("revision = %q, want %q", result.Revision, want)
	}
	if result.Journal == nil {
		t.Fatal("a save that rewrote a note recorded no undo entry")
	}
	if got := sourceJournalEntryCount(t, root); got != 1 {
		t.Fatalf("journal entries = %d, want 1", got)
	}
}

// TestSaveSourceKeepsLineEndingsAndTrailingSpace puts every hazard a helpful
// normalization would repair into one submission and asserts byte equality on
// disk. The fixture is read back verbatim elsewhere; this is the write half,
// because a save that round-tripped through frontmatter.Marshal would still pass
// every assertion about a submission that happened to be canonical already.
func TestSaveSourceKeepsLineEndingsAndTrailingSpace(t *testing.T) {
	root, cfg := sourceTestVault(t)
	const ugly = "---\ntitle: Alpha\nstatus: done\ntopics:\n    - one\n    - two\n---\n\nedited  \r\nlast line, no final newline"

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      ugly,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
	})
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if got := readSourceContent(t, root); got != ugly {
		t.Fatalf("the note on disk was repaired on the way in:\n%q", got)
	}
	// The published revision has to be the digest of those exact bytes, or the
	// next save this editor sends is a spurious conflict.
	if want := vault.HashBytes([]byte(ugly)); result.Revision != want {
		t.Fatalf("revision = %q, want %q", result.Revision, want)
	}
}

// TestSaveSourceStoresAnUnterminatedPropertiesBlockVerbatim pins the deference
// ValidateSource's contract claims. A file that opens a properties block and
// never closes it is body-only with a nil error to frontmatter.Parse — a note
// whose body begins with a horizontal rule, a reading that package chose
// deliberately so such a note stays editable. Refusing it here would make this
// the one surface in the vault that cannot save a note the vault itself holds
// and serves.
func TestSaveSourceStoresAnUnterminatedPropertiesBlockVerbatim(t *testing.T) {
	root, cfg := sourceTestVault(t)
	const unterminated = "---\ntitle: Alpha\n\nstill the body: the block above is never closed.\n"

	if err := ValidateSource(unterminated); err != nil {
		t.Fatalf("ValidateSource refused a note frontmatter.Parse accepts: %v", err)
	}
	if _, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      unterminated,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
	}); err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	stored := readSourceContent(t, root)
	if stored != unterminated {
		t.Fatalf("the note on disk is not what was submitted:\n%q", stored)
	}
	// The vault reads back what was stored, which is the whole claim: this is a
	// note, not a file only this API can produce.
	doc, err := frontmatter.Parse([]byte(stored))
	if err != nil {
		t.Fatalf("the vault cannot parse what this surface just stored: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Fatalf("the unterminated block was read as properties: %v", doc.Front.Keys())
	}
}

// TestSaveSourceRefusesAStaleBaseRevision is the stale case. The refusal has to
// be typed, because the only useful answer to it is a merge and a caller cannot
// offer one from a message.
func TestSaveSourceRefusesAStaleBaseRevision(t *testing.T) {
	root, cfg := sourceTestVault(t)

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      sourceEdit,
		BaseRevision: vault.HashBytes([]byte("a version this vault never held")),
	})
	if result != nil {
		t.Fatalf("a refused save returned a result: %+v", result)
	}
	if !IsSourceRevisionConflict(err) {
		t.Fatalf("SaveSource = %T (%v), want a source revision conflict", err, err)
	}
	if code := CodeOf(err); code != CodeConflict {
		t.Fatalf("code = %q, want %q", code, CodeConflict)
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("the refusal must say nothing was written; it said %q", err)
	}
	var conflict *SourceRevisionError
	if !errors.As(err, &conflict) || conflict.Actual == "" {
		t.Fatalf("the conflict must name the revision the note is actually at: %+v", conflict)
	}
	assertSourceUnchanged(t, root)
	if got := sourceJournalEntryCount(t, root); got != 0 {
		t.Fatalf("journal entries = %d, want none", got)
	}
}

// TestSaveSourceConvergesOnBytesTheNoteAlreadyHolds is the no-op case: a success
// that changed nothing, reporting the revision the note is at, and leaving no
// undo entry and no empty commit behind.
func TestSaveSourceConvergesOnBytesTheNoteAlreadyHolds(t *testing.T) {
	root, cfg := sourceTestVault(t)
	base := vault.HashBytes([]byte(sourceFixture))

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      sourceFixture,
		BaseRevision: base,
	})
	if err != nil {
		t.Fatalf("SaveSource of identical bytes: %v", err)
	}
	if result.Changed || result.Journal != nil {
		t.Fatalf("result = %+v, want a change-nothing success", result)
	}
	if result.Revision != base {
		t.Fatalf("revision = %q, want %q", result.Revision, base)
	}
	assertSourceUnchanged(t, root)
	assertNoJournalEntries(t, root)
}

// TestSaveSourceAnswersAResubmissionAfterALostResponse is the lost-response
// case, and the reason convergence is checked before staleness.
//
// The second call carries the revision the first one replaced, so a naive
// comparison would call it a conflict and tell somebody their edit was rejected
// while the file on disk holds exactly what they typed.
func TestSaveSourceAnswersAResubmissionAfterALostResponse(t *testing.T) {
	root, cfg := sourceTestVault(t)
	base := vault.HashBytes([]byte(sourceFixture))

	first, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: sourceEdit, BaseRevision: base,
	})
	if err != nil {
		t.Fatalf("first save: %v", err)
	}

	second, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: sourceEdit, BaseRevision: base,
	})
	if err != nil {
		t.Fatalf("the resubmission was refused: %v", err)
	}
	if second.Changed {
		t.Fatalf("the resubmission claimed to write: %+v", second)
	}
	if second.Revision != first.Revision {
		t.Fatalf("revision = %q, want the one the first save landed at %q", second.Revision, first.Revision)
	}
	if got := sourceJournalEntryCount(t, root); got != 1 {
		t.Fatalf("journal entries = %d, want 1 — the resubmission must not record a second", got)
	}
}

// TestSaveSourceReplaysASaveIDOnceTheNoteHasMovedOn is the duplicate case that
// convergence cannot answer: the same submission arrives twice and a third
// writer has changed the note in between, so the bytes on disk are nobody's
// second copy of anything.
func TestSaveSourceReplaysASaveIDOnceTheNoteHasMovedOn(t *testing.T) {
	root, cfg := sourceTestVault(t)
	base := vault.HashBytes([]byte(sourceFixture))

	first, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: sourceEdit, BaseRevision: base, SaveID: "editor-1",
	})
	if err != nil {
		t.Fatalf("keyed save: %v", err)
	}
	// Somebody else saves over it, so the submitted bytes are no longer on disk.
	if _, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      "---\ntitle: Alpha\n---\n\nsomebody else.\n",
		BaseRevision: first.Revision,
	}); err != nil {
		t.Fatalf("the intervening save: %v", err)
	}

	replay, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: sourceEdit, BaseRevision: base, SaveID: "editor-1",
	})
	if err != nil {
		t.Fatalf("the replayed save: %v", err)
	}
	if !replay.Replayed || replay.Changed {
		t.Fatalf("result = %+v, want replayed and unchanged", replay)
	}
	if replay.Revision != first.Revision {
		t.Fatalf("revision = %q, want the recorded %q", replay.Revision, first.Revision)
	}
	// The replay is an answer, not a rewrite: the other writer's note stands.
	if got := sourceJournalEntryCount(t, root); got != 2 {
		t.Fatalf("journal entries = %d, want 2 — the replay must record nothing", got)
	}
}

// TestSaveSourceRefusesASaveIDCarryingDifferentContent is the one case where
// refusing loudly is the only answer that cannot lose an edit. Replaying the
// recorded outcome would report success for bytes nobody stored.
func TestSaveSourceRefusesASaveIDCarryingDifferentContent(t *testing.T) {
	root, cfg := sourceTestVault(t)
	base := vault.HashBytes([]byte(sourceFixture))

	first, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: sourceEdit, BaseRevision: base, SaveID: "editor-1",
	})
	if err != nil {
		t.Fatalf("keyed save: %v", err)
	}

	other := "---\ntitle: Alpha\n---\n\na different edit.\n"
	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: other, BaseRevision: first.Revision, SaveID: "editor-1",
	})
	if result != nil {
		t.Fatalf("the refusal returned a result: %+v", result)
	}
	if code := CodeOf(err); code != CodeConflict {
		t.Fatalf("SaveSource = %v (code %q), want %q", err, code, CodeConflict)
	}
	if !strings.Contains(err.Error(), "fresh save id") {
		t.Fatalf("the refusal must say what to do about it; it said %q", err)
	}
	if strings.Contains(readSourceContent(t, root), "a different edit") {
		t.Fatal("a refused save was written anyway")
	}
}

// TestSaveSourceRefusesUnstorableContentBeforeReplayingASaveID pins the
// validation order. A reused id carrying content that cannot be stored must be
// refused, not answered with what that id did last time — the answer would be a
// success for bytes this call never accepted.
func TestSaveSourceRefusesUnstorableContentBeforeReplayingASaveID(t *testing.T) {
	root, cfg := sourceTestVault(t)
	base := vault.HashBytes([]byte(sourceFixture))

	if _, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: sourceEdit, BaseRevision: base, SaveID: "editor-1",
	}); err != nil {
		t.Fatalf("keyed save: %v", err)
	}

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      "---\ntitle: [unterminated\n---\n\nbody\n",
		BaseRevision: base,
		SaveID:       "editor-1",
	})
	if result != nil {
		t.Fatalf("unstorable content was answered with %+v", result)
	}
	if code := CodeOf(err); code != CodeInvalidRequest {
		t.Fatalf("SaveSource = %v (code %q), want %q", err, code, CodeInvalidRequest)
	}
}

func TestSaveSourceCreateAndSaveDisagreeAboutExistence(t *testing.T) {
	root, cfg := sourceTestVault(t)

	t.Run("create over an existing note", func(t *testing.T) {
		_, err := SaveSource(root, cfg, SaveSourceRequest{
			RelPath: "Alpha.md", Content: sourceEdit, Create: true,
		})
		if code := CodeOf(err); code != CodeConflict {
			t.Fatalf("err = %v (code %q), want %q", err, code, CodeConflict)
		}
		assertSourceUnchanged(t, root)
	})

	t.Run("create over a note that already holds the bytes", func(t *testing.T) {
		result, err := SaveSource(root, cfg, SaveSourceRequest{
			RelPath: "Alpha.md", Content: sourceFixture, Create: true,
		})
		if err != nil {
			t.Fatalf("a repeated create was refused: %v", err)
		}
		if result.Changed {
			t.Fatalf("result = %+v, want a change-nothing success", result)
		}
		if result.Revision != vault.HashBytes([]byte(sourceFixture)) {
			t.Fatalf("revision = %q, want the note's own", result.Revision)
		}
	})

	t.Run("save of a note that is not there", func(t *testing.T) {
		_, err := SaveSource(root, cfg, SaveSourceRequest{
			RelPath:      "Notes/Nothing.md",
			Content:      sourceEdit,
			BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		})
		if code := CodeOf(err); code != CodeNotFound {
			t.Fatalf("err = %v (code %q), want %q", err, code, CodeNotFound)
		}
	})

	t.Run("create with a base revision", func(t *testing.T) {
		_, err := SaveSource(root, cfg, SaveSourceRequest{
			RelPath:      "Notes/New.md",
			Content:      sourceEdit,
			BaseRevision: vault.HashBytes([]byte(sourceFixture)),
			Create:       true,
		})
		if code := CodeOf(err); code != CodeInvalidRequest {
			t.Fatalf("err = %v (code %q), want %q", err, code, CodeInvalidRequest)
		}
	})

	t.Run("save with no base revision", func(t *testing.T) {
		_, err := SaveSource(root, cfg, SaveSourceRequest{RelPath: "Alpha.md", Content: sourceEdit})
		if code := CodeOf(err); code != CodeInvalidRequest {
			t.Fatalf("err = %v (code %q), want %q", err, code, CodeInvalidRequest)
		}
		assertSourceUnchanged(t, root)
	})
}

func TestSaveSourceCreatesANewNoteAndItsParents(t *testing.T) {
	root, cfg := sourceTestVault(t)

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Drafts/Deep/New.md", Content: sourceEdit, Create: true,
	})
	if err != nil {
		t.Fatalf("SaveSource create: %v", err)
	}
	if !result.Created || !result.Changed {
		t.Fatalf("result = %+v, want a created change", result)
	}
	stored, err := os.ReadFile(filepath.Join(root, "Drafts", "Deep", "New.md"))
	if err != nil {
		t.Fatalf("read the created note: %v", err)
	}
	if string(stored) != sourceEdit {
		t.Fatalf("the created note is not what was submitted:\n%q", stored)
	}
	if result.Journal == nil {
		t.Fatal("a create recorded no undo entry, so `bn undo` cannot remove the note")
	}
}

// TestSaveSourceRefusesAStaleConfigurationPin is the session-3 precondition
// applied to this surface. Whether the properties block a save submits is
// acceptable is a question about the vault's declarations, and answering it
// against a configuration that has since been replaced is answering the wrong
// question.
func TestSaveSourceRefusesAStaleConfigurationPin(t *testing.T) {
	root, cfg := sourceTestVault(t)
	base := vault.HashBytes([]byte(sourceFixture))

	result, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: sourceEdit, BaseRevision: base,
	}, WithSourceRequire(filepath.Join(root, "brainnotes.toml"),
		vault.ExpectHash(vault.HashBytes([]byte("a configuration this vault does not have")))))
	if result != nil {
		t.Fatalf("a refused save returned a result: %+v", result)
	}
	if code := CodeOf(err); code != CodeConflict {
		t.Fatalf("SaveSource = %v (code %q), want %q", err, code, CodeConflict)
	}
	assertSourceUnchanged(t, root)
	assertNoJournalEntries(t, root)
}
