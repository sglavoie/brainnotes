package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/template"
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

// TestTemplatesSave_RefusesEveryNameOutsideThePolicy is the path policy, stated
// as the refusals it exists for. Each of these would otherwise write a file this
// vault cannot use, or one outside the directory the preview named.
func TestTemplatesSave_RefusesEveryNameOutsideThePolicy(t *testing.T) {
	ts, _, root := newTemplatesFixtureServer(t)

	cases := []struct {
		name   string
		target string
		status int
	}{
		{name: "empty", target: "   ", status: http.StatusBadRequest},
		{name: "slash", target: "Nested/Deep.md", status: http.StatusForbidden},
		{name: "backslash", target: `Nested\Deep.md`, status: http.StatusForbidden},
		{name: "parent", target: "../Escape.md", status: http.StatusForbidden},
		{name: "dot", target: ".hidden.md", status: http.StatusForbidden},
		{name: "nul", target: "Bad\x00.md", status: http.StatusForbidden},
		{name: "no extension", target: "Daily", status: http.StatusBadRequest},
		{name: "wrong extension", target: "Daily.txt", status: http.StatusBadRequest},
		{name: "extension only", target: ".md", status: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
				templateMutationRequest{Op: templateOpSave, Name: tc.target, Content: "body\n"})
			if status != tc.status {
				t.Fatalf("save %q status = %d, want %d (body %q)", tc.target, status, tc.status, raw)
			}
		})
	}

	// Nothing landed anywhere: the vault root still holds only what it started
	// with, and the templates directory only its one file.
	if _, err := os.Stat(filepath.Join(root, "Escape.md")); !os.IsNotExist(err) {
		t.Fatalf("a refused name escaped the templates directory: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "Templates"))
	if err != nil {
		t.Fatalf("read templates dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("templates directory holds %d entries, want 1", len(entries))
	}
}

// TestTemplatesSave_RefusesASymlinkedDestination: a symlink placed in the
// templates directory must not redirect a write the policy already judged.
func TestTemplatesSave_RefusesASymlinkedDestination(t *testing.T) {
	ts, _, root := newTemplatesFixtureServer(t)

	target := filepath.Join(root, "Alpha.md")
	link := filepath.Join(root, "Templates", "Linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	before := readNoteFile(t, target)

	status, raw := postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpSave, Name: "Linked.md", Content: "redirected\n"})
	if status == http.StatusOK {
		t.Fatalf("a symlinked destination was accepted: %q", raw)
	}
	if got := readNoteFile(t, target); got != before {
		t.Fatalf("the symlink target was written through:\n%s", got)
	}
}

func TestTemplatesSave_RefusesContentItCannotStore(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	status, raw := postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpSave, Name: "Binary.md", Content: "before\x00after"})
	if status != http.StatusBadRequest {
		t.Fatalf("NUL content status = %d, want 400 (body %q)", status, raw)
	}

	status, raw = postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpSave, Name: "Huge.md",
			Content: strings.Repeat("x", maxTemplateBytes+1)})
	if status != http.StatusBadRequest {
		t.Fatalf("oversized content status = %d, want 400 (body %q)", status, raw)
	}
	if !strings.Contains(raw, "maximum") {
		t.Fatalf("the refusal does not name the limit: %q", raw)
	}
}

// TestTemplatesSave_CreatesAFileAndItsDirectory covers the first save into a
// vault that has no templates directory yet.
func TestTemplatesSave_CreatesAFileAndItsDirectory(t *testing.T) {
	ts, _, root := newTemplatesServer(t, nil)

	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpSave, Name: "Daily.md", Content: templateTestBody,
	})
	if !preview.Created {
		t.Fatalf("preview does not report a creation: %#v", preview)
	}
	if len(preview.Diff) == 0 {
		t.Fatalf("preview rendered no diff")
	}
	if _, err := os.Stat(filepath.Join(root, "Templates")); !os.IsNotExist(err) {
		t.Fatalf("preview created the directory: %v", err)
	}

	status, payload, raw := applyTemplate(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d, body %q", status, raw)
	}
	if payload.JournalID == "" {
		t.Fatalf("apply recorded no journal entry: %q", raw)
	}
	if payload.RelPath != "Templates/Daily.md" {
		t.Fatalf("rel_path = %q, want Templates/Daily.md", payload.RelPath)
	}
	if got := readVaultFile(t, root, "Templates/Daily.md"); got != templateTestBody {
		t.Fatalf("template content = %q", got)
	}
	if payload.Revision == "" {
		t.Fatalf("apply carries no revision for the next edit")
	}
}

