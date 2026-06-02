package noteops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestBulkSetFrontmatterJournalsMultipleFilesInOneEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "A.md", "---\nstatus: draft\ntitle: A\n---\nBody A.\n")
	writeFrontmatterTestFile(t, root, "B.md", "---\nstatus: draft\ntitle: B\n---\nBody B.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	mutations, err := ParseFrontmatterMutations([]string{"status=done"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	result, err := BulkSetFrontmatter(
		root,
		[]BulkSource{
			{Key: "a", RelPath: "A.md"},
			{Key: "b", RelPath: "B.md"},
		},
		mutations,
		&cfg.Frontmatter,
		WithBulkJournalOptions(
			journal.WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-bulk-frontmatter", nil }),
		),
	)
	if err != nil {
		t.Fatalf("BulkSetFrontmatter: %v", err)
	}
	if result.Changed != 2 || result.Unchanged != 0 || len(result.Failures) != 0 {
		t.Fatalf("result counts = changed %d unchanged %d failures %#v", result.Changed, result.Unchanged, result.Failures)
	}
	if result.Journal == nil || result.Journal.ID != "entry-bulk-frontmatter" {
		t.Fatalf("journal = %#v, want one deterministic entry", result.Journal)
	}
	for _, rel := range []string{"A.md", "B.md"} {
		got := readFrontmatterTestFile(t, root, rel)
		if !strings.Contains(got, "status: done") {
			t.Fatalf("%s was not updated:\n%s", rel, got)
		}
		rec := frontmatterRecordForPath(t, *result.Journal, rel)
		if rec.Before.Kind != journal.KindFile || rec.After.Kind != journal.KindFile {
			t.Fatalf("journal record for %s = %#v, want file -> file", rel, rec)
		}
	}
}

func TestBulkTrashNotesContinuesMissingSourcesAndUsesOneJournal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "A.md", "---\ntitle: A\n---\nBody A.\n")

	result, err := BulkTrashNotes(
		root,
		[]BulkSource{
			{Key: "a", RelPath: "A.md"},
			{Key: "missing", RelPath: "Missing.md"},
		},
		WithBulkJournalOptions(
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-bulk-trash", nil }),
		),
	)
	if err != nil {
		t.Fatalf("BulkTrashNotes: %v", err)
	}
	if result.Changed != 1 || len(result.Failures) != 1 {
		t.Fatalf("result = changed %d failures %#v, want one change and one failure", result.Changed, result.Failures)
	}
	if result.Journal == nil || result.Journal.ID != "entry-bulk-trash" {
		t.Fatalf("journal = %#v, want one deterministic entry", result.Journal)
	}
	if _, err := os.Stat(filepath.Join(root, "A.md")); !os.IsNotExist(err) {
		t.Fatalf("source stat err = %v, want missing", err)
	}
	if got := readFrontmatterTestFile(t, root, "Trash/A.md"); !strings.Contains(got, "Body A.") {
		t.Fatalf("trashed note unexpected:\n%s", got)
	}
	if len(result.Journal.Files) != 2 {
		t.Fatalf("journal files = %#v, want source and destination records", result.Journal.Files)
	}
}

func TestBulkMoveNotesJournalsOneEntryAndRewritesLinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Inbox/Source.md", "---\ntitle: Source\n---\nBody.\n")
	writeFrontmatterTestFile(t, root, "Ref.md", "---\ntitle: Ref\n---\nSee [[Inbox/Source]].\n")
	cfg := &config.Config{Vault: root}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}

	result, err := BulkMoveNotes(
		v,
		cfg,
		[]BulkSource{{Key: "inbox/source", RelPath: "Inbox/Source.md"}},
		"Archive",
		WithBulkJournalOptions(
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-bulk-move", nil }),
		),
	)
	if err != nil {
		t.Fatalf("BulkMoveNotes: %v", err)
	}
	if result.Changed != 1 || len(result.Failures) != 0 {
		t.Fatalf("result = changed %d failures %#v, want one clean move", result.Changed, result.Failures)
	}
	if result.Journal == nil || result.Journal.ID != "entry-bulk-move" {
		t.Fatalf("journal = %#v, want one deterministic entry", result.Journal)
	}
	if _, err := os.Stat(filepath.Join(root, "Inbox", "Source.md")); !os.IsNotExist(err) {
		t.Fatalf("source stat err = %v, want missing", err)
	}
	if got := readFrontmatterTestFile(t, root, "Archive/Source.md"); !strings.Contains(got, "Body.") {
		t.Fatalf("moved note unexpected:\n%s", got)
	}
	if got := readFrontmatterTestFile(t, root, "Ref.md"); !strings.Contains(got, "[[Archive/Source]]") {
		t.Fatalf("reference was not rewritten:\n%s", got)
	}
	for _, rel := range []string{"Archive/Source.md", "Inbox/Source.md", "Ref.md"} {
		_ = frontmatterRecordForPath(t, *result.Journal, rel)
	}
}
