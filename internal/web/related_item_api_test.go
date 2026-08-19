package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

const relatedItemTestToken = "related-item-csrf-token"

func TestServer_RelatedItemAPI_Validation(t *testing.T) {
	files := map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\n---\nsource [[Candidate]]\n"),
		"Candidate.md": []byte("---\ntitle: Candidate\n---\ncandidate\n"),
	}
	tests := []struct {
		name   string
		values url.Values
		token  string
		status int
	}{
		{name: "csrf", values: relatedItemValues("source", "candidate", "useful", ""), status: http.StatusForbidden},
		{name: "missing source", values: relatedItemValues("", "candidate", "useful", ""), token: relatedItemTestToken, status: http.StatusBadRequest},
		{name: "missing candidate", values: relatedItemValues("source", "", "useful", ""), token: relatedItemTestToken, status: http.StatusBadRequest},
		{name: "same note", values: relatedItemValues("source", "source", "useful", ""), token: relatedItemTestToken, status: http.StatusBadRequest},
		{name: "stale source", values: relatedItemValues("stale", "candidate", "useful", ""), token: relatedItemTestToken, status: http.StatusNotFound},
		{name: "stale candidate", values: relatedItemValues("source", "stale", "useful", ""), token: relatedItemTestToken, status: http.StatusNotFound},
		{name: "filesystem path candidate", values: relatedItemValues("source", "/tmp/outside.md", "useful", ""), token: relatedItemTestToken, status: http.StatusNotFound},
		{name: "unsupported action", values: relatedItemValues("source", "candidate", "archive", ""), token: relatedItemTestToken, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ts, _ := newHTTPServerForFilesWithOptions(t, files, nil, Options{CSRFToken: relatedItemTestToken})
			resp := postRelatedItemRequest(t, ts.URL, test.values, test.token)
			body := readResponseBody(t, resp)
			if resp.StatusCode != test.status {
				t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, test.status, body)
			}
		})
	}

	t.Run("method", func(t *testing.T) {
		ts, _ := newHTTPServerForFilesWithOptions(t, files, nil, Options{CSRFToken: relatedItemTestToken})
		resp, err := http.Get(ts.URL + relatedItemAPIRoutePath) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatal(err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodPost {
			t.Fatalf("status/allow = %d/%q; body=%s", resp.StatusCode, resp.Header.Get("Allow"), body)
		}
	})

	t.Run("malformed form", func(t *testing.T) {
		ts, _ := newHTTPServerForFilesWithOptions(t, files, nil, Options{CSRFToken: relatedItemTestToken})
		req, err := http.NewRequest(http.MethodPost, ts.URL+relatedItemAPIRoutePath, strings.NewReader("%"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set(csrfHeaderName, relatedItemTestToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "invalid request form") {
			t.Fatalf("status/body = %d/%s", resp.StatusCode, body)
		}
	})

	t.Run("form csrf token", func(t *testing.T) {
		ts, _ := newHTTPServerForFilesWithOptions(t, files, nil, Options{CSRFToken: relatedItemTestToken})
		values := relatedItemValues("source", "candidate", "useful", "")
		values.Set("token", relatedItemTestToken)
		resp := postRelatedItemRequest(t, ts.URL, values, "")
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
		}
	})
}

func TestServer_RelatedItemAPI_FeedbackDatesViewAndNoMarkdownJournalMutation(t *testing.T) {
	now := time.Date(2026, time.March, 7, 23, 30, 0, 0, time.FixedZone("related-local", -6*60*60))
	files := map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\ncreated: 2020-01-01\n---\ncommon topic [[Bridge]]\n"),
		"Bridge.md":    []byte("---\ntitle: Bridge\ncreated: 2020-01-01\n---\nbridge [[Candidate]]\n"),
		"Candidate.md": []byte("---\ntitle: Candidate\ncreated: 2020-01-01\n---\ncommon topic candidate\n"),
	}
	ts, root := newHTTPServerForFilesWithOptions(t, files, nil, Options{
		CSRFToken: relatedItemTestToken,
		BriefNow:  func() time.Time { return now },
	})
	assertRelatedModesContain(t, ts.URL, "candidate", true)

	for _, action := range []string{"useful", "dismissed", "useful"} {
		payload := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", action, ""))
		if payload.State != recall.FeedbackState(action) {
			t.Fatalf("%s state = %q", action, payload.State)
		}
		assertMarkdownAndHistoryUnchanged(t, ts.URL, root, files)
	}

	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "dismissed", ""))
	assertRelatedModesContain(t, ts.URL, "candidate", false)
	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "clear", ""))
	assertRelatedModesContain(t, ts.URL, "candidate", true)

	validDates := []string{"2026-03-08", "2026-03-14", "2026-04-03"}
	for index, date := range validDates {
		if index == 0 {
			postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "useful", ""))
		}
		payload := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "later", date))
		if payload.State != recall.FeedbackLater || payload.Until != date {
			t.Fatalf("later %s payload = %#v", date, payload)
		}
		assertRelatedModesContain(t, ts.URL, "candidate", false)
		postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "clear", ""))
		assertMarkdownAndHistoryUnchanged(t, ts.URL, root, files)
	}
	for _, date := range []string{"", "2026-03-07", "2026-03-06", "2026-02-29", "03/08/2026"} {
		resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "later", date), relatedItemTestToken)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("later %q status = %d, want 400; body=%s", date, resp.StatusCode, body)
		}
	}

	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "useful", ""))
	view := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "view", ""))
	if view.Recorded == nil || !*view.Recorded {
		t.Fatalf("view response = %#v", view)
	}
	signal, err := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now })).Signal("Source.md", "Candidate.md")
	if err != nil || !signal.Useful || signal.LastOpenedAt.IsZero() {
		t.Fatalf("view signal = %#v, err=%v", signal, err)
	}
	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "clear", ""))
	signal, err = recall.NewStateStore(root, recall.WithClock(func() time.Time { return now })).Signal("Source.md", "Candidate.md")
	if err != nil || signal.Useful || signal.Dismissed || signal.LastOpenedAt.IsZero() {
		t.Fatalf("cleared signal = %#v, err=%v", signal, err)
	}
	assertMarkdownAndHistoryUnchanged(t, ts.URL, root, files)
}

