package noteops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// sourceFixture is deliberately ugly. The keys are in the order somebody typed
// them rather than alphabetical, the body carries trailing spaces, one CRLF line
// ending and no final newline. Every one of those is a thing a round trip
// through frontmatter.Marshal or a helpful normalization would quietly repair,
// and repairing a person's file while saving it is the failure this surface
// exists to avoid.
const sourceFixture = "---\ntitle: Alpha\nstatus: draft\n---\n\nAlpha body,  two trailing spaces  \r\nsecond line\nno final newline"

// sourceEdit is the same note edited, and just as unsorted.
const sourceEdit = "---\ntitle: Alpha\nstatus: done\n---\n\nAlpha body, edited.\n"

// sourceTestVault builds the vault and the configuration the source API is
// asked to honour, together, because they are one fact: the ignore list and the
// templates directory below are the ones the written brainnotes.toml declares,
// and a test that passed a nil config would exercise a laxer containment rule
// than any caller uses.
func sourceTestVault(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()
	writeCreateTestFile(t, root, "brainnotes.toml", "ignore = [\"Archive\"]\n")
	writeCreateTestFile(t, root, "Alpha.md", sourceFixture)
	return root, &config.Config{
		Vault:     root,
		Ignore:    []string{"Archive"},
		Templates: config.TemplatesConfig{Dir: "Templates"},
	}
}

func sourceJournalEntryCount(t *testing.T, root string) int {
	t.Helper()
	entries, _, err := journal.ListEntries(JournalRoot(root))
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	return len(entries)
}

func TestReadSourceReturnsTheBytesOnDiskAndTheirRevision(t *testing.T) {
	root, cfg := sourceTestVault(t)

	doc, err := ReadSource(root, cfg, "Alpha.md")
	if err != nil {
		t.Fatalf("ReadSource: %v", err)
	}
	if !doc.Exists {
		t.Fatal("Exists = false for a note that is there")
	}
	if doc.Content != sourceFixture {
		t.Fatalf("content was not returned verbatim:\n%q", doc.Content)
	}
	if want := vault.HashBytes([]byte(sourceFixture)); doc.Revision != want {
		t.Fatalf("revision = %q, want the content digest %q", doc.Revision, want)
	}
	if doc.Destination.RelPath != "Alpha.md" {
		t.Fatalf("RelPath = %q, want Alpha.md", doc.Destination.RelPath)
	}
}

// TestReadSourceReportsAMissingNoteRatherThanFailing keeps "there is nothing
// here yet" separate from "this could not be read". A create asks the same
// question a save does, and an error for the ordinary answer would make the
// absence indistinguishable from a permission failure.
func TestReadSourceReportsAMissingNoteRatherThanFailing(t *testing.T) {
	root, cfg := sourceTestVault(t)

	doc, err := ReadSource(root, cfg, "Notes/Nothing.md")
	if err != nil {
		t.Fatalf("ReadSource of a missing note: %v", err)
	}
	if doc.Exists || doc.Revision != "" || doc.Content != "" {
		t.Fatalf("a missing note read back as %+v", doc)
	}
	if doc.Destination.RelPath != "Notes/Nothing.md" {
		t.Fatalf("RelPath = %q, want the requested path", doc.Destination.RelPath)
	}
}

func TestReadSourceRefusesWhatIsNotAContainedNote(t *testing.T) {
	root, cfg := sourceTestVault(t)

	cases := map[string]struct {
		relPath string
		code    ErrorCode
	}{
		// Containment is decided by CleanRelPath and is a refusal rather than a
		// malformed request: the path is well formed and names somewhere this
		// operation may not go.
		"outside the vault":  {relPath: "../escape.md", code: CodeForbidden},
		"absolute":           {relPath: "/etc/passwd", code: CodeForbidden},
		"not markdown":       {relPath: "brainnotes.toml", code: CodeInvalidRequest},
		"no extension":       {relPath: "Alpha", code: CodeInvalidRequest},
		"empty":              {relPath: "  ", code: CodeInvalidRequest},
		"markdown directory": {relPath: "Notes/../Alpha.md", code: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadSource(root, cfg, tc.relPath)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("ReadSource(%q) = %v, want it cleaned and accepted", tc.relPath, err)
				}
				return
			}
			if code := CodeOf(err); code != tc.code {
				t.Fatalf("ReadSource(%q) = %v (code %q), want %q", tc.relPath, err, code, tc.code)
			}
		})
	}
}

