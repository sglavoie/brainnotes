package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// viewsTestConfigTOML adds three saved views to the shared property fixture: one
// that compiles, one whose comment must survive a rename but not an overwrite,
// and one whose query names a field that does not exist, so every test can
// observe the disabled-not-dropped contract.
const viewsTestConfigTOML = propertyTestConfigTOML + `
[[views]]
name = "Active"
query = "status:draft"
description = "Work in progress"

[[views]]
# Why this view exists.
name = "Commented"
query = "tag:alpha"

[[views]]
name = "Rotted"
query = "statuz:wip"
`

func newViewsFixtureServer(t *testing.T) (*httptest.Server, *Server, string) {
	t.Helper()
	return newPropertyServer(t, viewsTestConfigTOML, propertyTestNotes())
}

func getViews(t *testing.T, baseURL string) viewsResponse {
	t.Helper()

	resp, err := http.Get(baseURL + viewsAPIRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", viewsAPIRoutePath, err)
	}
	raw := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("views status = %d, body %q", resp.StatusCode, raw)
	}
	var payload viewsResponse
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode views %q: %v", raw, err)
	}
	return payload
}

func postViews(t *testing.T, baseURL, route, token string, body any) (int, viewsResponse) {
	t.Helper()

	status, raw := postProperty(t, baseURL, route, token, body)
	var payload viewsResponse
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode %s body %q: %v", route, raw, err)
		}
	}
	return status, payload
}

func previewView(t *testing.T, baseURL string, req viewMutationRequest) viewsResponse {
	t.Helper()

	status, payload := postViews(t, baseURL, viewsPreviewAPIRoutePath, propertyTestToken, req)
	if status != http.StatusOK {
		t.Fatalf("view preview status = %d", status)
	}
	if payload.PreviewID == "" {
		t.Fatalf("view preview issued no receipt: %#v", payload)
	}
	return payload
}

func applyView(t *testing.T, baseURL, previewID string) (int, viewsResponse) {
	t.Helper()
	return postViews(t, baseURL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: previewID})
}

func findViewSummary(t *testing.T, views []viewSummary, name string) viewSummary {
	t.Helper()
	for _, view := range views {
		if strings.EqualFold(view.Name, name) {
			return view
		}
	}
	t.Fatalf("view %q is missing from %#v", name, views)
	return viewSummary{}
}

func TestViews_ListsEveryViewInFileOrder(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	payload := getViews(t, ts.URL)
	if !payload.OK {
		t.Fatal("ok = false")
	}
	names := make([]string, 0, len(payload.Views))
	for _, view := range payload.Views {
		names = append(names, view.Name)
	}
	if got := strings.Join(names, ","); got != "Active,Commented,Rotted" {
		t.Fatalf("views = %q, want file order", got)
	}

	active := findViewSummary(t, payload.Views, "Active")
	if !active.Enabled || active.Detail != "" {
		t.Fatalf("Active = %#v, want enabled", active)
	}
	if active.Normalized != "status:draft" {
		t.Fatalf("normalized = %q", active.Normalized)
	}
	if active.Description != "Work in progress" {
		t.Fatalf("description = %q", active.Description)
	}
}

// TestViews_ReportsARottedViewDisabledRatherThanDropped pins the contract
// queryexpr.ResolveViews exists to make free: one stale view must never vanish
// from the list and must never fail the request.
func TestViews_ReportsARottedViewDisabledRatherThanDropped(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	rotted := findViewSummary(t, getViews(t, ts.URL).Views, "Rotted")
	if rotted.Enabled {
		t.Fatal("a view naming an unknown field must be disabled")
	}
	if !strings.Contains(rotted.Detail, "statuz") {
		t.Fatalf("detail = %q, want the offending text", rotted.Detail)
	}
	if rotted.Normalized != "" {
		t.Fatalf("normalized = %q, want empty for a disabled view", rotted.Normalized)
	}
}

