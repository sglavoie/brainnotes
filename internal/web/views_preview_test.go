package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

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
