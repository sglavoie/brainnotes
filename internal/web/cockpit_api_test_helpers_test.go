package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/cockpit"
)

// --- helpers ---------------------------------------------------------------

func getCockpitPayload(t *testing.T, baseURL string) cockpitAPIResponse {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/cockpit") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/cockpit: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cockpit status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want json", ct)
	}
	return decodeCockpitResult(t, body)
}

func getCockpitDailyPayload(t *testing.T, baseURL string) cockpitAPIResponse {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/cockpit?view=daily") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/cockpit?view=daily: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cockpit daily status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	return decodeCockpitResult(t, body)
}

func decodeCockpitResult(t *testing.T, body string) cockpitAPIResponse {
	t.Helper()
	var payload cockpitAPIResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode cockpit result: %v; body=%q", err, body)
	}
	return payload
}

func decodeFixPreview(t *testing.T, body string) fixPreviewResponse {
	t.Helper()
	var payload fixPreviewResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode fix preview: %v; body=%q", err, body)
	}
	return payload
}

func decodeFixApply(t *testing.T, body string) fixApplyResponse {
	t.Helper()
	var payload fixApplyResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode fix apply: %v; body=%q", err, body)
	}
	return payload
}

func findCockpitTask(t *testing.T, queue cockpit.Queue, kind cockpit.RuleKind, detail string) cockpit.Task {
	t.Helper()
	for _, task := range queue.Tasks {
		if task.Kind == kind && task.Detail == detail {
			return task
		}
	}
	t.Fatalf("no cockpit task kind=%q detail=%q; tasks=%#v", kind, detail, queue.Tasks)
	return cockpit.Task{}
}

func postCockpitItem(t *testing.T, baseURL, id, action, snooze, headerToken, formToken string) *http.Response {
	t.Helper()
	form := url.Values{}
	if id != "" {
		form.Set("id", id)
	}
	if action != "" {
		form.Set("action", action)
	}
	if snooze != "" {
		form.Set("snooze", snooze)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	return doFormPost(t, baseURL+"/api/cockpit/item", form, headerToken)
}

func postCockpitItemDaily(t *testing.T, baseURL, id, action, snooze, headerToken, formToken string) *http.Response {
	t.Helper()
	form := url.Values{}
	if id != "" {
		form.Set("id", id)
	}
	if action != "" {
		form.Set("action", action)
	}
	if snooze != "" {
		form.Set("snooze", snooze)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	return doFormPost(t, baseURL+"/api/cockpit/item?view=daily", form, headerToken)
}

func postFixPreview(t *testing.T, baseURL, id, action, headerToken, formToken string) *http.Response {
	t.Helper()
	return doFormPost(t, baseURL+"/api/fixes/preview", fixForm(id, action, "", formToken), headerToken)
}

func postFixApply(t *testing.T, baseURL, id, action, previewID, headerToken, formToken string) *http.Response {
	t.Helper()
	return doFormPost(t, baseURL+"/api/fixes/apply", fixForm(id, action, previewID, formToken), headerToken)
}

func fixForm(id, action, previewID, formToken string) url.Values {
	form := url.Values{}
	if id != "" {
		form.Set("id", id)
	}
	if action != "" {
		form.Set("action", action)
	}
	if previewID != "" {
		form.Set("preview_id", previewID)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	return form
}

func doFormPost(t *testing.T, rawURL string, form url.Values, headerToken string) *http.Response {
	t.Helper()
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

// --- daily-review agent ----------------------------------------------------

func agentForm(formToken string) url.Values {
	form := url.Values{}
	if formToken != "" {
		form.Set("token", formToken)
	}
	return form
}

func decodeAgentResult(t *testing.T, body string) dailyReviewAgentResponse {
	t.Helper()
	var payload dailyReviewAgentResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode agent result: %v; body=%q", err, body)
	}
	return payload
}
