package web

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func TestServer_RelatedEndToEnd_FeedbackExpiryAndOpenHistory(t *testing.T) {
	now := time.Date(2026, time.March, 7, 23, 30, 0, 0, time.FixedZone("related-local", -6*60*60))
	files := map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\ncreated: 2020-01-01\n---\nshared retrieval topic [[Bridge]]\n"),
		"Bridge.md":    []byte("---\ntitle: Bridge\ncreated: 2020-01-01\n---\nshared retrieval bridge [[Candidate]]\n"),
		"Candidate.md": []byte("---\ntitle: Candidate\ncreated: 2020-01-01\n---\nshared retrieval topic candidate\n"),
	}
	editorCalls := 0
	ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --goto"
	}, Options{
		CSRFToken: relatedItemTestToken,
		BriefNow:  func() time.Time { return now },
		StartEditor: func(string, []string) error {
			editorCalls++
			return nil
		},
	})

	_, initial := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "mode": {"balanced"}, "limit": {"5"}})
	if len(initial.Results) == 0 || initial.Results[0].Candidate.Key == "" {
		t.Fatalf("initial Balanced response = %#v", initial)
	}
	relatedResultForKey(t, initial, "candidate")

	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "useful", ""))
	_, useful := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "mode": {"balanced"}, "limit": {"20"}})
	if !relatedResultForKey(t, useful, "candidate").Useful {
		t.Fatalf("Useful candidate was not visibly marked: %#v", useful.Results)
	}
	assertRelatedFeedback(t, useful, "candidate", recall.FeedbackUseful, "")

	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "dismissed", ""))
	for _, mode := range []recall.Mode{recall.ModeBalanced, recall.ModeRediscover, recall.ModeConnect} {
		_, payload := getRelatedResponse(t, ts.URL, url.Values{
			"source": {"source"},
			"mode":   {string(mode)},
			"limit":  {"20"},
		})
		if relatedResponseContains(payload, "candidate") {
			t.Fatalf("Dismissed candidate remained in %s results: %#v", mode, payload.Results)
		}
		assertRelatedFeedback(t, payload, "candidate", recall.FeedbackDismissed, "")
	}

	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "clear", ""))
	assertRelatedModesContain(t, ts.URL, "candidate", true)

	deadline := "2026-03-08"
	later := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "later", deadline))
	if later.Until != deadline {
		t.Fatalf("Later deadline = %q, want %q", later.Until, deadline)
	}
	_, snoozed := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "mode": {"balanced"}, "limit": {"20"}})
	if relatedResponseContains(snoozed, "candidate") {
		t.Fatalf("snoozed candidate remained eligible: %#v", snoozed.Results)
	}
	assertRelatedFeedback(t, snoozed, "candidate", recall.FeedbackLater, deadline)

	statePath := filepath.Join(root, ".bnotes", "recall.json")
	beforeExpiryRead, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state before expiry: %v", err)
	}
	now = time.Date(2026, time.March, 8, 1, 0, 0, 0, now.Location())
	_, expired := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "mode": {"balanced"}, "limit": {"20"}})
	relatedResultForKey(t, expired, "candidate")
	if len(expired.Feedback) != 0 {
		t.Fatalf("expired feedback remained active: %#v", expired.Feedback)
	}
	afterExpiryRead, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after expiry: %v", err)
	}
	if !bytes.Equal(beforeExpiryRead, afterExpiryRead) {
		t.Fatalf("expiry read rewrote recall state\nbefore=%s\nafter=%s", beforeExpiryRead, afterExpiryRead)
	}

	postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "useful", ""))
	opened := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""))
	if editorCalls != 1 || !opened.Opened || opened.Recorded == nil || !*opened.Recorded {
		t.Fatalf("editor calls/open response = %d/%#v", editorCalls, opened)
	}
	signal, err := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now })).Signal("Source.md", "Candidate.md")
	if err != nil || !signal.Useful || signal.LastOpenedAt.IsZero() {
		t.Fatalf("open history did not preserve feedback: signal=%#v err=%v", signal, err)
	}
	assertMarkdownAndHistoryUnchanged(t, ts.URL, root, files)
}

func assertRelatedFeedback(
	t *testing.T,
	response relatedResponse,
	candidateKey string,
	wantState recall.FeedbackState,
	wantUntil string,
) {
	t.Helper()
	for _, feedback := range response.Feedback {
		if feedback.Candidate.Key == candidateKey {
			if feedback.State != wantState || feedback.Until != wantUntil {
				t.Fatalf("feedback for %q = %#v, want state=%q until=%q", candidateKey, feedback, wantState, wantUntil)
			}
			return
		}
	}
	t.Fatalf("feedback for %q missing from %#v", candidateKey, response.Feedback)
}

func relatedResponseContains(response relatedResponse, candidateKey string) bool {
	for _, result := range response.Results {
		if result.Candidate.Key == candidateKey {
			return true
		}
	}
	return false
}
