package mutationmatrix

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/web"
)

// The portable-configuration surface's routes, spelled out rather than imported:
// they are unexported in internal/web, and a row that derived them from the
// package under test would follow it wherever it went.
const (
	settingsPreviewRoute  = "/api/settings/preview"
	settingsApplyRoute    = "/api/settings/apply"
	templatesPreviewRoute = "/api/templates/preview"
	templatesApplyRoute   = "/api/templates/apply"
)

// matrixTemplateRel is the template the templates row writes. It is a file no
// other case names, under the default templates directory, which the fixture
// does not create — so the row also covers the directory a save has to make.
const matrixTemplateRel = "Templates/Matrix.md"

// settingsMutations are the two preview-gated writes on the portable
// configuration: one line of brainnotes.toml, and one file under the configured
// templates directory.
//
// They are a distinct shape in this table. Every other row is a request that
// mutates; these are a receipt issued by one request and redeemed by a second,
// so what the runner drives is the *apply*, with the preview as its prepare. The
// failure that shape invites is a redemption that plans afresh and commits more
// than the diff a person approved — which is exactly what "exactly one commit of
// exactly these paths" is.
func settingsMutations() []mutation {
	return []mutation{
		{
			name: "web/settings-set",
			prepare: func(t *testing.T, h *harness) any {
				return previewConfigWrite(t, h, settingsPreviewRoute, map[string]any{
					"op":    "set",
					"key":   "capture.heading",
					"value": map[string]any{"kind": "string", "text": "## Later"},
				})
			},
			apply: func(t *testing.T, h *harness, state any) error {
				return applyConfigWrite(t, state.(configWritePreview), settingsApplyRoute)
			},
			touches:   []string{config.FileName},
			journaled: true,
			overHTTP:  true,
			// The no-op cannot live here: a preview that would change nothing issues
			// no receipt at all, so there is no apply to drive.
			// internal/web's TestSettingsPreview_IssuesNoReceiptForANoOp covers it.
		},
		{
			name: "web/templates-save",
			prepare: func(t *testing.T, h *harness) any {
				return previewConfigWrite(t, h, templatesPreviewRoute, map[string]any{
					"op":      "save",
					"name":    "Matrix.md",
					"content": "# {{ .Title }}\n",
				})
			},
			apply: func(t *testing.T, h *harness, state any) error {
				return applyConfigWrite(t, state.(configWritePreview), templatesApplyRoute)
			},
			touches:   []string{matrixTemplateRel},
			journaled: true,
			overHTTP:  true,
			// Same reason as above, and internal/web's
			// TestTemplatesPreview_IssuesNoReceiptForANoOp covers it.
		},
	}
}

// configWritePreview is one issued receipt and the server that holds it. The
// server travels with it because the receipt store is per-server: an apply
// posted to a second server is a receipt nobody issued.
type configWritePreview struct {
	server    *httptest.Server
	previewID string
}

// previewConfigWrite starts a server over the fixture vault and takes one
// receipt from it, failing the test if the preview itself is refused — a preview
// is this row's setup, not the thing under test.
func previewConfigWrite(t *testing.T, h *harness, route string, request map[string]any) configWritePreview {
	t.Helper()

	srv, err := web.NewServer(h.cfg, h.v, web.Options{CSRFToken: matrixCSRFToken})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	status, payload := postConfigWrite(t, ts, route, request)
	if status != http.StatusOK {
		t.Fatalf("POST %s answered %d (%+v)", route, status, payload)
	}
	if payload.PreviewID == "" {
		t.Fatalf("POST %s issued no receipt (%+v)", route, payload)
	}
	return configWritePreview{server: ts, previewID: payload.PreviewID}
}

// applyConfigWrite redeems the receipt and translates the response back into the
// operation error the rest of the matrix reasons about, checking the status
// against the code the shared mapping assigns it — the same assertion postCapture
// makes, on two more routes.
func applyConfigWrite(t *testing.T, preview configWritePreview, route string) error {
	t.Helper()

	status, payload := postConfigWrite(t, preview.server, route, map[string]any{
		"preview_id": preview.previewID,
	})
	if status == http.StatusOK {
		return nil
	}
	opErr := &noteops.Error{Code: noteops.ErrorCode(payload.Code), Message: payload.Message}
	if want := noteops.HTTPStatusForError(opErr); status != want {
		t.Fatalf("POST %s answered %d for code %q, want %d", route, status, payload.Code, want)
	}
	return opErr
}

// configWriteAnswer is the envelope both routes answer with, in both directions.
type configWriteAnswer struct {
	OK        bool   `json:"ok"`
	PreviewID string `json:"preview_id"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

func postConfigWrite(t *testing.T, ts *httptest.Server, route string, request map[string]any) (int, configWriteAnswer) {
	t.Helper()

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal %s request: %v", route, err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+route, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build %s request: %v", route, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BN-CSRF", matrixCSRFToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", route, err)
	}
	defer resp.Body.Close()

	var payload configWriteAnswer
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode %s response: %v", route, err)
	}
	return resp.StatusCode, payload
}