// TestReadSourceRefusesANoteReachedThroughASymlink keeps the path the client
// named and the path on disk the same thing. A save is pinned to a revision of
// the file it read, so a link that redirects to another note — or out of the
// vault entirely — would pin the wrong file.
func TestReadSourceRefusesANoteReachedThroughASymlink(t *testing.T) {
	root, cfg := sourceTestVault(t)
	if err := os.Symlink(filepath.Join(root, "Alpha.md"), filepath.Join(root, "Link.md")); err != nil {
		t.Skipf("this platform cannot create a symlink: %v", err)
	}

	_, err := ReadSource(root, cfg, "Link.md")
	if code := CodeOf(err); code != CodeForbidden {
		t.Fatalf("ReadSource through a symlink = %v (code %q), want %q", err, code, CodeForbidden)
	}

	_, err = SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Link.md",
		Content:      sourceEdit,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
	})
	if code := CodeOf(err); code != CodeForbidden {
		t.Fatalf("SaveSource through a symlink = %v (code %q), want %q", err, code, CodeForbidden)
	}
	assertSourceUnchanged(t, root)
}

// TestSourceRefusesALocationWalkWouldNeverVisit covers the refusal that is not
// about containment at all. Every path below is inside the vault and ends in
// .md, so CleanRelPath and the extension check both pass — and a note written
// to any of them would be on disk and in no index, no graph, no search and no
// query, because vault.Walk never goes there. Under `.bnotes/` and `.git/` it is
// worse: those are largely outside vaultgit.InScope, so the save's own snapshot
// would commit nothing while the answer reported a revision and a success. Both
// losses are invisible from the client's side, which is the class this surface
// exists to rule out, so they are refused before any artifact exists.
func TestSourceRefusesALocationWalkWouldNeverVisit(t *testing.T) {
	root, cfg := sourceTestVault(t)

	for _, relPath := range []string{
		".bnotes/Sneaky.md",
		".git/Sneaky.md",
		"Templates/Daily.md",
		"Archive/Old.md",
	} {
		t.Run(relPath, func(t *testing.T) {
			if _, err := ReadSource(root, cfg, relPath); CodeOf(err) != CodeForbidden {
				t.Fatalf("ReadSource(%q) = %v, want code %q", relPath, err, CodeForbidden)
			}
			result, err := SaveSource(root, cfg, SaveSourceRequest{
				RelPath: relPath, Content: sourceEdit, Create: true,
			})
			if result != nil {
				t.Fatalf("a refused create was answered with %+v", result)
			}
			if code := CodeOf(err); code != CodeForbidden {
				t.Fatalf("SaveSource(%q) = %v (code %q), want %q", relPath, err, code, CodeForbidden)
			}
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relPath))); !os.IsNotExist(statErr) {
				t.Fatalf("the refused create left something at %s: %v", relPath, statErr)
			}
			assertNoJournalEntries(t, root)
		})
	}
}

// TestSourceLocationRuleWithoutAConfiguration states what a nil configuration
// costs on that rule, because the two halves of it have different sources. A
// hidden component is refused from the path alone, so it holds with no
// configuration at all; the ignore list and the templates directory are
// declarations, and without them there is nothing to consult. That is why the
// callers pass the configuration they planned the save against rather than nil.
func TestSourceLocationRuleWithoutAConfiguration(t *testing.T) {
	root, _ := sourceTestVault(t)

	if _, err := ReadSource(root, nil, ".bnotes/Sneaky.md"); CodeOf(err) != CodeForbidden {
		t.Fatalf("a hidden path was accepted with no configuration: %v", err)
	}
	if _, err := ReadSource(root, nil, "Archive/Old.md"); err != nil {
		t.Fatalf("ReadSource with no configuration = %v, want a declared ignore unenforced", err)
	}
}

func TestValidateSourceRefusesOnlyWhatCannotBeStored(t *testing.T) {
	if err := ValidateSource(sourceFixture); err != nil {
		t.Fatalf("the fixture is storable and was refused: %v", err)
	}
	// No frontmatter at all is a legal note.
	if err := ValidateSource("just a body\n"); err != nil {
		t.Fatalf("a note without frontmatter was refused: %v", err)
	}

	cases := map[string]string{
		"broken frontmatter": "---\ntitle: [unterminated\n---\n\nbody\n",
		"nul byte":           "---\ntitle: Alpha\n---\n\nbo\x00dy\n",
		"too large":          strings.Repeat("a", MaxSourceBytes+1),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateSource(content)
			if code := CodeOf(err); code != CodeInvalidRequest {
				t.Fatalf("ValidateSource = %v (code %q), want %q", err, code, CodeInvalidRequest)
			}
		})
	}
}

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

func readSourceContent(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "Alpha.md"))
	if err != nil {
		t.Fatalf("read Alpha.md: %v", err)
	}
	return string(data)
}

func assertSourceUnchanged(t *testing.T, root string) {
	t.Helper()
	if got := readSourceContent(t, root); got != sourceFixture {
		t.Fatalf("Alpha.md changed:\n%q", got)
	}
}
