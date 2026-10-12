package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

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
