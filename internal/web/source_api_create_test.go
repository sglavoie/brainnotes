package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

func TestSourceCreateWritesANewNoteAndRefusesAnExistingPath(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})
	content := "---\ntitle: Delta\n---\n\nDelta body.\n"

	form := url.Values{}
	form.Set("rel_path", "Drafts/Delta.md")
	form.Set("content", content)
	payload := decodeSourceSave(t, postSourceRoute(t, ts, sourceCreateAPIRoutePath, form, sourceTestToken))
	if !payload.Created || !payload.Changed {
		t.Fatalf("payload = %+v, want a created change", payload)
	}
	stored, err := os.ReadFile(filepath.Join(root, "Drafts", "Delta.md"))
	if err != nil {
		t.Fatalf("read the created note: %v", err)
	}
	if string(stored) != content {
		t.Fatalf("the created note is not what was submitted:\n%q", stored)
	}

	// The same create again converges: the destination holds exactly these bytes,
	// so this is the create that already happened rather than a second one.
	again := decodeSourceSave(t, postSourceRoute(t, ts, sourceCreateAPIRoutePath, form, sourceTestToken))
	if again.Changed {
		t.Fatalf("the repeated create claimed to write: %+v", again)
	}
	if again.Revision != payload.Revision {
		t.Fatalf("revision = %q, want %q", again.Revision, payload.Revision)
	}

	// A create whose destination holds *different* bytes is the one that must be
	// refused, because succeeding would replace a note nobody asked about.
	form.Set("content", "---\ntitle: Delta\n---\n\nsomebody else's Delta.\n")
	decodeSourceError(t,
		postSourceRoute(t, ts, sourceCreateAPIRoutePath, form, sourceTestToken),
		http.StatusConflict, noteops.CodeConflict)
	if stored, err := os.ReadFile(filepath.Join(root, "Drafts", "Delta.md")); err != nil {
		t.Fatalf("read the created note: %v", err)
	} else if string(stored) != content {
		t.Fatalf("a refused create overwrote the note:\n%q", stored)
	}
}

// TestSourceCreateRefusesAPathThatIsNotAContainedNote covers both halves of the
// location rule over the route: a path that is not a contained Markdown note,
// and one that is but names somewhere vault.Walk never visits. The second half
// matters more here than it reads — a create is the only source request that
// names a brand-new path, so it is the only one that could put a note where no
// query would ever find it and, under `.bnotes/`, where the save's own snapshot
// would commit nothing while the answer reported success.
func TestSourceCreateRefusesAPathThatIsNotAContainedNote(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	for _, relPath := range []string{
		"../escape.md", "/etc/passwd", "Notes/plain.txt", "",
		".bnotes/Sneaky.md", ".git/Sneaky.md", "Templates/Daily.md", "Archive/Old.md",
	} {
		form := url.Values{}
		form.Set("rel_path", relPath)
		form.Set("content", "---\ntitle: X\n---\n\nbody\n")
		resp := postSourceRoute(t, ts, sourceCreateAPIRoutePath, form, sourceTestToken)
		payload := sourceErrorPayload{}
		func() {
			defer resp.Body.Close()
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}()
		code := noteops.ErrorCode(payload.Code)
		if code != noteops.CodeForbidden && code != noteops.CodeInvalidRequest {
			t.Fatalf("rel_path %q answered %d %q, want a refusal", relPath, resp.StatusCode, payload.Code)
		}
		if want := noteops.HTTPStatusForError(&noteops.Error{Code: code}); resp.StatusCode != want {
			t.Fatalf("rel_path %q answered %d for code %q, want %d", relPath, resp.StatusCode, code, want)
		}
	}

	// A refusal has to have written nothing, which is the half a status code
	// cannot show. Only the in-vault paths are checked: the escaping ones name
	// somewhere this assertion has no business reading, and the empty one names
	// the vault root, which of course exists.
	for _, relPath := range []string{
		"Notes/plain.txt", ".bnotes/Sneaky.md", ".git/Sneaky.md", "Templates/Daily.md", "Archive/Old.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath))); !os.IsNotExist(err) {
			t.Fatalf("the refused create left something at %s: %v", relPath, err)
		}
	}
}
