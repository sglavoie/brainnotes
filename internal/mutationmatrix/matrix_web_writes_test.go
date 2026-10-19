package mutationmatrix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/web"
)

// The note, property, synthesis, pin and cockpit routes, spelled out for the
// reason settingsPreviewRoute gives.
const (
	propertyValueRoute         = "/api/properties/value"
	propertyPreviewRoute       = "/api/properties/preview"
	propertyApplyRoute         = "/api/properties/apply"
	propertySchemaPreviewRoute = "/api/properties/schema/preview"
	propertySchemaApplyRoute   = "/api/properties/schema/apply"
	viewsPreviewRoute          = "/api/views/preview"
	viewsApplyRoute            = "/api/views/apply"
	synthesisContextRoute      = "/api/synthesis/context"
	synthesisPreviewRoute      = "/api/synthesis/preview"
	synthesisApplyRoute        = "/api/synthesis/apply"
	noteMoveRoute              = "/api/notes/move"
	noteCreateRoute            = "/api/notes/create"
	bulkPreviewRoute           = "/api/notes/bulk/preview"
	bulkApplyRoute             = "/api/notes/bulk"
	pinSetRoute                = "/api/pins/set"
	cockpitQueueRoute          = "/api/cockpit"
	cockpitItemRoute           = "/api/cockpit/item"
	fixesPreviewRoute          = "/api/fixes/preview"
	fixesApplyRoute            = "/api/fixes/apply"
)

// matrixSynthesisRel is where the synthesis row's note lands: its title, in the
// vault root, because the row names no folder and the fixture configures none.
const matrixSynthesisRel = "Matrix Synthesis.md"

// webWriteMutations are the remaining web writers, each driven through the route
// a browser posts to. They share the server-per-row shape the settings rows use:
// the server is built in prepare, before the location is refused, because what a
// row measures is the write and not whether a server can start on refused
// storage. A preview-gated route takes its receipt in prepare too, so the runner
// drives exactly the request that writes.
func webWriteMutations() []mutation {
	all := webPropertyMutations()
	all = append(all, webNoteMutations()...)
	return append(all, webCockpitMutations()...)
}

// webPropertyMutations are the commitPropertyPlan writers: the one unpreviewed
// single-note value edit, a previewed multi-note change, and the two previewed
// brainnotes.toml edits that reach the same commit through the config branch.
func webPropertyMutations() []mutation {
	return []mutation{
		{
			name:    "web/property-value",
			prepare: prepareMatrixServer,
			apply: func(t *testing.T, h *harness, state any) error {
				return postPropertyValue(t, h, state.(*httptest.Server), "done")
			},
			touches:   []string{"Alpha.md"},
			journaled: true,
			overHTTP:  true,
			noop: func(t *testing.T, h *harness, state any) error {
				// Alpha.md already says draft: the same route with nothing to write.
				return postPropertyValue(t, h, state.(*httptest.Server), "draft")
			},
		},
		previewedJSONRow("web/property-apply", propertyPreviewRoute, propertyApplyRoute, map[string]any{
			"action":   "set",
			"property": "status",
			"notes":    []string{"Alpha.md", "Notes/Gamma.md"},
			"value":    map[string]any{"kind": "string", "text": "done"},
		}, "Alpha.md", "Notes/Gamma.md"),
		previewedJSONRow("web/property-schema", propertySchemaPreviewRoute, propertySchemaApplyRoute, map[string]any{
			"op":         "upsert",
			"property":   "priority",
			"definition": map[string]any{"type": "string"},
		}, config.FileName),
		previewedJSONRow("web/views-apply", viewsPreviewRoute, viewsApplyRoute, map[string]any{
			"op":   "upsert",
			"name": "Matrix",
			"view": map[string]any{"query": "status:draft"},
		}, config.FileName),
	}
}

// previewedJSONRow is one JSON preview-then-apply route. A preview that would
// change nothing issues no receipt, so these rows carry no no-op; each route's
// own tests cover that refusal.
func previewedJSONRow(name, previewRoute, applyRoute string, request map[string]any, touches ...string) mutation {
	return mutation{
		name: name,
		prepare: func(t *testing.T, h *harness) any {
			return previewConfigWrite(t, h, previewRoute, request)
		},
		apply: func(t *testing.T, h *harness, state any) error {
			return applyConfigWrite(t, state.(configWritePreview), applyRoute)
		},
		touches:   touches,
		journaled: true,
		overHTTP:  true,
	}
}