func TestServer_RelatedItemAPI_WritePreservesRecallForNotesOutsideSnapshot(t *testing.T) {
	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	files := map[string][]byte{
		"Source.md":                  []byte("---\ntitle: Source\n---\nsource [[Candidate]]\n"),
		"Candidate.md":               []byte("---\ntitle: Candidate\n---\ncandidate\n"),
		"Archive/HiddenSource.md":    []byte("---\ntitle: Hidden Source\n---\nhidden source\n"),
		"Archive/HiddenCandidate.md": []byte("---\ntitle: Hidden Candidate\n---\nhidden candidate\n"),
	}
	ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
		cfg.Ignore = append(cfg.Ignore, "Archive")
	}, Options{
		CSRFToken: relatedItemTestToken,
		BriefNow:  func() time.Time { return now },
	})

	seed := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now }))
	if err := seed.SetFeedback("Archive/HiddenSource.md", "Archive/HiddenCandidate.md", recall.FeedbackUseful, time.Time{}); err != nil {
		t.Fatalf("seed hidden feedback: %v", err)
	}
	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "useful", ""))

	signal, err := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now })).Signal(
		"Archive/HiddenSource.md",
		"Archive/HiddenCandidate.md",
	)
	if err != nil || !signal.Useful {
		t.Fatalf("hidden signal after visible web write = %#v, err=%v; want preserved useful feedback", signal, err)
	}
}

func TestServer_RelatedItemAPI_ConcurrentWritesPreserveEveryPair(t *testing.T) {
	candidates := []string{
		"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot",
		"Golf", "Hotel", "India", "Juliet", "Kilo", "Lima",
	}
	files := map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nsource\n"),
	}
	for _, candidate := range candidates {
		files[candidate+".md"] = []byte("---\ntitle: " + candidate + "\n---\ncandidate\n")
	}

	arrived := make(chan struct{}, len(candidates))
	release := make(chan struct{})
	now := time.Date(2026, time.August, 19, 11, 0, 0, 0, time.UTC)
	ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --goto"
	}, Options{
		CSRFToken: relatedItemTestToken,
		BriefNow:  func() time.Time { return now },
		StartEditor: func(string, []string) error {
			arrived <- struct{}{}
			<-release
			return nil
		},
	})

	type requestResult struct {
		candidate string
		err       error
	}
	results := make(chan requestResult, len(candidates))
	for _, candidate := range candidates {
		candidate := candidate
		go func() {
			values := relatedItemValues("source", strings.ToLower(candidate), "open_editor", "")
			req, err := http.NewRequest(http.MethodPost, ts.URL+relatedItemAPIRoutePath, strings.NewReader(values.Encode()))
			if err == nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set(csrfHeaderName, relatedItemTestToken)
				var response *http.Response
				response, err = http.DefaultClient.Do(req)
				if err == nil {
					body, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if readErr != nil {
						err = readErr
					} else if response.StatusCode != http.StatusOK {
						err = fmt.Errorf("status %d: %s", response.StatusCode, body)
					}
				}
			}
			results <- requestResult{candidate: candidate, err: err}
		}()
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for range candidates {
		select {
		case <-arrived:
		case result := <-results:
			close(release)
			t.Fatalf("%s request completed before the concurrency barrier: %v", result.candidate, result.err)
		case <-timer.C:
			close(release)
			t.Fatal("timed out waiting for concurrent related-item requests")
		}
	}
	close(release)

	var firstErr error
	for range candidates {
		result := <-results
		if result.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s request: %w", result.candidate, result.err)
		}
	}
	if firstErr != nil {
		t.Fatal(firstErr)
	}

	store := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now }))
	for _, candidate := range candidates {
		signal, err := store.Signal("Source.md", candidate+".md")
		if err != nil || signal.LastOpenedAt.IsZero() {
			t.Errorf("final recall.json pair Source.md -> %s.md = %#v, err=%v", candidate, signal, err)
		}
	}
}

