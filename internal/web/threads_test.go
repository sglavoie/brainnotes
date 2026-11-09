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

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/threads"
	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestThreadPreparePreservesWritingAndUsesSourceSave(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{Hosted: true})
	original := "---\ntitle: Inquiry\n---\n\n" + threads.NewBody("Why?") + "My draft.\n"
	writeTestFile(t, root, "Threads/Inquiry.md", original)
	form := url.Values{"content": {original}, "action": {"add"}, "source": {"alpha"}, "source_revision": {vault.HashBytes([]byte(sourceTestNote))}, "quote": {"Alpha body"}, "commentary": {"This matters."}, "group": {"Evidence"}}
	response := postSourceRoute(t, ts, threadPrepareAPI, form, sourceTestToken)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("prepare: %d %s", response.StatusCode, body)
	}
	var prepared threadPrepared
	threadRequireNoError(t, json.NewDecoder(response.Body).Decode(&prepared))
	if len(prepared.Items) != 1 || prepared.Items[0].Warning != "" || prepared.Items[0].Key != "alpha" {
		t.Fatalf("items: %+v", prepared.Items)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Threads/Inquiry.md")); string(got) != original {
		t.Fatal("prepare wrote a file")
	}
	if !strings.HasSuffix(prepared.Content, "My draft.\n") {
		t.Fatal("draft lost")
	}
	save := url.Values{"key": {"Threads/Inquiry"}, "content": {prepared.Content}, "revision": {vault.HashBytes([]byte(original))}, "save_id": {"thread-save-test"}}
	result := decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, save, sourceTestToken))
	if !result.Changed || result.JournalID == "" {
		t.Fatalf("save: %+v", result)
	}
	replay := decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, save, sourceTestToken))
	if !replay.Replayed {
		t.Fatalf("replay: %+v", replay)
	}
	assertThreadStaleSave(t, ts, root, save, prepared.Content, result.Revision)
}

func TestThreadsSurviveFreshServerAndSourceChanges(t *testing.T) {
	doc, _ := threads.Parse(threads.NewBody("Why?"))
	if err := doc.Add(threads.Item{Group: "Evidence", Link: "[[Alpha]]", Quote: "An old quotation", Revision: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := doc.Add(threads.Item{Group: "Still unclear", Link: "[[Missing]]", Quote: "Keep this too", Revision: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	// Only ordinary Markdown is provided to this new vault/server: no browser or sidecar state.
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{"Threads/Inquiry.md": []byte(doc.String() + "A portable draft."), "Alpha.md": []byte("Changed source")})
	for _, route := range []string{"/threads", "/edit?key=threads/inquiry", "/synthesize?source=threads/inquiry"} {
		body := getThreadPage(t, ts.URL+route)
		if route == "/threads" && !strings.Contains(string(body), "Inquiry") {
			t.Fatal("thread not discovered")
		}
		if strings.HasPrefix(route, "/synthesize") && (!strings.Contains(string(body), "Source changed") || !strings.Contains(string(body), "Source is missing")) {
			t.Fatal("source warnings absent")
		}
	}
}

func TestThreadPrepareBoundaries(t *testing.T) {
	for _, mode := range []struct {
		name   string
		opts   Options
		status int
	}{{"local", Options{}, http.StatusOK}, {"hosted", Options{Hosted: true}, http.StatusOK}, {"replica", Options{ReadOnly: true, StateDir: t.TempDir()}, http.StatusForbidden}} {
		t.Run(mode.name, func(t *testing.T) {
			ts, _, _ := newSourceTestServer(t, mode.opts)
			form := url.Values{"content": {threads.NewBody("Q")}, "action": {"inspect"}}
			resp := postSourceRoute(t, ts, threadPrepareAPI, form, sourceTestToken)
			resp.Body.Close()
			if resp.StatusCode != mode.status {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if mode.status != http.StatusOK {
				return
			}
			resp = postSourceRoute(t, ts, threadPrepareAPI, form, "bad-token")
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatal("CSRF accepted")
			}
			form.Set("content", threads.Start+"\nExternal writing\n"+threads.End)
			resp = postSourceRoute(t, ts, threadPrepareAPI, form, sourceTestToken)
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatal("malformed collection accepted")
			}
		})
	}
}

func assertThreadStaleSave(t *testing.T, ts *httptest.Server, root string, save url.Values, content, revision string) {
	t.Helper()
	// Another device or external editor changes the file after preparation.
	newer := content + "A second writer.\n"
	writeTestFile(t, root, "Threads/Inquiry.md", newer)
	save.Set("save_id", "thread-save-stale")
	save.Set("content", content+"My further writing.\n")
	save.Set("revision", revision)
	decodeSourceError(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, save, sourceTestToken), http.StatusConflict, noteops.CodeConflict)
	if got, _ := os.ReadFile(filepath.Join(root, "Threads/Inquiry.md")); string(got) != newer {
		t.Fatal("overwrote concurrent writing")
	}
}

func getThreadPage(t *testing.T, url string) []byte {
	t.Helper()
	response, err := http.Get(url)
	threadRequireNoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	threadRequireNoError(t, err)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", url, response.StatusCode, body)
	}
	return body
}
func threadRequireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