// webNoteMutations are the note-level writers outside the property surface:
// synthesis, a single and a bulk move, a pin, and a stub for a broken link.
func webNoteMutations() []mutation {
	return []mutation{
		{
			name:    "web/synthesis-apply",
			prepare: prepareSynthesisPreview,
			apply: func(t *testing.T, h *harness, state any) error {
				preview := state.(configWritePreview)
				return postMatrixForm(t, preview.server, synthesisApplyRoute, url.Values{"preview_id": {preview.previewID}})
			},
			touches:   []string{matrixSynthesisRel},
			journaled: true,
			overHTTP:  true,
		},
		{
			name:    "web/note-move",
			prepare: prepareMatrixServer,
			apply: func(t *testing.T, h *harness, state any) error {
				return postMatrixForm(t, state.(*httptest.Server), noteMoveRoute, url.Values{
					"key": {"alpha"}, "folder": {"Archive"},
				})
			},
			touches:   []string{"Alpha.md", "Archive/Alpha.md"},
			journaled: true,
			overHTTP:  true,
		},
		{
			name: "web/bulk-move",
			prepare: func(t *testing.T, h *harness) any {
				ts := newMatrixServer(t, h)
				status, answer := postMatrixFormAnswer(t, ts, bulkPreviewRoute, bulkMoveForm(""))
				if status != http.StatusOK || answer.PreviewID == "" {
					t.Fatalf("POST %s answered %d (%+v)", bulkPreviewRoute, status, answer)
				}
				return configWritePreview{server: ts, previewID: answer.PreviewID}
			},
			apply: func(t *testing.T, h *harness, state any) error {
				preview := state.(configWritePreview)
				return postMatrixForm(t, preview.server, bulkApplyRoute, bulkMoveForm(preview.previewID))
			},
			touches:   []string{"Alpha.md", "Archive/Alpha.md", "Notes/Gamma.md", "Archive/Gamma.md"},
			journaled: true,
			overHTTP:  true,
		},
		{
			name:    "web/pin-set",
			prepare: prepareMatrixServer,
			apply: func(t *testing.T, h *harness, state any) error {
				return postMatrixForm(t, state.(*httptest.Server), pinSetRoute, url.Values{"key": {"alpha"}})
			},
			touches:   []string{"Alpha.md"},
			journaled: true,
			overHTTP:  true,
			// The no-op — pinning a note already pinned — needs this row's own
			// write to have happened first, so it cannot be a separate request here.
		},
		{
			name:    "web/broken-link-stub",
			prepare: prepareMatrixServer,
			apply: func(t *testing.T, h *harness, state any) error {
				return postMatrixForm(t, state.(*httptest.Server), noteCreateRoute, url.Values{
					"key": {"alpha"}, "target": {"Missing Note"},
				})
			},
			touches:   []string{"Missing Note.md"},
			journaled: true,
			overHTTP:  true,
		},
	}
}

// webCockpitMutations are the two cockpit routes that write notes: a triage
// action that edits frontmatter, and a previewed fix that creates a stub.
func webCockpitMutations() []mutation {
	return []mutation{
		{
			name: "web/cockpit-item",
			prepare: func(t *testing.T, h *harness) any {
				ts := newMatrixServer(t, h)
				task := findMatrixTask(t, ts, cockpit.RuleDeadEndNote, "Notes/Gamma.md")
				return cockpitItemState{server: ts, taskID: task.ID}
			},
			apply: func(t *testing.T, h *harness, state any) error {
				item := state.(cockpitItemState)
				return postMatrixForm(t, item.server, cockpitItemRoute, url.Values{
					"id": {item.taskID}, "action": {string(cockpit.ActionExemptDeadEnd)},
				})
			},
			touches:   []string{"Notes/Gamma.md"},
			journaled: true,
			overHTTP:  true,
		},
		{
			name: "web/fixes-apply",
			prepare: func(t *testing.T, h *harness) any {
				ts := newMatrixServer(t, h)
				task := findMatrixTask(t, ts, cockpit.RuleBrokenTarget, "")
				status, answer := postMatrixFormAnswer(t, ts, fixesPreviewRoute, url.Values{
					"id": {task.ID}, "action": {string(cockpit.ActionCreateStub)},
				})
				if status != http.StatusOK || answer.PreviewID == "" {
					t.Fatalf("POST %s answered %d (%+v)", fixesPreviewRoute, status, answer)
				}
				return fixPreviewState{server: ts, taskID: task.ID, previewID: answer.PreviewID}
			},
			apply: func(t *testing.T, h *harness, state any) error {
				fix := state.(fixPreviewState)
				return postMatrixForm(t, fix.server, fixesApplyRoute, url.Values{
					"id": {fix.taskID}, "action": {string(cockpit.ActionCreateStub)}, "preview_id": {fix.previewID},
				})
			},
			touches:   []string{"Missing Note.md"},
			journaled: true,
			overHTTP:  true,
		},
	}
}

type cockpitItemState struct {
	server *httptest.Server
	taskID string
}

type fixPreviewState struct {
	server    *httptest.Server
	taskID    string
	previewID string
}

