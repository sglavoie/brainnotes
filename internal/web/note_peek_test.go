package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/brief"
)

func TestNotePeekIsBoundedAndDoesNotRecordActivity(t *testing.T) {
	ts, root := newHTTPServerForFiles(t, map[string][]byte{
		"Notes/Café.md": []byte("# Café\n\nAn introduction:\n\n" + strings.Repeat("Useful writing about a specific idea. ", 30)),
	})
	response, err := ts.Client().Get(ts.URL + "/api/note/peek?key=" + url.QueryEscape("Notes/Café"))
	if err != nil {
		t.Fatal(err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("peek: %d %s", response.StatusCode, body)
	}
	var result struct{ Title, Excerpt, Path, Modified string }
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	if len([]rune(result.Excerpt)) > brief.DefaultCardExcerptRunes || !strings.HasPrefix(result.Excerpt, "Useful writing") {
		t.Fatalf("unhelpful or unbounded excerpt: %q", result.Excerpt)
	}
	assertPeekModified(t, filepath.Join(root, "Notes/Café.md"), result.Modified)
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("peek must not be cached")
	}
	for _, name := range []string{"recall.json", "brief.json"} {
		if _, err := os.Stat(filepath.Join(root, ".bnotes", name)); !os.IsNotExist(err) {
			t.Fatalf("peek wrote %s", name)
		}
	}
	assertPeekUnknownPaths(t, ts.Client(), ts.URL)
}

func assertPeekModified(t *testing.T, path, modified string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || modified != info.ModTime().Format(time.RFC3339) {
		t.Fatalf("peek modification date does not match the source: %q, %v", modified, err)
	}
}

func assertPeekUnknownPaths(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	for _, key := range []string{"missing", "../Notes/Café", "/etc/passwd"} {
		resp, err := client.Get(baseURL + "/api/note/peek?key=" + url.QueryEscape(key))
		if err != nil {
			t.Fatal(err)
		}
		readResponseBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("key %q: status %d", key, resp.StatusCode)
		}
	}
}

func TestBriefBrowserOpenWorksWithoutLaunchingAnEditor(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "hosted"}[hosted], func(t *testing.T) {
			now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
			ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{"Source.md": []byte("# Source\n\nA substantive note to resume.\n")}, nil, Options{
				Hosted: hosted, CSRFToken: "token", BriefNow: func() time.Time { return now },
				StartEditor: func(string, []string) error { t.Error("browser action launched a process"); return nil },
			})
			form := url.Values{"card_id": {string(mustBriefCardID(t, now, brief.SlotContinue, "Source.md", ""))}, "action": {"open"}, "target": {"browser"}, "token": {"token"}}
			response, err := ts.Client().PostForm(ts.URL+"/api/brief/item", form)
			if err != nil {
				t.Fatal(err)
			}
			body := readResponseBody(t, response)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("open: %d %s", response.StatusCode, body)
			}
			result := decodeBriefItemResponse(t, body)
			if result.Destination != "/edit?key=source" || result.Activity.Last7Days.OpenCount != 1 {
				t.Fatalf("unexpected open: %#v", result)
			}
		})
	}
}
