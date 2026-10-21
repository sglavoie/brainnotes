package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
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
