package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func TestNewNoteCreatesWithoutContextAndReplays(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "hosted"}[hosted], func(t *testing.T) {
			ts, srv, root := newSourceTestServer(t, Options{Hosted: hosted})
			for _, name := range []string{"Alpha.md", "Beta.md"} {
				if err := os.Remove(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
			srv.state.invalidate()
			for i, folder := range []string{"", "Projects/Ideas"} {
				assertNewNoteCreation(t, ts, root, folder, "new-note-test-"+string(rune('a'+i)))
			}
		})
	}
}

func assertNewNoteCreation(t *testing.T, ts *httptest.Server, root, folder, saveID string) {
	t.Helper()
	form := url.Values{"title": {"Café: notes"}, "folder": {folder}, "save_id": {saveID}}
	resp := postSourceRoute(t, ts, newNoteAPIRoutePath, form, sourceTestToken)
	var result sourceSaveResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !result.OK || !result.Created || result.JournalID == "" || result.Key == "" {
		t.Fatalf("create: %+v (status %d)", result, resp.StatusCode)
	}
	data, err := os.ReadFile(filepath.Join(root, result.RelPath))
	if err != nil || !strings.Contains(string(data), "Café: notes") {
		t.Fatalf("note: %q, %v", data, err)
	}
	editor, err := ts.Client().Get(ts.URL + "/edit?key=" + url.QueryEscape(result.Key))
	if err != nil {
		t.Fatal(err)
	}
	editor.Body.Close()
	if editor.StatusCode != http.StatusOK {
		t.Fatalf("editor status: %d", editor.StatusCode)
	}
	assertNewNoteReplay(t, ts, form, result.Revision)
}

func assertNewNoteReplay(t *testing.T, ts *httptest.Server, form url.Values, revision string) {
	t.Helper()
	replay := decodeSourceSave(t, postSourceRoute(t, ts, newNoteAPIRoutePath, form, sourceTestToken))
	if !replay.Replayed || replay.Changed || replay.Revision != revision {
		t.Fatalf("replay: %+v", replay)
	}
	form.Set("save_id", "different-save-id")
	decodeSourceError(t, postSourceRoute(t, ts, newNoteAPIRoutePath, form, sourceTestToken), http.StatusConflict, noteops.CodeConflict)
}

func TestNewNoteRejectsInvalidDestinations(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{Hosted: true})
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ title, folder string }{
		{"", ""}, {"../escape", ""}, {"a\\b", ""}, {".hidden", ""}, {"[[Bad]]", ""},
		{"Fresh", "../outside"}, {"Fresh", "/tmp"}, {"Fresh", "a/../b"},
		{"Fresh", ".git"}, {"Fresh", "Templates"}, {"Fresh", "Archive"}, {"Fresh", "escape"},
		{"Fresh", "C:/outside"}, {"Fresh", "a#b"}, {"Alpha", ""},
	} {
		t.Run(input.title+"/"+input.folder, func(t *testing.T) {
			form := url.Values{"title": {input.title}, "folder": {input.folder}, "save_id": {"invalid-destination"}}
			resp := postSourceRoute(t, ts, newNoteAPIRoutePath, form, sourceTestToken)
			defer resp.Body.Close()
			if resp.StatusCode < 400 || resp.StatusCode >= 500 {
				t.Fatalf("status = %d", resp.StatusCode)
			}
		})
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatal("existing note changed")
	}
}

func TestNewNotePageAndGuards(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		ts, _, root := newSourceTestServer(t, Options{ReadOnly: readOnly, Hosted: !readOnly, StateDir: t.TempDir()})
		resp, err := ts.Client().Get(ts.URL + newNoteRoutePath)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := http.StatusOK
		if readOnly {
			want = http.StatusForbidden
		}
		if resp.StatusCode != want {
			t.Fatalf("page = %d: %s", resp.StatusCode, body)
		}
		if !readOnly && !strings.Contains(string(body), "Create and write") {
			t.Fatal("creation form missing")
		}
		form := url.Values{"title": {"Guarded"}, "save_id": {"guard-test"}}
		decodeSourceError(t, postSourceRoute(t, ts, newNoteAPIRoutePath, form, ""), http.StatusForbidden, noteops.CodeForbidden)
		if readOnly {
			decodeSourceError(t, postSourceRoute(t, ts, newNoteAPIRoutePath, form, sourceTestToken), http.StatusForbidden, noteops.CodeForbidden)
		} else {
			req, _ := http.NewRequest(http.MethodPost, ts.URL+newNoteAPIRoutePath, strings.NewReader(form.Encode()))
			req.Header.Set("Origin", "https://unrelated.example")
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set(csrfHeaderName, sourceTestToken)
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			decodeSourceError(t, resp, http.StatusForbidden, noteops.CodeForbidden)
		}
		if _, err := os.Stat(filepath.Join(root, "Guarded.md")); !os.IsNotExist(err) {
			t.Fatal("refused create wrote a note")
		}
	}
}
