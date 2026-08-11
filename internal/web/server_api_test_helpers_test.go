package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func postOpenRequest(t *testing.T, baseURL, key, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/open", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/open: %v", err)
	}
	return resp
}

func postTrashRequest(t *testing.T, baseURL, key, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/trash", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/trash: %v", err)
	}
	return resp
}

func postCreateRequest(t *testing.T, baseURL, key, target, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("target", target)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/create", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/create: %v", err)
	}
	return resp
}

func postRenameRequest(t *testing.T, baseURL, key, title, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("title", title)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/rename", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/rename: %v", err)
	}
	return resp
}

func postMoveRequest(t *testing.T, baseURL, key, folder, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("folder", folder)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/move", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/move: %v", err)
	}
	return resp
}

func postFrontmatterSetRequest(t *testing.T, baseURL, key, property, op, value, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("property", property)
	if op != "" {
		form.Set("op", op)
	}
	form.Set("value", value)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/frontmatter/set", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/frontmatter/set: %v", err)
	}
	return resp
}

func postFrontmatterUnsetRequest(t *testing.T, baseURL, key, property, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("property", property)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/frontmatter/unset", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/frontmatter/unset: %v", err)
	}
	return resp
}

func postBulkRequest(t *testing.T, baseURL, action string, keys []string, fields map[string]string, headerToken, formToken string) *http.Response {
	t.Helper()

	return postBulkFormRequest(t, baseURL+"/api/notes/bulk", action, keys, fields, headerToken, formToken)
}

func bulkFieldsWithPreviewID(fields map[string]string, previewID string) map[string]string {
	out := make(map[string]string, len(fields)+1)
	for name, value := range fields {
		out[name] = value
	}
	out["preview_id"] = previewID
	return out
}

func postBulkPreviewRequest(t *testing.T, baseURL, action string, keys []string, fields map[string]string, headerToken, formToken string) *http.Response {
	t.Helper()

	return postBulkFormRequest(t, baseURL+"/api/notes/bulk/preview", action, keys, fields, headerToken, formToken)
}

func postBulkFormRequest(t *testing.T, rawURL, action string, keys []string, fields map[string]string, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	form.Set("action", action)
	for _, key := range keys {
		form.Add("keys[]", key)
	}
	for name, value := range fields {
		form.Set(name, value)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", rawURL, err)
	}
	return resp
}

func postUndoRequest(t *testing.T, baseURL, entryID, headerToken, formToken string, force, confirmForce bool) *http.Response {
	t.Helper()

	form := url.Values{}
	if entryID != "" {
		form.Set("id", entryID)
	}
	if force {
		form.Set("force", "1")
	}
	if confirmForce {
		form.Set("confirm_force", "1")
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/undo", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/undo: %v", err)
	}
	return resp
}

type managementResultPayload struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	Key       string `json:"key"`
	Path      string `json:"path"`
	RelPath   string `json:"rel_path"`
	JournalID string `json:"journal_id"`
}

type managementErrorPayload struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type bulkManagementPayload struct {
	OK        bool                 `json:"ok"`
	Code      string               `json:"code"`
	Message   string               `json:"message"`
	Changed   int                  `json:"changed"`
	Unchanged int                  `json:"unchanged"`
	Failed    int                  `json:"failed"`
	JournalID string               `json:"journal_id"`
	Results   []bulkResultPayload  `json:"results"`
	Failures  []bulkFailurePayload `json:"failures"`
}

type bulkResultPayload struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Changed bool   `json:"changed"`
}

type bulkPreviewPayload struct {
	OK        bool                       `json:"ok"`
	Code      string                     `json:"code"`
	Action    string                     `json:"action"`
	Message   string                     `json:"message"`
	Changed   int                        `json:"changed"`
	Unchanged int                        `json:"unchanged"`
	Failed    int                        `json:"failed"`
	PreviewID string                     `json:"preview_id"`
	ExpiresAt string                     `json:"expires_at"`
	Results   []bulkPreviewResultPayload `json:"results"`
	Failures  []bulkFailurePayload       `json:"failures"`
}

type bulkPreviewResultPayload struct {
	Key         string                           `json:"key"`
	Path        string                           `json:"path"`
	RelPath     string                           `json:"rel_path"`
	Source      bulkPreviewPathPayload           `json:"source"`
	Destination *bulkPreviewPathPayload          `json:"destination"`
	Changed     bool                             `json:"changed"`
	Move        *bulkPreviewMovePayload          `json:"move"`
	Frontmatter []bulkPreviewFrontmatterPayload  `json:"frontmatter"`
	Affected    []bulkPreviewAffectedPathPayload `json:"affected"`
}