// TestTemplatesSave_PinsTheRevisionItWasPlannedAgainst is the concurrent-edit
// refusal: an edit that names bytes the file no longer holds writes nothing.
func TestTemplatesSave_PinsTheRevisionItWasPlannedAgainst(t *testing.T) {
	ts, _, root := newTemplatesFixtureServer(t)

	_, read, _ := getTemplates(t, ts.URL, "Daily.md")

	status, raw := postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpSave, Name: "Daily.md", Content: "new\n",
			Revision: "0000000000000000000000000000000000000000000000000000000000000000"})
	if status != http.StatusConflict {
		t.Fatalf("stale-revision save status = %d, want 409 (body %q)", status, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if got := readVaultFile(t, root, "Templates/Daily.md"); got != templateTestBody {
		t.Fatalf("a refused save changed the file:\n%s", got)
	}

	// A create that claims a revision is the same mistake in the other direction.
	status, raw = postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpSave, Name: "Fresh.md", Content: "new\n",
			Revision: read.Revision})
	if status != http.StatusConflict {
		t.Fatalf("create-with-revision status = %d, want 409 (body %q)", status, raw)
	}

	updated := saveTemplate(t, ts.URL, "Daily.md", "# replaced\n", read.Revision)
	if updated.Created {
		t.Fatalf("an overwrite reported a creation: %#v", updated)
	}
	if got := readVaultFile(t, root, "Templates/Daily.md"); got != "# replaced\n" {
		t.Fatalf("overwrite content = %q", got)
	}
}

// TestTemplatesPreview_IssuesNoReceiptForANoOp keeps an apply that would write
// identical bytes from ever existing.
func TestTemplatesPreview_IssuesNoReceiptForANoOp(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	_, read, _ := getTemplates(t, ts.URL, "Daily.md")
	status, payload, raw := postTemplates(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpSave, Name: "Daily.md",
			Content: templateTestBody, Revision: read.Revision})
	if status != http.StatusOK {
		t.Fatalf("no-op preview status = %d, body %q", status, raw)
	}
	if payload.PreviewID != "" {
		t.Fatalf("a no-op issued a receipt: %q", payload.PreviewID)
	}
}

func TestTemplatesDelete_RemovesTheFile(t *testing.T) {
	ts, _, root := newTemplatesFixtureServer(t)

	_, read, _ := getTemplates(t, ts.URL, "Daily.md")
	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpDelete, Name: "Daily.md", Revision: read.Revision,
	})
	if !preview.Removed {
		t.Fatalf("preview does not report a removal: %#v", preview)
	}

	status, payload, raw := applyTemplate(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("delete status = %d, body %q", status, raw)
	}
	if payload.JournalID == "" {
		t.Fatalf("delete recorded no journal entry: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(root, "Templates", "Daily.md")); !os.IsNotExist(err) {
		t.Fatalf("the template is still on disk: %v", err)
	}
}

// TestTemplatesDelete_RefusesWhatNobodyLookedAt: a delete is the one template
// action with no undo a person can reason about before taking it, so it has to
// name the bytes it is removing.
func TestTemplatesDelete_RefusesWhatNobodyLookedAt(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	status, raw := postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpDelete, Name: "Daily.md"})
	if status != http.StatusBadRequest {
		t.Fatalf("revisionless delete status = %d, want 400 (body %q)", status, raw)
	}

	status, raw = postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpDelete, Name: "Absent.md", Revision: "deadbeef"})
	if status != http.StatusNotFound {
		t.Fatalf("delete of a missing template status = %d, want 404 (body %q)", status, raw)
	}
}

func TestTemplatesPreview_RefusesAnUnsupportedOp(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	status, raw := postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: "rename", Name: "Daily.md"})
	if status != http.StatusBadRequest {
		t.Fatalf("bad op status = %d, want 400 (body %q)", status, raw)
	}
}

func TestTemplatesApply_RequiresAReceiptAndRefusesAReplay(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	status, raw := postProperty(t, ts.URL, templatesApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{})
	if status == http.StatusOK {
		t.Fatalf("a receiptless apply was accepted")
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))

	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpSave, Name: "Fresh.md", Content: "body\n",
	})
	if status, _, raw := applyTemplate(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("first apply status = %d, body %q", status, raw)
	}
	status, _, raw = applyTemplate(t, ts.URL, preview.PreviewID)
	if status == http.StatusOK {
		t.Fatalf("the same receipt applied twice")
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

// TestTemplatesApply_RefusesAReceiptFromAnotherFamily proves the preview kinds
// cannot be crossed.
func TestTemplatesApply_RefusesAReceiptFromAnotherFamily(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)

	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpSave, Name: "Fresh.md", Content: "body\n",
	})
	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status == http.StatusOK {
		t.Fatalf("a template receipt was redeemed at the properties endpoint")
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

// TestTemplatesApply_RefusesAPreviewTheFileMovedPast is the stale-preview path:
// an apply can never write what the preview did not show.
func TestTemplatesApply_RefusesAPreviewTheFileMovedPast(t *testing.T) {
	ts, _, root := newTemplatesFixtureServer(t)

	_, read, _ := getTemplates(t, ts.URL, "Daily.md")
	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpSave, Name: "Daily.md", Content: "# mine\n", Revision: read.Revision,
	})

	external := "# somebody else's edit\n"
	writeTestFile(t, root, "Templates/Daily.md", external)

	status, _, raw := applyTemplate(t, ts.URL, preview.PreviewID)
	if status == http.StatusOK {
		t.Fatalf("apply wrote over an external edit")
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if got := readVaultFile(t, root, "Templates/Daily.md"); got != external {
		t.Fatalf("the external edit was not preserved:\n%s", got)
	}
}

