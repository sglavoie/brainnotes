package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/properties"
)

func TestPropertySchemaPreview_RejectsMissingCSRF(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertySchemaPreviewAPIRoutePath, "", propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	requireErrorCode(t, raw, string(noteops.CodeForbidden))
}

func TestPropertySchemaApply_RejectsMissingCSRF(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	status, raw := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, "",
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	requireErrorCode(t, raw, string(noteops.CodeForbidden))
}

func TestPropertySchemaPreview_RejectsNonPost(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	resp, err := http.Get(ts.URL + propertySchemaPreviewAPIRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", propertySchemaPreviewAPIRoutePath, err)
	}
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestPropertySchemaPreviewApply_UpsertWritesConfigurationOnly(t *testing.T) {
	ts, srv, root := newPropertyFixtureServer(t)

	notesBefore := map[string]string{
		"Alpha.md": readVaultFile(t, root, "Alpha.md"),
		"Beta.md":  readVaultFile(t, root, "Beta.md"),
	}

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:       propertySchemaOpUpsert,
		Property: "topic",
		Definition: &propertySchemaDefinition{
			Type:    config.KindString,
			Allowed: []string{"ops", "research"},
		},
	})
	if preview.Action != "schema_upsert" {
		t.Fatalf("action = %q", preview.Action)
	}
	if len(preview.ConfigDiff) == 0 {
		t.Fatal("a definition change must preview a configuration diff")
	}
	if preview.JournalID != "" {
		t.Fatal("a preview must not journal anything")
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.topic]") {
		t.Fatal("a preview must not write the configuration")
	}

	generationBefore := srv.configState.Status().Generation
	status, applied := applyPropertySchema(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if applied.JournalID == "" {
		t.Fatal("a definition change must be journalled")
	}
	if applied.Generation <= generationBefore {
		t.Fatalf("generation = %d, want more than %d", applied.Generation, generationBefore)
	}
	if !strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.topic]") {
		t.Fatal("the definition was not written")
	}
	for rel, want := range notesBefore {
		if got := readVaultFile(t, root, rel); got != want {
			t.Fatalf("%s changed during a definition-only apply:\n got %q\nwant %q", rel, got, want)
		}
	}

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 || history.Entries[0].Command != propertySchemaUpsertCommand {
		t.Fatalf("history = %#v, want %q", history.Entries, propertySchemaUpsertCommand)
	}
	if history.Entries[0].FileCount != 1 {
		t.Fatalf("file count = %d, want 1", history.Entries[0].FileCount)
	}
}

func TestPropertySchemaApply_UndoRestoresTheConfiguration(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	before := readVaultFile(t, root, config.FileName)
	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	status, applied := applyPropertySchema(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}

	undoStatus, raw := postUndo(t, ts.URL, applied.JournalID)
	if undoStatus != http.StatusOK {
		t.Fatalf("undo status = %d, body %q", undoStatus, raw)
	}
	if got := readVaultFile(t, root, config.FileName); got != before {
		t.Fatalf("undo did not restore the configuration:\n got %q\nwant %q", got, before)
	}
}

func TestPropertySchemaPreviewApply_DeleteRemovesDefinitionOnly(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	notesBefore := readVaultFile(t, root, "Alpha.md")
	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:       propertySchemaOpDelete,
		Property: "reviewed",
	})
	if preview.Action != "schema_delete" {
		t.Fatalf("action = %q", preview.Action)
	}
	if preview.Affected != 2 {
		t.Fatalf("affected = %d, want 2", preview.Affected)
	}

	status, _ := applyPropertySchema(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.reviewed]") {
		t.Fatal("the definition survived the delete")
	}
	if got := readVaultFile(t, root, "Alpha.md"); got != notesBefore {
		t.Fatalf("a delete must not touch notes:\n got %q\nwant %q", got, notesBefore)
	}
}

func TestPropertySchemaPreview_ProjectsViolationsForTheNewDefinition(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:       propertySchemaOpUpsert,
		Property: "status",
		Definition: &propertySchemaDefinition{
			Type:    config.KindString,
			Allowed: []string{"done"},
		},
	})
	if preview.Projected.Before.Value != 0 {
		t.Fatalf("before value violations = %d, want 0", preview.Projected.Before.Value)
	}
	if preview.Projected.After.Value != 2 {
		t.Fatalf("after value violations = %d, want 2", preview.Projected.After.Value)
	}
	if preview.Projected.After.Notes != 2 {
		t.Fatalf("after notes = %d, want 2", preview.Projected.After.Notes)
	}
	if preview.Affected != 2 {
		t.Fatalf("affected = %d, want 2", preview.Affected)
	}
}