type bulkPreviewPathPayload struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
}

type bulkPreviewMovePayload struct {
	RewriteCount            int `json:"rewrite_count"`
	RewriteFileCount        int `json:"rewrite_file_count"`
	BodyRewriteCount        int `json:"body_rewrite_count"`
	FrontmatterRewriteCount int `json:"frontmatter_rewrite_count"`
}

type bulkPreviewValuePayload struct {
	Present bool `json:"present"`
	Value   any  `json:"value"`
}

type bulkPreviewFrontmatterPayload struct {
	Key     string                  `json:"key"`
	Before  bulkPreviewValuePayload `json:"before"`
	After   bulkPreviewValuePayload `json:"after"`
	Changed bool                    `json:"changed"`
}

type bulkPreviewAffectedPathPayload struct {
	Role    string `json:"role"`
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Exists  bool   `json:"exists"`
}

type bulkFailurePayload struct {
	Key     string `json:"key"`
	RelPath string `json:"rel_path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type historyPayload struct {
	OK      bool                  `json:"ok"`
	Entries []historyEntryPayload `json:"entries"`
}

type historyEntryPayload struct {
	ID        string               `json:"id"`
	Timestamp string               `json:"timestamp"`
	Command   string               `json:"command"`
	FileCount int                  `json:"file_count"`
	Files     []historyFilePayload `json:"files"`
}

type historyFilePayload struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type undoPayload struct {
	OK            bool                  `json:"ok"`
	Code          string                `json:"code"`
	Message       string                `json:"message"`
	EntryID       string                `json:"entry_id"`
	Command       string                `json:"command"`
	RequiresForce bool                  `json:"requires_force"`
	Changes       []undoChangePayload   `json:"changes"`
	Conflicts     []undoConflictPayload `json:"conflicts"`
}

type undoChangePayload struct {
	Path          string `json:"path"`
	CurrentState  string `json:"current_state"`
	RestoredState string `json:"restored_state"`
}

type undoConflictPayload struct {
	Path          string `json:"path"`
	ExpectedState string `json:"expected_state"`
	CurrentState  string `json:"current_state"`
}

func decodeManagementResult(t *testing.T, body string) managementResultPayload {
	t.Helper()
	var payload managementResultPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode management result: %v; body=%q", err, body)
	}
	return payload
}

func decodeManagementError(t *testing.T, body string) managementErrorPayload {
	t.Helper()
	var payload managementErrorPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode management error: %v; body=%q", err, body)
	}
	return payload
}

func decodeBulkManagementResult(t *testing.T, body string) bulkManagementPayload {
	t.Helper()
	var payload bulkManagementPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode bulk management result: %v; body=%q", err, body)
	}
	return payload
}

func decodeBulkPreviewResult(t *testing.T, body string) bulkPreviewPayload {
	t.Helper()
	var payload bulkPreviewPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode bulk preview result: %v; body=%q", err, body)
	}
	return payload
}

func decodeHistoryResult(t *testing.T, body string) historyPayload {
	t.Helper()
	var payload historyPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode history result: %v; body=%q", err, body)
	}
	return payload
}

func decodeUndoResult(t *testing.T, body string) undoPayload {
	t.Helper()
	var payload undoPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode undo result: %v; body=%q", err, body)
	}
	return payload
}

func fetchGraphJSON(t *testing.T, rawURL string) (graphJSON, string, string) {
	t.Helper()

	resp, err := http.Get(rawURL) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, string(body))
	}

	var payload graphJSON
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode graph json: %v", err)
	}

	return payload, string(body), resp.Header.Get("Content-Type")
}

func graphNodeDataByID(payload graphJSON) map[string]graphJSONNodeData {
	nodes := make(map[string]graphJSONNodeData, len(payload.Nodes))
	for _, node := range payload.Nodes {
		nodes[node.Data.ID] = node.Data
	}
	return nodes
}

func graphNodeIDs(payload graphJSON) []string {
	ids := make([]string, 0, len(payload.Nodes))
	for _, node := range payload.Nodes {
		ids = append(ids, node.Data.ID)
	}
	return ids
}

func fetchSearchJSON(t *testing.T, rawURL string) ([]searchResult, string, int) {
	t.Helper()

	resp, err := http.Get(rawURL) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var payload []searchResult
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode search json: %v; body=%q", err, string(body))
	}

	return payload, resp.Header.Get("Content-Type"), resp.StatusCode
}