func TestViews_RejectsNonGet(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	status, _ := postProperty(t, ts.URL, viewsAPIRoutePath, propertyTestToken, viewMutationRequest{})
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", status, http.StatusMethodNotAllowed)
	}
}

func TestViewsPreview_RejectsMissingCSRF(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	status, raw := postProperty(t, ts.URL, viewsPreviewAPIRoutePath, "", viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	requireErrorCode(t, raw, string(noteops.CodeForbidden))
}

func TestViewsApply_RejectsMissingCSRF(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, "",
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	requireErrorCode(t, raw, string(noteops.CodeForbidden))
}

func TestViewsPreview_RejectsNonPost(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	resp, err := http.Get(ts.URL + viewsPreviewAPIRoutePath) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", viewsPreviewAPIRoutePath, err)
	}
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

// TestViewsPreviewApply_CreateWritesConfigurationOnly is the create case the
// client auto-chains: nothing exists to destroy, so Created is true and the diff
// is a receipt rather than a question.
func TestViewsPreviewApply_CreateWritesConfigurationOnly(t *testing.T) {
	ts, srv, root := newViewsFixtureServer(t)

	notesBefore := map[string]string{
		"Alpha.md": readVaultFile(t, root, "Alpha.md"),
		"Beta.md":  readVaultFile(t, root, "Beta.md"),
	}

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done and not folder:Archive", Sort: "-modified", Limit: 20},
	})
	if !preview.Created {
		t.Fatal("a view that does not exist yet must preview as created")
	}
	if len(preview.ConfigDiff) == 0 {
		t.Fatal("a view change must preview a configuration diff")
	}
	if preview.JournalID != "" {
		t.Fatal("a preview must not journal anything")
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("a preview must not write the configuration")
	}
	fresh := findViewSummary(t, preview.Views, "Fresh")
	if !fresh.Enabled || fresh.Limit != 20 || fresh.Sort != "-modified" {
		t.Fatalf("previewed view = %#v", fresh)
	}

	generationBefore := srv.configState.Status().Generation
	status, applied := applyView(t, ts.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if applied.JournalID == "" {
		t.Fatal("a view change must be journalled")
	}
	if applied.Generation <= generationBefore {
		t.Fatalf("generation = %d, want more than %d", applied.Generation, generationBefore)
	}
	if !strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("the view was not written")
	}
	for rel, want := range notesBefore {
		if got := readVaultFile(t, root, rel); got != want {
			t.Fatalf("%s changed during a view apply:\n got %q\nwant %q", rel, got, want)
		}
	}
	findViewSummary(t, getViews(t, ts.URL).Views, "Fresh")

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 || history.Entries[0].Command != viewsUpsertCommand {
		t.Fatalf("history = %#v, want %q", history.Entries, viewsUpsertCommand)
	}
	if history.Entries[0].FileCount != 1 {
		t.Fatalf("file count = %d, want 1", history.Entries[0].FileCount)
	}
}

// TestViewsPreviewApply_OverwriteDropsCommentsInsideTheBlock is the concrete
// reason overwrite must confirm the diff while create may auto-chain: an upsert
// replaces the whole block, so a comment inside it is destroyed.
func TestViewsPreviewApply_OverwriteDropsCommentsInsideTheBlock(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Commented",
		View: &viewDefinition{Query: "tag:shared"},
	})
	if preview.Created {
		t.Fatal("an existing view must not preview as created")
	}
	if status, _ := applyView(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}

	source := readVaultFile(t, root, config.FileName)
	if strings.Contains(source, "# Why this view exists.") {
		t.Fatal("an overwrite is expected to drop the comment inside the block")
	}
	if !strings.Contains(source, `query = "tag:shared"`) {
		t.Fatal("the overwrite did not land")
	}
}

