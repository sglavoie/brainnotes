package noteops

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestRoutePathForKeyRoundTrip(t *testing.T) {
	t.Parallel()

	p, err := RoutePathForKey(NoteRoutePrefix, "Folder/My note")
	if err != nil {
		t.Fatalf("RoutePathForKey: %v", err)
	}
	if p != "/n/Folder/My%20note" {
		t.Fatalf("path = %q, want /n/Folder/My%%20note", p)
	}
	key, err := RouteKeyFromPath(NoteRoutePrefix, p)
	if err != nil {
		t.Fatalf("RouteKeyFromPath: %v", err)
	}
	if key != "Folder/My note" {
		t.Fatalf("key = %q, want Folder/My note", key)
	}
}

func TestRoutePathForKeyRejectsMalformedSegments(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"", "folder/", "../escape", "folder/./note"} {
		if _, err := RoutePathForKey(NoteRoutePrefix, key); err == nil {
			t.Fatalf("RoutePathForKey(%q) succeeded, want error", key)
		}
	}
}

func TestResolveRouteNoteUsesGraphKeySemantics(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notePath := filepath.Join(root, "Folder", "Target.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("---\ntitle: Target\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	notes := []*vault.Note{{Path: notePath}}
	if err := notes[0].Load(); err != nil {
		t.Fatalf("load note: %v", err)
	}
	graph := query.BuildGraphForRoot(root, notes)

	note, key, err := ResolveRouteNote(notes, graph, "Folder/Target.md")
	if err != nil {
		t.Fatalf("ResolveRouteNote: %v", err)
	}
	if key != "folder/target" {
		t.Fatalf("key = %q, want folder/target", key)
	}
	if note.Path != notePath {
		t.Fatalf("note path = %q, want %q", note.Path, notePath)
	}
}

func TestResolveRouteNoteClassifiesUnknownKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	graph := query.BuildGraphForRoot(root, nil)
	_, _, err := ResolveRouteNote(nil, graph, "Missing")
	if CodeOf(err) != CodeNotFound {
		t.Fatalf("CodeOf(err) = %q, want %q", CodeOf(err), CodeNotFound)
	}
	if HTTPStatusForError(err) != http.StatusNotFound {
		t.Fatalf("HTTPStatusForError(err) = %d, want %d", HTTPStatusForError(err), http.StatusNotFound)
	}
}