// matrixAnswer is the envelope every route here answers with, in either
// direction, plus the identities a preview or a queue hands back.
type matrixAnswer struct {
	OK        bool           `json:"ok"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	PreviewID string         `json:"preview_id"`
	ContextID string         `json:"context_id"`
	Tasks     []cockpit.Task `json:"tasks"`
}

func newMatrixServer(t *testing.T, h *harness) *httptest.Server {
	t.Helper()
	srv, err := web.NewServer(h.cfg, h.v, web.Options{CSRFToken: matrixCSRFToken})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func prepareMatrixServer(t *testing.T, h *harness) any {
	return newMatrixServer(t, h)
}

// prepareSynthesisPreview assembles a context packet anchored on Alpha and
// previews one draft from it, so the row's apply is the receipt alone.
func prepareSynthesisPreview(t *testing.T, h *harness) any {
	t.Helper()
	ts := newMatrixServer(t, h)
	status, context := postMatrixFormAnswer(t, ts, synthesisContextRoute, url.Values{"source": {"alpha"}})
	if status != http.StatusOK || context.ContextID == "" {
		t.Fatalf("POST %s answered %d (%+v)", synthesisContextRoute, status, context)
	}
	status, preview := postMatrixFormAnswer(t, ts, synthesisPreviewRoute, url.Values{
		"context_id": {context.ContextID},
		"title":      {"Matrix Synthesis"},
		"draft":      {"A synthesized thought."},
	})
	if status != http.StatusOK || preview.PreviewID == "" {
		t.Fatalf("POST %s answered %d (%+v)", synthesisPreviewRoute, status, preview)
	}
	return configWritePreview{server: ts, previewID: preview.PreviewID}
}

func bulkMoveForm(previewID string) url.Values {
	form := url.Values{"action": {"move"}, "keys[]": {"alpha", "notes/gamma"}, "folder": {"Archive"}}
	if previewID != "" {
		form.Set("preview_id", previewID)
	}
	return form
}

// postPropertyValue sends the workbench's one unpreviewed write, echoing the
// hash of Alpha.md as it is on disk now — what the browser rendered.
func postPropertyValue(t *testing.T, h *harness, ts *httptest.Server, value string) error {
	t.Helper()
	sum := sha256.Sum256([]byte(h.readFile("Alpha.md")))
	status, answer := postConfigWrite(t, ts, propertyValueRoute, map[string]any{
		"note":          "Alpha.md",
		"property":      "status",
		"op":            "replace",
		"expected_hash": hex.EncodeToString(sum[:]),
		"value":         map[string]any{"kind": "string", "text": value},
	})
	return answerError(t, propertyValueRoute, status, answer.Code, answer.Message)
}

// findMatrixTask reads the cockpit queue and returns the first task of kind
// whose detail matches, or the first of that kind when detail is empty.
func findMatrixTask(t *testing.T, ts *httptest.Server, kind cockpit.RuleKind, detail string) cockpit.Task {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + cockpitQueueRoute)
	if err != nil {
		t.Fatalf("GET %s: %v", cockpitQueueRoute, err)
	}
	defer resp.Body.Close()
	var answer matrixAnswer
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatalf("decode %s: %v", cockpitQueueRoute, err)
	}
	for _, task := range answer.Tasks {
		if task.Kind == kind && (detail == "" || task.Detail == detail) {
			return task
		}
	}
	t.Fatalf("the cockpit queue has no %s task for %q: %+v", kind, detail, answer.Tasks)
	return cockpit.Task{}
}

// postMatrixForm posts a form and translates the answer into the operation
// error the runner reasons about, with the status held to the code.
func postMatrixForm(t *testing.T, ts *httptest.Server, route string, form url.Values) error {
	t.Helper()
	status, answer := postMatrixFormAnswer(t, ts, route, form)
	return answerError(t, route, status, answer.Code, answer.Message)
}

func postMatrixFormAnswer(t *testing.T, ts *httptest.Server, route string, form url.Values) (int, matrixAnswer) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+route, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build %s request: %v", route, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-BN-CSRF", matrixCSRFToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", route, err)
	}
	defer resp.Body.Close()

	var answer matrixAnswer
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatalf("decode %s response: %v", route, err)
	}
	return resp.StatusCode, answer
}

// answerError is the translation postCapture and applyConfigWrite make: a 200
// is success, and anything else is the coded error, provided the status is the
// one the shared mapping assigns that code.
func answerError(t *testing.T, route string, status int, code, message string) error {
	t.Helper()
	if status == http.StatusOK {
		return nil
	}
	opErr := &noteops.Error{Code: noteops.ErrorCode(code), Message: message}
	if want := noteops.HTTPStatusForError(opErr); status != want {
		t.Fatalf("POST %s answered %d for code %q, want %d (%s)", route, status, code, want, message)
	}
	return opErr
}
