package noteops

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/journal"
)

func TestCreateStubFromBrokenTargetBareTargetCreatesBesideSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Notes/Source.md", "---\ntitle: Source\n---\n[[Missing Note]]\n")

	result, err := CreateStubFromBrokenTarget(root, "Notes/Source.md", "Missing Note")
	if err != nil {
		t.Fatalf("CreateStubFromBrokenTarget: %v", err)
	}
	if result.Destination.RelPath != "Notes/Missing Note.md" {
		t.Fatalf("destination = %q, want Notes/Missing Note.md", result.Destination.RelPath)
	}
	if result.Title != "Missing Note" {
		t.Fatalf("title = %q, want Missing Note", result.Title)
	}
	assertCreateTestFile(t, root, "Notes/Missing Note.md", "---\ntitle: Missing Note\n---\n")
}

func TestCreateStubFromBrokenTargetFolderTargetIgnoresHeadingAndAlias(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Notes/Source.md", "---\ntitle: Source\n---\n[[Projects/Roadmap#Next|plan]]\n")

	result, err := CreateStubFromBrokenTarget(root, "Notes/Source.md", "[[Projects/Roadmap#Next|plan]]")
	if err != nil {
		t.Fatalf("CreateStubFromBrokenTarget: %v", err)
	}
	if result.Destination.RelPath != "Projects/Roadmap.md" {
		t.Fatalf("destination = %q, want Projects/Roadmap.md", result.Destination.RelPath)
	}
	assertCreateTestFile(t, root, "Projects/Roadmap.md", "---\ntitle: Roadmap\n---\n")
}

func TestCreateStubFromBrokenTargetPreservesTargetCasing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[MiXeD Case]]\n")

	result, err := CreateStubFromBrokenTarget(root, "Source.md", "MiXeD Case")
	if err != nil {
		t.Fatalf("CreateStubFromBrokenTarget: %v", err)
	}
	if result.Destination.RelPath != "MiXeD Case.md" {
		t.Fatalf("destination = %q, want MiXeD Case.md", result.Destination.RelPath)
	}
	assertCreateTestFile(t, root, "MiXeD Case.md", "---\ntitle: MiXeD Case\n---\n")
}

func TestCreateStubFromBrokenTargetRejectsCollision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	writeCreateTestFile(t, root, "Missing.md", "---\ntitle: Missing\n---\nExisting\n")

	_, err := CreateStubFromBrokenTarget(root, "Source.md", "Missing")
	if CodeOf(err) != CodeConflict {
		t.Fatalf("CodeOf(err) = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	assertCreateTestFile(t, root, "Missing.md", "---\ntitle: Missing\n---\nExisting\n")
}

func TestCreateStubFromBrokenTargetRejectsInvalidTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[../Escape]]\n")

	for _, target := range []string{"../Escape", "Folder//Missing", "[[unterminated", "Bad[Title"} {
		t.Run(target, func(t *testing.T) {
			_, err := CreateStubFromBrokenTarget(root, "Source.md", target)
			if CodeOf(err) != CodeInvalidRequest {
				t.Fatalf("CodeOf(err) = %q, want %q (%v)", CodeOf(err), CodeInvalidRequest, err)
			}
		})
	}
}

func TestCreateStubFromBrokenTargetJournalsCreatedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	result, err := CreateStubFromBrokenTarget(
		root,
		"Source.md",
		"Missing",
		WithCreateJournalOptions(
			journal.WithClock(func() time.Time { return fixed }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-create", nil }),
		),
	)
	if err != nil {
		t.Fatalf("CreateStubFromBrokenTarget: %v", err)
	}
	if result.Journal == nil {
		t.Fatal("journal entry = nil, want entry")
	}
	if result.Journal.ID != "entry-create" {
		t.Fatalf("journal ID = %q, want entry-create", result.Journal.ID)
	}
	rec := createRecordForPath(t, *result.Journal, "Missing.md")
	if rec.Before.Kind != journal.KindMissing || rec.After.Kind != journal.KindFile {
		t.Fatalf("journal record = %#v, want missing -> file", rec)
	}
	if _, err := os.Stat(filepath.Join(JournalRoot(root), "entry-create", "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}
}

func TestPreviewStubFromBrokenTargetResolvesWithoutWriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Notes/Source.md", "---\ntitle: Source\n---\n[[Missing Note]]\n")

	preview, err := PreviewStubFromBrokenTarget(root, "Notes/Source.md", "Missing Note")
	if err != nil {
		t.Fatalf("PreviewStubFromBrokenTarget: %v", err)
	}
	if preview.Destination.RelPath != "Notes/Missing Note.md" {
		t.Fatalf("destination = %q, want Notes/Missing Note.md", preview.Destination.RelPath)
	}
	if preview.Title != "Missing Note" {
		t.Fatalf("title = %q, want Missing Note", preview.Title)
	}
	if _, err := os.Stat(filepath.Join(root, "Notes", "Missing Note.md")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote a file: err=%v", err)
	}
}

func TestPreviewStubFromBrokenTargetReportsCollision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Source.md", "---\ntitle: Source\n---\n[[Missing]]\n")
	writeCreateTestFile(t, root, "Missing.md", "---\ntitle: Missing\n---\nExisting\n")

	_, err := PreviewStubFromBrokenTarget(root, "Source.md", "Missing")
	if CodeOf(err) != CodeConflict {
		t.Fatalf("CodeOf(err) = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
}

func writeCreateTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func assertCreateTestFile(t *testing.T, root, rel, want string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	if string(data) != want {
		t.Fatalf("%q = %q, want %q", rel, string(data), want)
	}
}

func createRecordForPath(t *testing.T, entry journal.Entry, rel string) journal.FileRecord {
	t.Helper()
	for _, rec := range entry.Files {
		if rec.Path == rel {
			return rec
		}
	}
	t.Fatalf("entry missing record for %q: %#v", rel, entry.Files)
	return journal.FileRecord{}
}
