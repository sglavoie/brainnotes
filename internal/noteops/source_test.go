package noteops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestSourceLedgerPruningRemainsInsideCheckpointAdmission(t *testing.T) {
	root, cfg := sourceTestVault(t)
	dir := filepath.Join(root, ".bnotes", "source-saves")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < sourceSaveLedgerCap; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("old-%03d.json", i)), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	joined := false
	sourceLedgerPruneHook = func() { joined = checkpoint.Joined(root) }
	t.Cleanup(func() { sourceLedgerPruneHook = nil })
	_, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath: "Alpha.md", Content: sourceEdit,
		BaseRevision: vault.HashBytes([]byte(sourceFixture)), SaveID: "checkpoint-prune",
	})
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if !joined {
		t.Fatal("source receipt pruning ran after the operation released its checkpoint admission")
	}
}

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
