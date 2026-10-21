package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/template"
)

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
