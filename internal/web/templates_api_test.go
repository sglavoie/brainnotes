package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// templateTestBody is what a real template looks like, and it is the reason this
// surface never parses one: the frontmatter block holds an unrendered action, so
// a YAML parser refuses a file `bn new` renders perfectly.
const templateTestBody = "---\ntitle: {{ .Title }}\ncreated: {{ today }}\ntags:\n    - {{ .Vars.topic }}\n---\n\n# {{ .Title }}\n"

func newTemplatesServer(t *testing.T, files map[string]string) (*httptest.Server, *Server, string) {
	t.Helper()

	seeded := propertyTestNotes()
	for rel, body := range files {
		seeded[rel] = body
	}
	return newPropertyServer(t, propertyTestConfigTOML, seeded)
}

func newTemplatesFixtureServer(t *testing.T) (*httptest.Server, *Server, string) {
	t.Helper()
	return newTemplatesServer(t, map[string]string{"Templates/Daily.md": templateTestBody})
}

func getTemplates(t *testing.T, baseURL, name string) (int, templatesResponse, string) {
	t.Helper()

	target := baseURL + templatesAPIRoutePath
	if name != "" {
		target += "?name=" + url.QueryEscape(name)
	}
	resp, err := http.Get(target) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	raw := readResponseBody(t, resp)
	var payload templatesResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode templates body %q: %v", raw, err)
		}
	}
	return resp.StatusCode, payload, raw
}

func postTemplates(t *testing.T, baseURL, route, token string, body any) (int, templatesResponse, string) {
	t.Helper()

	status, raw := postProperty(t, baseURL, route, token, body)
	var payload templatesResponse
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode templates body %q: %v", raw, err)
		}
	}
	return status, payload, raw
}

func previewTemplate(t *testing.T, baseURL string, req templateMutationRequest) templatesResponse {
	t.Helper()

	status, payload, raw := postTemplates(t, baseURL, templatesPreviewAPIRoutePath, propertyTestToken, req)
	if status != http.StatusOK {
		t.Fatalf("template preview status = %d, body %q", status, raw)
	}
	if payload.PreviewID == "" {
		t.Fatalf("template preview issued no receipt: %q", raw)
	}
	return payload
}

func applyTemplate(t *testing.T, baseURL, previewID string) (int, templatesResponse, string) {
	t.Helper()
	return postTemplates(t, baseURL, templatesApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: previewID})
}

// saveTemplate is the whole round trip, for the tests whose subject is what
// happens afterwards rather than the write itself.
func saveTemplate(t *testing.T, baseURL, name, content, revision string) templatesResponse {
	t.Helper()

	preview := previewTemplate(t, baseURL, templateMutationRequest{
		Op: templateOpSave, Name: name, Content: content, Revision: revision,
	})
	status, payload, raw := applyTemplate(t, baseURL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("template apply status = %d, body %q", status, raw)
	}
	return payload
}

func templateNames(payload templatesResponse) []string {
	out := make([]string, 0, len(payload.Templates))
	for _, summary := range payload.Templates {
		out = append(out, summary.Name)
	}
	return out
}

// TestTemplatesList_DescribesTheConfiguredDirectory is the claim this surface is
// accountable for: a template is out of every note walk and still travels.
func TestTemplatesList_DescribesTheConfiguredDirectory(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	status, payload, raw := getTemplates(t, ts.URL, "")
	if status != http.StatusOK {
		t.Fatalf("GET templates status = %d, body %q", status, raw)
	}
	if payload.Dir != "Templates" || payload.Extension != ".md" {
		t.Fatalf("dir/extension = %q/%q, want Templates/.md", payload.Dir, payload.Extension)
	}
	if !payload.Templating.ExcludedFromNotes {
		t.Fatalf("the templates directory is not excluded from note walking: %#v", payload.Templating)
	}
	if !payload.Templating.Portable {
		t.Fatalf("the templates directory is outside the undo or synchronization scope: %#v", payload.Templating)
	}
	if got := templateNames(payload); len(got) != 1 || got[0] != "Daily.md" {
		t.Fatalf("templates = %v, want [Daily.md]", got)
	}
	if payload.Templates[0].Content != "" {
		t.Fatalf("the listing served a template's bytes: %q", payload.Templates[0].Content)
	}
	if payload.Templates[0].Revision == "" {
		t.Fatalf("the listing carries no revision to plan an edit against")
	}
}