// TestViewsPreviewApply_RenamePreservesCommentsInsideTheBlock is the other half
// of that split: a rename rewrites one line, so the block survives intact.
func TestViewsPreviewApply_RenamePreservesCommentsInsideTheBlock(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:      viewOpRename,
		Name:    "Commented",
		NewName: "Renamed",
	})
	if status, applied := applyView(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	} else if applied.NewName != "Renamed" {
		t.Fatalf("new_name = %q", applied.NewName)
	}

	source := readVaultFile(t, root, config.FileName)
	if !strings.Contains(source, "# Why this view exists.") {
		t.Fatal("a rename must preserve the comment inside the block")
	}
	if strings.Contains(source, `name = "Commented"`) {
		t.Fatal("the old name survived the rename")
	}
	findViewSummary(t, getViews(t, ts.URL).Views, "Renamed")

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 || history.Entries[0].Command != viewsRenameCommand {
		t.Fatalf("history = %#v, want %q", history.Entries, viewsRenameCommand)
	}
}

func TestViewsPreviewApply_DeleteRemovesTheBlock(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{Op: viewOpDelete, Name: "Rotted"})
	for _, view := range preview.Views {
		if strings.EqualFold(view.Name, "Rotted") {
			t.Fatal("the preview still lists the deleted view")
		}
	}
	if status, _ := applyView(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("apply status = %d", status)
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Rotted"`) {
		t.Fatal("the view survived the delete")
	}

	history := propertyHistory(t, ts.URL)
	if len(history.Entries) == 0 || history.Entries[0].Command != viewsDeleteCommand {
		t.Fatalf("history = %#v, want %q", history.Entries, viewsDeleteCommand)
	}
}

func TestViewsApply_UndoRestoresTheConfiguration(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	before := readVaultFile(t, root, config.FileName)
	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	status, applied := applyView(t, ts.URL, preview.PreviewID)
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

// TestViewsApply_RejectsStaleConfiguration is the three-writer failure mode:
// brainnotes.toml moved under the receipt, so the loser is a 409 that names the
// remedy rather than a generic error.
func TestViewsApply_RejectsStaleConfiguration(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	rewriteConfig(t, root, viewsTestConfigTOML+"\n[frontmatter.properties.area]\ntype = \"wikilink\"\n")

	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if !strings.Contains(raw, "re-check the diff") {
		t.Fatalf("message = %q, want the re-check remedy", raw)
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("a stale apply must write nothing")
	}
}

// TestViewsApply_RejectsAConcurrentViewWrite is the same race between two view
// writers rather than between a view and a property writer.
func TestViewsApply_RejectsAConcurrentViewWrite(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	first := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	second := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Other",
		View: &viewDefinition{Query: "tag:alpha"},
	})
	if status, _ := applyView(t, ts.URL, first.PreviewID); status != http.StatusOK {
		t.Fatalf("first apply status = %d", status)
	}

	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: second.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("second apply status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewStale))
	if !strings.Contains(raw, "re-check the diff") {
		t.Fatalf("message = %q, want the re-check remedy", raw)
	}
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Other"`) {
		t.Fatal("the losing writer must write nothing")
	}
}

func TestViewsApply_RejectsReusedReceipt(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	if status, _ := applyView(t, ts.URL, preview.PreviewID); status != http.StatusOK {
		t.Fatalf("first apply status = %d", status)
	}
	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("replay status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
}

func TestViewsApply_RejectsSchemaReceipt(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewPropertySchema(t, ts.URL, propertySchemaRequest{
		Op:         propertySchemaOpUpsert,
		Property:   "topic",
		Definition: &propertySchemaDefinition{Type: config.KindString},
	})
	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if strings.Contains(readVaultFile(t, root, config.FileName), "[frontmatter.properties.topic]") {
		t.Fatal("a mismatched receipt must write nothing")
	}
}

func TestPropertySchemaApply_RejectsViewReceipt(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	status, raw := postProperty(t, ts.URL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodePreviewRequired))
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Fresh"`) {
		t.Fatal("a mismatched receipt must write nothing")
	}
}

