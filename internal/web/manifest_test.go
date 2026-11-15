package web

import (
	"encoding/json"
	"image/png"
	"io/fs"
	"mime"
	"net/http"
	"strings"
	"testing"
)

func TestPageTemplates_IncludeManifestHead(t *testing.T) {
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
				if !found || !strings.Contains(head, `{{template "themeBoot" .}}`) {
					t.Fatal("page head must include themeBoot for the manifest and window colors")
				}
			}
		})
	}
}

func TestServer_AppManifest(t *testing.T) {
	for _, mode := range []struct {
		name string
		opts Options
	}{
		{"local", Options{}},
		{"hosted", Options{Hosted: true}},
		{"read-only", Options{ReadOnly: true, StateDir: t.TempDir()}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
				"Note.md": []byte("---\ntitle: Note\n---\nBody\n"),
			}, nil, mode.opts)
			assertManifestPageHead(t, ts.URL)
			resp := getAppAsset(t, ts.URL, "/static/manifest.webmanifest")
			mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
			if err != nil || (mediaType != "application/manifest+json" && mediaType != "application/json") {
				t.Fatalf("manifest Content-Type = %q: %v", mediaType, err)
			}
			var manifest struct {
				appManifestFields
				Icons []appManifestIcon `json:"icons"`
			}
			if err := json.Unmarshal([]byte(readResponseBody(t, resp)), &manifest); err != nil {
				t.Fatal(err)
			}
			want := appManifestFields{
				Name: "Brain Notes", ShortName: "bn", StartURL: "/", Scope: "/",
				Display: "standalone", BackgroundColor: "#f8f7f3", ThemeColor: "#f8f7f3",
			}
			if manifest.appManifestFields != want {
				t.Fatalf("manifest fields = %+v, want %+v", manifest.appManifestFields, want)
			}
			assertManifestIcons(t, ts.URL, manifest.Icons)
		})
	}
}

type appManifestFields struct {
	Name            string `json:"name"`
	ShortName       string `json:"short_name"`
	StartURL        string `json:"start_url"`
	Scope           string `json:"scope"`
	Display         string `json:"display"`
	BackgroundColor string `json:"background_color"`
	ThemeColor      string `json:"theme_color"`
}

type appManifestIcon struct {
	Src   string `json:"src"`
	Sizes string `json:"sizes"`
	Type  string `json:"type"`
}

func getAppAsset(t *testing.T, baseURL, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(baseURL + path) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("GET %s: Cache-Control = %q", path, got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("GET %s: X-Content-Type-Options = %q", path, got)
	}
	return resp
}

func assertManifestPageHead(t *testing.T, baseURL string) {
	t.Helper()
	for _, path := range []string{"/", "/library", "/n/note", "/graph", "/pinned"} {
		resp, err := http.Get(baseURL + path) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatal(err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
		_, section, _ := strings.Cut(body, "<head>")
		head, _, found := strings.Cut(section, "</head>")
		for _, want := range []string{
			`<link rel="manifest" href="/static/manifest.webmanifest">`,
			`<meta name="theme-color" content="#f8f7f3" media="(prefers-color-scheme: light)">`,
			`<meta name="theme-color" content="#171b19" media="(prefers-color-scheme: dark)">`,
		} {
			if !found || strings.Count(head, want) != 1 {
				t.Errorf("GET %s: head must contain exactly one %s", path, want)
			}
		}
	}
}

func assertManifestIcons(t *testing.T, baseURL string, icons []appManifestIcon) {
	t.Helper()
	wants := map[appManifestIcon]int{
		{Src: "/static/icon-192.png", Sizes: "192x192", Type: "image/png"}: 192,
		{Src: "/static/icon-512.png", Sizes: "512x512", Type: "image/png"}: 512,
		{Src: "/static/favicon.svg", Sizes: "any", Type: "image/svg+xml"}:  0,
	}
	if len(icons) != len(wants) {
		t.Fatalf("icons = %+v, want two PNGs and the SVG", icons)
	}
	for _, icon := range icons {
		size, ok := wants[icon]
		if !ok {
			t.Fatalf("unexpected or duplicate icon: %+v", icon)
		}
		delete(wants, icon)
		resp := getAppAsset(t, baseURL, icon.Src)
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, icon.Type) {
			t.Errorf("%s Content-Type = %q, want %s", icon.Src, got, icon.Type)
		}
		if size == 0 {
			continue // SVG shape is covered by TestServer_Favicon.
		}
		img, err := png.Decode(resp.Body)
		if err != nil {
			t.Fatalf("decode %s: %v", icon.Src, err)
		}
		if img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Errorf("%s dimensions = %v, want %dx%d", icon.Src, img.Bounds(), size, size)
		}
	}
}
