package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

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
