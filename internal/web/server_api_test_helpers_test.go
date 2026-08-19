package web

import (
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
