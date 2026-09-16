package web

import (
	"encoding/xml"
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestServer_Favicon(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody\n"),
	})
	resp, err := http.Get(ts.URL + "/static/favicon.svg") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatal(err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("favicon status: got %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Fatalf("favicon Content-Type: got %q", got)
	}
	var svg struct {
		XMLName xml.Name `xml:"http://www.w3.org/2000/svg svg"`
		ViewBox string   `xml:"viewBox,attr"`
	}
	if err := xml.Unmarshal([]byte(body), &svg); err != nil {
		t.Fatalf("invalid favicon SVG: %v", err)
	}
	if svg.ViewBox != "0 0 32 32" {
		t.Fatalf("favicon viewBox: got %q", svg.ViewBox)
	}
}

func TestPageTemplates_LinkFavicon(t *testing.T) {
	paths, err := fs.Glob(embeddedFiles, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			body, err := embeddedFiles.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, section := range strings.Split(string(body), "<head>")[1:] {
				head, _, found := strings.Cut(section, "</head>")
				if !found || !strings.Contains(head, `rel="icon" type="image/svg+xml" sizes="any" href="/static/favicon.svg"`) {
					t.Fatal("page head is missing its SVG favicon link")
				}
			}
		})
	}
}