// TestTemplatesList_SkipsWhatItCannotEdit: the listing describes what this
// surface can write, so everything its own name policy refuses is left out.
func TestTemplatesList_SkipsWhatItCannotEdit(t *testing.T) {
	ts, _, root := newTemplatesServer(t, map[string]string{
		"Templates/Daily.md":         templateTestBody,
		"Templates/notes.txt":        "not a template this vault can resolve\n",
		"Templates/.hidden.md":       "invisible to vault.Walk and to template.Find\n",
		"Templates/Nested/Deep.md":   templateTestBody,
		"Templates/Book Template.md": templateTestBody,
	})

	link := filepath.Join(root, "Templates", "Linked.md")
	if err := os.Symlink(filepath.Join(root, "Alpha.md"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, payload, _ := getTemplates(t, ts.URL, "")
	got := templateNames(payload)
	want := map[string]bool{"Daily.md": true, "Book Template.md": true}
	if len(got) != len(want) {
		t.Fatalf("templates = %v, want exactly %v", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("templates = %v, want exactly %v", got, want)
		}
	}
}

func TestTemplatesList_IsEmptyWhenTheDirectoryDoesNotExist(t *testing.T) {
	ts, _, _ := newTemplatesServer(t, nil)

	status, payload, raw := getTemplates(t, ts.URL, "")
	if status != http.StatusOK {
		t.Fatalf("GET templates status = %d, body %q", status, raw)
	}
	if len(payload.Templates) != 0 {
		t.Fatalf("templates = %v, want empty", templateNames(payload))
	}
}

// TestTemplatesRead_ServesTheExactBytes: the content is never round-tripped
// through a parser, so a template whose frontmatter is not YAML comes back
// unchanged.
func TestTemplatesRead_ServesTheExactBytes(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	status, payload, raw := getTemplates(t, ts.URL, "Daily.md")
	if status != http.StatusOK {
		t.Fatalf("read status = %d, body %q", status, raw)
	}
	if payload.Action != "read" || payload.Name != "Daily.md" {
		t.Fatalf("action/name = %q/%q, want read/Daily.md", payload.Action, payload.Name)
	}
	if len(payload.Templates) != 1 || payload.Templates[0].Content != templateTestBody {
		t.Fatalf("content = %q, want %q", payload.Templates[0].Content, templateTestBody)
	}
	if payload.Revision == "" {
		t.Fatalf("read carries no revision: %q", raw)
	}
}

func TestTemplatesRead_RefusesWhatItCannotAddress(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	cases := []struct {
		name   string
		query  string
		status int
	}{
		{name: "missing", query: "Absent.md", status: http.StatusNotFound},
		{name: "separator", query: "../brainnotes.toml", status: http.StatusForbidden},
		{name: "nested", query: "Nested/Deep.md", status: http.StatusForbidden},
		{name: "hidden", query: ".hidden.md", status: http.StatusForbidden},
		{name: "extension", query: "notes.txt", status: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _, raw := getTemplates(t, ts.URL, tc.query)
			if status != tc.status {
				t.Fatalf("read %q status = %d, want %d (body %q)", tc.query, status, tc.status, raw)
			}
		})
	}
}

func TestTemplates_RefusesAnythingButGET(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	status, raw := postProperty(t, ts.URL, templatesAPIRoutePath, propertyTestToken, map[string]string{})
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("POST %s status = %d, want 405 (body %q)", templatesAPIRoutePath, status, raw)
	}
}

func TestTemplatesPreview_RequiresTheCSRFToken(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	status, raw := postProperty(t, ts.URL, templatesPreviewAPIRoutePath, "",
		templateMutationRequest{Op: templateOpSave, Name: "New.md", Content: "body\n"})
	if status != http.StatusForbidden {
		t.Fatalf("untokened preview status = %d, want 403 (body %q)", status, raw)
	}
}