func TestServer_RelatedItemAPI_EditorOrderingFailuresAndPartialSuccess(t *testing.T) {
	files := map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\n---\nsource [[Candidate]]\n"),
		"Candidate.md": []byte("---\ntitle: Candidate\n---\ncandidate\n"),
	}
	now := time.Date(2026, time.August, 19, 10, 0, 0, 0, time.UTC)

	t.Run("success preserves feedback", func(t *testing.T) {
		started := 0
		ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = "echo --goto"
		}, Options{
			CSRFToken: relatedItemTestToken,
			BriefNow:  func() time.Time { return now },
			StartEditor: func(string, []string) error {
				started++
				return nil
			},
		})
		postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "useful", ""))
		payload := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""))
		if started != 1 || !payload.Opened || payload.Recorded == nil || !*payload.Recorded || payload.Warning != "" {
			t.Fatalf("started/payload = %d/%#v", started, payload)
		}
		signal, err := recall.NewStateStore(root).Signal("Source.md", "Candidate.md")
		if err != nil || !signal.Useful || signal.LastOpenedAt.IsZero() {
			t.Fatalf("signal = %#v, err=%v", signal, err)
		}
	})

	t.Run("start failure does not record", func(t *testing.T) {
		ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = "echo --goto"
		}, Options{
			CSRFToken: relatedItemTestToken,
			BriefNow:  func() time.Time { return now },
			StartEditor: func(string, []string) error {
				return errors.New("boom")
			},
		})
		resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""), relatedItemTestToken)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(body, "editor start error") {
			t.Fatalf("status/body = %d/%s", resp.StatusCode, body)
		}
		signal, err := recall.NewStateStore(root).Signal("Source.md", "Candidate.md")
		if err != nil || !signal.LastOpenedAt.IsZero() {
			t.Fatalf("signal = %#v, err=%v", signal, err)
		}
	})

	t.Run("editor unavailable does not record", func(t *testing.T) {
		t.Setenv("EDITOR", "")
		ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = ""
		}, Options{CSRFToken: relatedItemTestToken, BriefNow: func() time.Time { return now }})
		resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""), relatedItemTestToken)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusPreconditionFailed || !strings.Contains(body, "editor not configured") {
			t.Fatalf("status/body = %d/%s", resp.StatusCode, body)
		}
		signal, err := recall.NewStateStore(root).Signal("Source.md", "Candidate.md")
		if err != nil || !signal.LastOpenedAt.IsZero() {
			t.Fatalf("signal = %#v, err=%v", signal, err)
		}
	})

	t.Run("state validation precedes editor", func(t *testing.T) {
		started := 0
		ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = "echo --goto"
		}, Options{
			CSRFToken: relatedItemTestToken,
			BriefNow:  func() time.Time { return now },
			StartEditor: func(string, []string) error {
				started++
				return nil
			},
		})
		statePath := filepath.Join(root, ".bnotes", "recall.json")
		if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, []byte("{broken\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""), relatedItemTestToken)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusConflict || started != 0 || !strings.Contains(body, "recall state is unreadable") {
			t.Fatalf("status/started/body = %d/%d/%s", resp.StatusCode, started, body)
		}
	})

	t.Run("editor success and recall write failure", func(t *testing.T) {
		var root string
		external := filepath.Join(t.TempDir(), "external-recall.json")
		if err := os.WriteFile(external, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ts, gotRoot := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = "echo --goto"
		}, Options{
			CSRFToken: relatedItemTestToken,
			BriefNow:  func() time.Time { return now },
			StartEditor: func(string, []string) error {
				statePath := filepath.Join(root, ".bnotes", "recall.json")
				if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
					return err
				}
				return os.Symlink(external, statePath)
			},
		})
		root = gotRoot
		payload := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""))
		if !payload.Opened || payload.Recorded == nil || *payload.Recorded || payload.Warning == "" {
			t.Fatalf("partial payload = %#v", payload)
		}
		if info, err := os.Lstat(filepath.Join(root, ".bnotes", "recall.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("state sidecar = %v, err=%v", info, err)
		}
	})
}

func TestServer_RelatedItemAPI_EditorRejectsCandidateReplacedByEscapingSymlink(t *testing.T) {
	files := map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\n---\nsource [[Candidate]]\n"),
		"Candidate.md": []byte("---\ntitle: Candidate\n---\ncandidate\n"),
	}
	started := 0
	ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --goto"
	}, Options{
		CSRFToken: relatedItemTestToken,
		StartEditor: func(string, []string) error {
			started++
			return nil
		},
	})
	external := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(external, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(root, "Candidate.md")
	if err := os.Remove(candidatePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, candidatePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""), relatedItemTestToken)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound || started != 0 || !strings.Contains(body, "not found or stale") {
		t.Fatalf("status/started/body = %d/%d/%s", resp.StatusCode, started, body)
	}
}

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
