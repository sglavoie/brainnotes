package web

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

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
	OK           bool                     `json:"ok"`
	Entries      []historyEntryPayload    `json:"entries"`
	Total        int                      `json:"total"`
	Offset       int                      `json:"offset"`
	Limit        int                      `json:"limit"`
	HasMore      bool                     `json:"has_more"`
	Origins      []historyOptionPayload   `json:"origins"`
	Classes      []historyOptionPayload   `json:"classes"`
	Warnings     []historyWarningResponse `json:"warnings"`
	WarningCount int                      `json:"warning_count"`
}

type historyOptionPayload struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type historyEntryPayload struct {
	ID          string                     `json:"id"`
	Timestamp   string                     `json:"timestamp"`
	Command     string                     `json:"command"`
	Origin      string                     `json:"origin"`
	OriginLabel string                     `json:"origin_label"`
	FileCount   int                        `json:"file_count"`
	Classes     []historyClassCountPayload `json:"classes"`
	Files       []historyFilePayload       `json:"files"`
}

type historyClassCountPayload struct {
	Class string `json:"class"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type historyFilePayload struct {
	Path     string `json:"path"`
	Before   string `json:"before"`
	After    string `json:"after"`
	Class    string `json:"class"`
	Recorded bool   `json:"recorded"`
	Travels  bool   `json:"travels"`
}

type historyPreviewPayload struct {
	OK            bool                          `json:"ok"`
	Code          string                        `json:"code"`
	Message       string                        `json:"message"`
	PreviewID     string                        `json:"preview_id"`
	ExpiresAt     string                        `json:"expires_at"`
	EntryID       string                        `json:"entry_id"`
	Command       string                        `json:"command"`
	Origin        string                        `json:"origin"`
	OriginLabel   string                        `json:"origin_label"`
	Timestamp     string                        `json:"timestamp"`
	RequiresForce bool                          `json:"requires_force"`
	Changes       []historyPreviewChangePayload `json:"changes"`
	Conflicts     []undoConflictPayload         `json:"conflicts"`
}

type historyPreviewChangePayload struct {
	Path          string `json:"path"`
	CurrentState  string `json:"current_state"`
	RestoredState string `json:"restored_state"`
	Class         string `json:"class"`
	Recorded      bool   `json:"recorded"`
	Travels       bool   `json:"travels"`
	Conflict      bool   `json:"conflict"`
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

func decodeHistoryPreviewResult(t *testing.T, body string) historyPreviewPayload {
	t.Helper()
	var payload historyPreviewPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode history preview result: %v; body=%q", err, body)
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
