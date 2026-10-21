package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/query"
)

func TestNotePathForKey_RoundTrip(t *testing.T) {
	t.Parallel()

	path, err := notePathForKey("Folder/My note")
	if err != nil {
		t.Fatalf("notePathForKey: %v", err)
	}
	if want := "/n/Folder/My%20note"; path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	key, err := noteKeyFromPath(path)
	if err != nil {
		t.Fatalf("noteKeyFromPath: %v", err)
	}
	if want := "Folder/My note"; key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
}

func TestNotePathForKey_RejectsEmptyKey(t *testing.T) {
	t.Parallel()

	if _, err := notePathForKey(""); err == nil {
		t.Fatal("expected empty key error")
	}
	if _, err := notePathForKey("folder/"); err == nil {
		t.Fatal("expected trailing slash segment error")
	}
	if _, err := notePathForKey("../escape"); err == nil {
		t.Fatal("expected dot-segment rejection")
	}
}

func TestNotePathForNote_UsesGraphKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notePath := filepath.Join(root, "Team", "Plan Note.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("---\ntitle: Plan Note\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	_ = cfg

	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	graph := query.BuildGraphForRoot(v.Root, notes)

	got, err := notePathForNote(graph, notes[0])
	if err != nil {
		t.Fatalf("notePathForNote: %v", err)
	}
	if want := "/n/team/plan%20note"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestMediaPathForRel_RoundTrip(t *testing.T) {
	t.Parallel()

	path, err := mediaPathForRel("Assets/My image.png")
	if err != nil {
		t.Fatalf("mediaPathForRel: %v", err)
	}
	if want := "/media/Assets/My%20image.png"; path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	rel, err := mediaRelFromPath(path)
	if err != nil {
		t.Fatalf("mediaRelFromPath: %v", err)
	}
	if want := "Assets/My image.png"; rel != want {
		t.Fatalf("rel = %q, want %q", rel, want)
	}
}
