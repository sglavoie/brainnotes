package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServer_MediaRouteContainment(t *testing.T) {
	ts, root := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md":  []byte("---\ntitle: Source\n---\n![[image.png]]\n![[missing.png]]\n"),
		"image.png":  []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
		"notes/A.md": []byte("---\ntitle: A\n---\nBody\n"),
	})

	noteResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	noteBody := readResponseBody(t, noteResp)
	if noteResp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", noteResp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(noteBody, "/media/image.png") {
		t.Fatalf("expected existing media embed path, got %q", noteBody)
	}
	if !strings.Contains(noteBody, "missing.png") {
		t.Fatalf("expected missing media embed to stay plain text, got %q", noteBody)
	}
	if strings.Contains(noteBody, "/broken?q=missing.png") {
		t.Fatalf("media embed should not route to broken page, got %q", noteBody)
	}

	mediaResp, err := http.Get(ts.URL + "/media/image.png") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /media/image.png: %v", err)
	}
	defer mediaResp.Body.Close()
	if mediaResp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", mediaResp.StatusCode, http.StatusOK)
	}
	if got := mediaResp.Header.Get("Content-Type"); !strings.HasPrefix(got, "image/png") {
		t.Fatalf("content-type: got %q, want image/png...", got)
	}

	traversalResp, err := http.Get(ts.URL + "/media/%2e%2e/secret.png") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET traversal media path: %v", err)
	}
	defer traversalResp.Body.Close()
	if traversalResp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", traversalResp.StatusCode, http.StatusNotFound)
	}

	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(root, "leak.png")
	if err := os.Symlink(outside, link); err == nil {
		symlinkResp, err := http.Get(ts.URL + "/media/leak.png") // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET symlink media path: %v", err)
		}
		defer symlinkResp.Body.Close()
		if symlinkResp.StatusCode != http.StatusNotFound {
			t.Fatalf("status: got %d, want %d", symlinkResp.StatusCode, http.StatusNotFound)
		}
	}
}

func TestServer_BrokenPageEscapesQuery(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	})

	q := url.QueryEscape("<script>alert(1)</script>")
	resp, err := http.Get(ts.URL + "/broken?q=" + q) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /broken: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("unescaped query found in response: %q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("escaped query not found in response: %q", body)
	}
}

func TestServer_BrokenPageIncludesCreateControls(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes/Source.md": []byte("---\ntitle: Source\n---\n[[MiXeD Missing]]\n"),
	}, nil, Options{
		CSRFToken: "csrf-test-token",
	})

	resp, err := http.Get(ts.URL + "/broken") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /broken: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	for _, want := range []string{
		`data-create-note`,
		`data-create-source-key="notes/source"`,
		`data-create-target="MiXeD Missing"`,
		`data-create-csrf="csrf-test-token"`,
		`aria-label="Create missing note"`,
		`MiXeD Missing`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing broken create fragment %q, got %q", want, body)
		}
	}
}