func TestViewsApply_RefusedWhileConfigurationInvalid(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	preview := previewView(t, ts.URL, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Fresh",
		View: &viewDefinition{Query: "status:done"},
	})
	rewriteConfig(t, root, "[frontmatter.properties.status\ntype = ")

	status, raw := postProperty(t, ts.URL, viewsApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	requireErrorCode(t, raw, string(noteops.CodeConflict))
}

// TestViewsPreview_RejectsAQueryThatWouldLandDisabled keeps the API from saving
// a view the user would immediately have to repair: config validates a saved
// view's shape only, so this is the one place the expression can be checked
// while it is still being typed.
func TestViewsPreview_RejectsAQueryThatWouldLandDisabled(t *testing.T) {
	ts, _, root := newViewsFixtureServer(t)

	status, raw := postProperty(t, ts.URL, viewsPreviewAPIRoutePath, propertyTestToken, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Broken",
		View: &viewDefinition{Query: "statuz:wip"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
	if strings.Contains(readVaultFile(t, root, config.FileName), `name = "Broken"`) {
		t.Fatal("a rejected preview must write nothing")
	}
}

func TestViewsPreview_RejectsARelevanceSortWithoutAnAnchor(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	status, raw := postProperty(t, ts.URL, viewsPreviewAPIRoutePath, propertyTestToken, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Unanchored",
		View: &viewDefinition{Query: "status:draft", Sort: "relevance"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestViewsPreview_RejectsAnInvalidName(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	status, raw := postProperty(t, ts.URL, viewsPreviewAPIRoutePath, propertyTestToken, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "not/a/name",
		View: &viewDefinition{Query: "status:draft"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestViewsPreview_RejectsRenameOntoATakenName(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	status, raw := postProperty(t, ts.URL, viewsPreviewAPIRoutePath, propertyTestToken, viewMutationRequest{
		Op:      viewOpRename,
		Name:    "Active",
		NewName: "Rotted",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
}

func TestViewsPreview_UnknownViewIsNotFound(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	for _, req := range []viewMutationRequest{
		{Op: viewOpDelete, Name: "Nowhere"},
		{Op: viewOpRename, Name: "Nowhere", NewName: "Somewhere"},
	} {
		status, raw := postProperty(t, ts.URL, viewsPreviewAPIRoutePath, propertyTestToken, req)
		if status != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", req.Op, status, http.StatusNotFound)
		}
		requireErrorCode(t, raw, string(noteops.CodeNotFound))
	}
}

func TestViewsPreview_RejectsUnknownOpAndMissingFields(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	cases := []viewMutationRequest{
		{Op: viewMutationOp("archive"), Name: "Active"},
		{Op: viewOpUpsert, Name: "Fresh"},
		{Op: viewOpUpsert, Name: "Fresh", View: &viewDefinition{Query: "   "}},
		{Op: viewOpRename, Name: "Active"},
		{Op: viewOpDelete},
	}
	for _, req := range cases {
		status, raw := postProperty(t, ts.URL, viewsPreviewAPIRoutePath, propertyTestToken, req)
		if status != http.StatusBadRequest {
			t.Fatalf("%#v status = %d, want %d", req, status, http.StatusBadRequest)
		}
		requireErrorCode(t, raw, string(noteops.CodeInvalidRequest))
	}
}

// TestViewsPreview_NoOpUpsertIssuesNoReceipt mirrors the schema endpoint: a
// change that patches to identical bytes has nothing to apply.
func TestViewsPreview_NoOpUpsertIssuesNoReceipt(t *testing.T) {
	ts, _, _ := newViewsFixtureServer(t)

	status, payload := postViews(t, ts.URL, viewsPreviewAPIRoutePath, propertyTestToken, viewMutationRequest{
		Op:   viewOpUpsert,
		Name: "Active",
		View: &viewDefinition{Query: "status:draft", Description: "Work in progress"},
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if payload.PreviewID != "" {
		t.Fatalf("a no-op preview issued a receipt: %#v", payload)
	}
	if len(payload.ConfigDiff) != 0 {
		t.Fatalf("config diff = %#v, want empty", payload.ConfigDiff)
	}
}