func TestPropertySchemaApply_RejectsNoteReceipt(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	status, raw := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if !strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: draft") {
		t.Fatal("a mismatched receipt must write nothing")
	}
}

func TestPropertiesApply_RejectsSchemaReceipt(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.topic]") {
		t.Fatal("a mismatched receipt must write nothing")
	}
}

func TestPropertySchemaApply_RejectsReusedReceipt(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	if status, _ := applyPropertySchema(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("first apply status = %d", status)
	}
	status, raw := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("replay status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

func TestPropertySchemaApply_RejectsStaleConfiguration(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	rewriteConfig(t, root, propertyTestConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")

	status, raw := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.topic]") {
		t.Fatal("a stale apply must write nothing")
	}
}

// The receipt is a statement about the whole configuration, not about the file
// the patch happens to land in: an overlay that appeared is a second source the
// reviewed diff was never measured against.
func TestPropertySchemaApply_RejectsAnOverlayThatAppeared(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	writeLocalOverlay(t, root, "[open]\neditor = \"hx\"\n")

	status, raw := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %q)", status, http.StatusConflict, raw)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.topic]") {
		t.Fatal("a stale apply must write nothing")
	}
}

func TestPropertySchemaApply_RefusedWhileConfigurationInvalid(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	rewriteConfig(t, root, "[frontmatter.properties.status\ntype = ")

	status, raw := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
}

func TestPropertySchemaPreview_RejectsInvalidDefinition(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertySchemaPreviewAPIRoutePath, propertyTestToken, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindList},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestPropertySchemaPreview_RejectsInvalidPropertyName(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertySchemaPreviewAPIRoutePath, propertyTestToken, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "not a key",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidKey))
}

func TestPropertySchemaPreview_RejectsMissingDefinition(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertySchemaPreviewAPIRoutePath, propertyTestToken, propertySchemaRequest{
		Op:       propertySchemaOpUpsert,
		Property: "topic",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestPropertySchemaPreview_DeleteUnknownProperty(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertySchemaPreviewAPIRoutePath, propertyTestToken, propertySchemaRequest{
		Op:       propertySchemaOpDelete,
		Property: "nowhere",
	})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
	}
	requireErrorCode(t, raw, string(noteops.CodeNotFound))
}

func TestPropertySchemaPreview_RejectsUnknownOp(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertySchemaPreviewAPIRoutePath, propertyTestToken, propertySchemaRequest{
		Op:       propertySchemaOp("rename"),
		Property: "status",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

// TestPropertySchemaApply_LeavesRepairToItsOwnPreview pins the split the
// workbench promises: tightening a definition never rewrites the notes it now
// flags.
func TestPropertySchemaApply_LeavesRepairToItsOwnPreview(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	notesBefore := readVaultFile(t, root, "Alpha.md")
	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:       propertySchemaOpUpsert,
		Property: "status",
		Definition: &propertySchemaDefinition{
			Type:    config.KindString,
			Allowed: []string{"done"},
		},
	})
	if status, _ := applyPropertySchema(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if got := readVaultFile(t, root, "Alpha.md"); got != notesBefore {
		t.Fatalf("the note was rewritten by a definition change:\n got %q\nwant %q", got, notesBefore)
	}

	// The follow-up repair is a separate action with its own preview.
	repair := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionRepair,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	if len(repair.Blockers) != 1 || repair.Blockers[0].Kind != propertyBlockerRepairSkip {
		t.Fatalf("blockers = %#v, want one advisory skip", repair.Blockers)
	}
}

func TestPropertySchemaUpsert_RevalidatesLaterWritesAgainstTheNewDefinition(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:       propertySchemaOpUpsert,
		Property: "status",
		Definition: &propertySchemaDefinition{
			Type:    config.KindString,
			Allowed: []string{"draft"},
		},
	})
	if status, _ := applyPropertySchema(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}
