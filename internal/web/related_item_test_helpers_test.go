package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

const relatedItemTestToken = "related-item-csrf-token"

func relatedItemValues(source, candidate, action, until string) url.Values {
	values := url.Values{"source": {source}, "candidate": {candidate}, "action": {action}}
	if until != "" {
		values.Set("until", until)
	}
	return values
}

func postRelatedItemRequest(t *testing.T, baseURL string, values url.Values, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+relatedItemAPIRoutePath, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set(csrfHeaderName, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST related item: %v", err)
	}
	return resp
}

func postRelatedItemOK(t *testing.T, baseURL string, values url.Values) relatedItemResponse {
	t.Helper()
	resp := postRelatedItemRequest(t, baseURL, values, relatedItemTestToken)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var payload relatedItemResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, body)
	}
	if !payload.OK || payload.Action != values.Get("action") || payload.Source.Key != values.Get("source") || payload.Candidate.Key != values.Get("candidate") {
		t.Fatalf("response = %#v", payload)
	}
	return payload
}

func assertRelatedModesContain(t *testing.T, baseURL, candidateKey string, want bool) {
	t.Helper()
	for _, mode := range []recall.Mode{recall.ModeBalanced, recall.ModeRediscover, recall.ModeConnect} {
		_, payload := getRelatedResponse(t, baseURL, url.Values{
			"source": {"source"},
			"mode":   {string(mode)},
			"limit":  {"20"},
		})
		got := false
		for _, result := range payload.Results {
			got = got || result.Candidate.Key == candidateKey
		}
		if got != want {
			t.Fatalf("mode %s contains %s = %v, want %v; results=%#v", mode, candidateKey, got, want, payload.Results)
		}
	}
}

func assertMarkdownAndHistoryUnchanged(t *testing.T, baseURL, root string, want map[string][]byte) {
	t.Helper()
	for relPath, bytes := range want {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		if err != nil || string(got) != string(bytes) {
			t.Fatalf("%s changed: got=%q err=%v want=%q", relPath, got, err, bytes)
		}
	}
	resp, err := http.Get(baseURL + "/api/history") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatal(err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d; body=%s", resp.StatusCode, body)
	}
	payload := decodeHistoryResult(t, body)
	if !payload.OK || len(payload.Entries) != 0 {
		t.Fatalf("history = %#v, want no entries", payload)
	}
}
