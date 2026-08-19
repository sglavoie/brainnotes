package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
)

func TestPropertyValue_RejectsMissingCSRF(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, "", propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	requireErrorCode(t, raw, string(noteops.CodeForbidden))
	if strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: done") {
		t.Fatal("a refused request must not write the note")
	}
}

func TestPropertyValue_RejectsNonPost(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	resp, err := http.Get(ts.URL + propertyValueAPIRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", propertyValueAPIRoutePath, err)
	}
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestPropertyValue_ReplacesScalarImmediately(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	status, payload := postPropertyMutation(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if payload.Affected != 1 || payload.Selected != 1 {
		t.Fatalf("counts = selected %d affected %d, want 1 and 1", payload.Selected, payload.Affected)
	}
	if payload.JournalID == "" {
		t.Fatal("an immediate edit must be journalled")
	}
	if payload.PreviewID != "" {
		t.Fatalf("an immediate edit must not issue a receipt: %q", payload.PreviewID)
	}
	if len(payload.Previews) != 1 || len(payload.Previews[0].Changes) != 1 {
		t.Fatalf("previews = %#v", payload.Previews)
	}
	if got := readVaultFile(t, root, "Alpha.md"); !strings.Contains(got, "status: done") {
		t.Fatalf("Alpha.md = %q", got)
	}
	if got := readVaultFile(t, root, "Beta.md"); !strings.Contains(got, "status: draft") {
		t.Fatalf("an unselected note changed: %q", got)
	}
}

func TestPropertyValue_RejectsStaleNoteHash(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	stale := noteHash(t, root, "Alpha.md")
	writeTestFile(t, root, "Alpha.md", "---\nstatus: draft\ntitle: Alpha\n---\nEdited elsewhere.\n")

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: stale,
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if !strings.Contains(readVaultFile(t, root, "Alpha.md"), "Edited elsewhere.") {
		t.Fatal("the external edit must survive")
	}
}

func TestPropertyValue_RevalidatesAllowedValues(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "shipped"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestPropertyValue_RevalidatesKind(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "reviewed",
		Op:           propertyValueOpReplace,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindDate, Text: "2026-1-2"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestPropertyValue_AddsListItem(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	status, payload := postPropertyMutation(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Beta.md",
		Property:     "tags",
		Op:           propertyValueOpListAdd,
		ExpectedHash: noteHash(t, root, "Beta.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "added"},
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if payload.Affected != 1 {
		t.Fatalf("affected = %d, want 1", payload.Affected)
	}
	got := readVaultFile(t, root, "Beta.md")
	if !strings.Contains(got, "- beta") || !strings.Contains(got, "- added") {
		t.Fatalf("Beta.md = %q", got)
	}
}

func TestPropertyValue_ListAddRefusesScalarProperty(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpListAdd,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestPropertyValue_RequiresExpectedHash(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		Note:     "Alpha.md",
		Property: "status",
		Op:       propertyValueOpReplace,
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestPropertiesPreview_RejectsMissingCSRF(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyPreviewAPIRoutePath, "", propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	requireErrorCode(t, raw, string(noteops.CodeForbidden))
}

func TestPropertiesApply_RejectsMissingCSRF(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, "", propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	requireErrorCode(t, raw, string(noteops.CodeForbidden))
}

func TestPropertiesPreviewApply_SetAcrossNotes(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	req := propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "status",
		Notes:    []string{"Beta.md", "Alpha.md"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	}
	preview := previewProperty(t, ts.URL, req)
	if preview.Selected != 2 || preview.Affected != 2 {
		t.Fatalf("counts = selected %d affected %d, want 2 and 2", preview.Selected, preview.Affected)
	}
	if preview.JournalID != "" {
		t.Fatal("a preview must not journal anything")
	}
	if preview.ExpiresAt == "" {
		t.Fatal("a receipt must advertise its expiry")
	}
	if _, err := time.Parse(time.RFC3339, preview.ExpiresAt); err != nil {
		t.Fatalf("expires_at %q: %v", preview.ExpiresAt, err)
	}
	if strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: done") {
		t.Fatal("a preview must not write")
	}

	status, applied := applyProperty(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if applied.JournalID == "" {
		t.Fatal("an apply must be journalled")
	}
	if applied.Affected != 2 {
		t.Fatalf("affected = %d, want 2", applied.Affected)
	}
	for _, rel := range []string{"Alpha.md", "Beta.md"} {
		if got := readVaultFile(t, root, rel); !strings.Contains(got, "status: done") {
			t.Fatalf("%s = %q", rel, got)
		}
	}
}

func TestPropertiesApply_RejectsReusedReceipt(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	if status, _ := applyProperty(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("first apply status = %d", status)
	}

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("replay status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

func TestPropertiesApply_RejectsExpiredReceipt(t *testing.T) {
	ts, srv, root := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	srv.propertyPreviews.now = func() time.Time { return time.Now().Add(2 * propertyPreviewTTL) }

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if !strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: draft") {
		t.Fatal("an expired receipt must not write")
	}
}

func TestPropertiesApply_RejectsApplyWithoutReceipt(t *testing.T) {
	ts, _, _ := newPropertyFixtureServer(t)

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken, propertyApplyRequest{})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

func TestPropertiesApply_RejectsStaleNote(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "status",
		Notes:    []string{"Alpha.md", "Beta.md"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	writeTestFile(t, root, "Beta.md", "---\nstatus: draft\ntitle: Beta\n---\nEdited elsewhere.\n")

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: done") {
		t.Fatal("a stale apply must write nothing at all")
	}
}

func TestPropertiesApply_RejectsStaleConfigGeneration(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionSet,
		Property: "status",
		Notes:    []string{"Alpha.md"},
		Value:    &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	rewriteConfig(t, root, propertyTestConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: done") {
		t.Fatal("a stale generation must write nothing")
	}
}

func TestPropertiesPreview_RefusedWhileConfigInvalid(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	rewriteConfig(t, root, "[frontmatter.properties.status\ntype = ")

	status, raw := postProperty(t, ts.URL, propertyPreviewAPIRoutePath, propertyTestToken, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
}

func TestPropertiesApply_RefusedWhileConfigInvalid(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)

	preview := previewProperty(t, ts.URL, propertyMutationRequest{
		Action:   propertyActionUnset,
		Property: "status",
		Notes:    []string{"Alpha.md"},
	})
	rewriteConfig(t, root, "[frontmatter.properties.status\ntype = ")

	status, raw := postProperty(t, ts.URL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
	if !strings.Contains(readVaultFile(t, root, "Alpha.md"), "status: draft") {
		t.Fatal("an invalid configuration must block the write")
	}
}