// TestTemplatesApply_RefusesAPreviewTheConfigurationMovedPast: templates.dir
// decides where the bytes land, so a configuration replaced inside the preview
// window is a stale plan even though the plan never wrote the configuration.
func TestTemplatesApply_RefusesAPreviewTheConfigurationMovedPast(t *testing.T) {
	ts, _, root := newTemplatesFixtureServer(t)

	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpSave, Name: "Fresh.md", Content: "body\n",
	})
	moved := propertyTestConfigTOML + "\n[templates]\ndir = \"Skeletons\"\n"
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(moved), 0o644); err != nil {
		t.Fatalf("move templates.dir: %v", err)
	}

	status, _, raw := applyTemplate(t, ts.URL, preview.PreviewID)
	if status == http.StatusOK {
		t.Fatalf("apply wrote under a configuration the preview never named: %q", raw)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	for _, rel := range []string{"Templates/Fresh.md", "Skeletons/Fresh.md"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s exists: %v", rel, err)
		}
	}
}

// TestTemplatesApply_NamesTheBusyVaultRatherThanTheStaleDiff is the templates
// half of the settings test of the same name: templatesWriteError rewrites a
// precondition failure and nothing else, so a vault somebody else is mid-write in
// keeps the sentence naming its own remedy.
func TestTemplatesApply_NamesTheBusyVaultRatherThanTheStaleDiff(t *testing.T) {
	ts, _, root := newTemplatesFixtureServer(t)
	startContentHistory(t, root)

	_, read, _ := getTemplates(t, ts.URL, "Daily.md")
	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpSave, Name: "Daily.md", Content: "# mine\n", Revision: read.Revision,
	})
	holdSafetyLock(t, root)

	status, _, raw := applyTemplate(t, ts.URL, preview.PreviewID)
	if status == http.StatusOK {
		t.Fatalf("apply ran while the safety lock was held: %q", raw)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if !strings.Contains(raw, busyVaultRemedyWord) {
		t.Fatalf("the refusal does not say the vault is busy: %q", raw)
	}
	if strings.Contains(raw, "re-check the diff") {
		t.Fatalf("a busy vault was reported as a changed template: %q", raw)
	}
	if got := readVaultFile(t, root, "Templates/Daily.md"); got != templateTestBody {
		t.Fatalf("a refused apply changed the template:\n%s", got)
	}
}

// TestTemplatesApply_IsUndoable is the "undone" half of the session's claim, for
// both directions: a save is taken back out and a delete is put back.
func TestTemplatesApply_IsUndoable(t *testing.T) {
	ts, _, root := newTemplatesFixtureServer(t)

	_, read, _ := getTemplates(t, ts.URL, "Daily.md")
	saved := saveTemplate(t, ts.URL, "Daily.md", "# replaced\n", read.Revision)
	if status, body := postUndo(t, ts.URL, saved.JournalID); status != http.StatusOK {
		t.Fatalf("undo of a save status = %d, body %q", status, body)
	}
	if got := readVaultFile(t, root, "Templates/Daily.md"); got != templateTestBody {
		t.Fatalf("undo did not restore the template:\n%s", got)
	}

	_, read, _ = getTemplates(t, ts.URL, "Daily.md")
	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpDelete, Name: "Daily.md", Revision: read.Revision,
	})
	status, deleted, raw := applyTemplate(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("delete status = %d, body %q", status, raw)
	}
	if status, body := postUndo(t, ts.URL, deleted.JournalID); status != http.StatusOK {
		t.Fatalf("undo of a delete status = %d, body %q", status, body)
	}
	if got := readVaultFile(t, root, "Templates/Daily.md"); got != templateTestBody {
		t.Fatalf("undo did not restore the deleted template:\n%s", got)
	}
}

// TestTemplatesApply_LeavesTheNoteIndexAlone is the invariant the whole path
// policy exists to preserve: a template with frontmatter no YAML parser accepts
// is written into the vault and no note walk ever sees it.
func TestTemplatesApply_LeavesTheNoteIndexAlone(t *testing.T) {
	ts, srv, _ := newTemplatesServer(t, nil)

	before, err := srv.state.refreshSnapshot()
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	saveTemplate(t, ts.URL, "Daily.md", templateTestBody, "")

	after, err := srv.state.refreshSnapshot()
	if err != nil {
		t.Fatalf("snapshot after: %v", err)
	}
	if len(after.notes) != len(before.notes) {
		t.Fatalf("the note count moved from %d to %d", len(before.notes), len(after.notes))
	}
	for _, note := range after.notes {
		if strings.Contains(filepath.ToSlash(note.Path), "/Templates/") {
			t.Fatalf("a template reached the note snapshot: %s", note.Path)
		}
	}
	if len(after.parseErrors) != 0 {
		t.Fatalf("the template produced parse diagnostics: %#v", after.parseErrors)
	}
}

// TestTemplatesApply_ProducesATemplateBnNewCanRender closes the loop on
// "preserve template-based creation": the file this surface wrote is the one
// template.Find resolves and template.Render executes, under an injected clock
// so the assertion is exact.
func TestTemplatesApply_ProducesATemplateBnNewCanRender(t *testing.T) {
	ts, srv, _ := newTemplatesServer(t, nil)

	saveTemplate(t, ts.URL, "Daily.md", templateTestBody, "")

	// Resolved by bare stem, which is how `bn new --template daily` asks for it.
	path, err := template.Find(srv.config(), "daily")
	if err != nil {
		t.Fatalf("template.Find: %v", err)
	}
	clock := template.FixedClock{T: time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)}
	ctx := template.NewContext("Review", "Notes", srv.vault.Root, map[string]string{"topic": "vault"}, clock)
	rendered, err := template.Render(readNoteFile(t, path), ctx)
	if err != nil {
		t.Fatalf("template.Render: %v", err)
	}

	want := "---\ntitle: Review\ncreated: 2026-03-04\ntags:\n    - vault\n---\n\n# Review\n"
	if rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
}

// TestTemplates_RefusesADirectoryOutsideTheTravellingScope: a hidden
// templates.dir would leave every file it holds with no undo history and no way
// to reach a peer, so this surface refuses it instead of writing there.
func TestTemplates_RefusesADirectoryOutsideTheTravellingScope(t *testing.T) {
	source := propertyTestConfigTOML + "\n[templates]\ndir = \".skeletons\"\n"
	ts, _, root := newPropertyServer(t, source, propertyTestNotes())

	status, payload, raw := getTemplates(t, ts.URL, "")
	if status != http.StatusConflict {
		t.Fatalf("list status = %d, want 409 (body %q)", status, raw)
	}
	if payload.Templating.ExcludedFromNotes || payload.Templating.Portable {
		t.Fatalf("an unusable directory reported a status: %#v", payload.Templating)
	}

	status, raw = postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpSave, Name: "Daily.md", Content: "body\n"})
	if status != http.StatusConflict {
		t.Fatalf("save status = %d, want 409 (body %q)", status, raw)
	}
	if _, err := os.Stat(filepath.Join(root, ".skeletons")); !os.IsNotExist(err) {
		t.Fatalf("the refused directory was created: %v", err)
	}
}

// TestTemplates_HonoursAConfiguredExtension: the accepted suffixes follow
// templates.default_extension, because that is what `bn new` looks a template up
// by — and .md stays accepted beside it for the files a vault already holds.
func TestTemplates_HonoursAConfiguredExtension(t *testing.T) {
	source := propertyTestConfigTOML + "\n[templates]\ndefault_extension = \".markdown\"\n"
	ts, _, root := newPropertyServer(t, source, propertyTestNotes())

	saveTemplate(t, ts.URL, "Daily.markdown", "body\n", "")
	saveTemplate(t, ts.URL, "Legacy.md", "body\n", "")
	if got := readVaultFile(t, root, "Templates/Daily.markdown"); got != "body\n" {
		t.Fatalf("configured-extension template = %q", got)
	}

	status, raw := postProperty(t, ts.URL, templatesPreviewAPIRoutePath, propertyTestToken,
		templateMutationRequest{Op: templateOpSave, Name: "Daily.txt", Content: "body\n"})
	if status != http.StatusBadRequest {
		t.Fatalf("unconfigured extension status = %d, want 400 (body %q)", status, raw)
	}
	if !strings.Contains(raw, ".markdown") {
		t.Fatalf("the refusal does not name the configured extension: %q", raw)
	}
}
