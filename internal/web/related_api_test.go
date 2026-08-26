package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func TestServer_RelatedAPI_DefaultsModesLimitsAndScoreFreeContract(t *testing.T) {
	now := time.Date(2026, time.August, 18, 23, 30, 0, 0, time.FixedZone("web-related", -6*60*60))
	ts, _ := newHTTPServerForFilesWithOptions(t, denseGraphVaultFixture(), nil, Options{
		BriefNow: func() time.Time { return now },
	})

	defaultBody, payload := getRelatedResponse(t, ts.URL, url.Values{"source": {"hub"}})
	if payload.ServerDate != "2026-08-18" || payload.Mode != recall.ModeBalanced {
		t.Fatalf("default payload date/mode = %q/%q", payload.ServerDate, payload.Mode)
	}
	if payload.Source != (relatedNoteIdentity{Key: "hub", Title: "Hub", Path: "Hub.md", Route: "/n/hub"}) {
		t.Fatalf("source identity = %#v", payload.Source)
	}
	if len(payload.Results) != defaultRelatedAPILimit || !payload.HasMore {
		t.Fatalf("default results/has_more = %d/%v, want %d/true", len(payload.Results), payload.HasMore, defaultRelatedAPILimit)
	}
	for _, forbidden := range []string{`"score"`, `"signals"`, `"contribution"`, `"weights"`} {
		if strings.Contains(defaultBody, forbidden) {
			t.Fatalf("score-bearing field %s leaked into response: %s", forbidden, defaultBody)
		}
	}
	for _, result := range payload.Results {
		if result.Candidate.Key == "" || result.Candidate.Route == "" || result.Candidate.Path == "" {
			t.Fatalf("incomplete candidate identity: %#v", result.Candidate)
		}
		if len(result.Reasons) > 3 {
			t.Fatalf("reason count = %d, want at most 3", len(result.Reasons))
		}
	}

	for _, mode := range []recall.Mode{recall.ModeBalanced, recall.ModeRediscover, recall.ModeConnect} {
		_, got := getRelatedResponse(t, ts.URL, url.Values{
			"source": {"hub"},
			"mode":   {string(mode)},
			"limit":  {"1"},
		})
		if got.Mode != mode {
			t.Errorf("mode = %q, want %q", got.Mode, mode)
		}
		if len(got.Results) > 1 {
			t.Errorf("%s result count = %d, want <= 1", mode, len(got.Results))
		}
	}

	firstBody, first := getRelatedResponse(t, ts.URL, url.Values{"source": {"hub"}, "limit": {"20"}})
	secondBody, second := getRelatedResponse(t, ts.URL, url.Values{"source": {"hub"}, "limit": {"20"}})
	if first.HasMore || second.HasMore || !reflect.DeepEqual(first, second) || firstBody != secondBody {
		t.Fatalf("full deterministic response mismatch\nfirst=%s\nsecond=%s", firstBody, secondBody)
	}
}

func TestServer_RelatedAPI_MetadataRelationshipsBridgeExcerptAndReasons(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\ntopic: shared-value\ncreated: 2020-01-01\n---\nSource prose [[Bridge]] [[Direct]]\n"),
		"Bridge.md":    []byte("---\ntitle: Bridge\n---\nBridge prose\n"),
		"Candidate.md": []byte("---\ntitle: Candidate\ntopic: shared-value\ncreated: 2020-01-01\n---\nCandidate prose is the representative excerpt. [[Bridge]]\n"),
		"Direct.md":    []byte("---\ntitle: Direct\ncreated: 2020-01-01\n---\nA directly linked note.\n"),
	}, func(cfg *config.Config) {
		cfg.Related.MetadataProperties = []string{"topic"}
	}, Options{BriefNow: func() time.Time { return now }})

	_, balanced := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "mode": {"balanced"}, "limit": {"20"}})
	candidate := relatedResultForKey(t, balanced, "candidate")
	if candidate.Relationship != recall.RelationshipSharedNeighbor || candidate.Bridge == nil || candidate.Bridge.Key != "bridge" {
		t.Fatalf("candidate relationship/bridge = %q/%#v", candidate.Relationship, candidate.Bridge)
	}
	if candidate.Excerpt != "Candidate prose is the representative excerpt. [[Bridge]]" {
		t.Fatalf("candidate excerpt = %q", candidate.Excerpt)
	}
	if !hasRelatedReason(candidate.Reasons, recall.ReasonMetadata) {
		t.Fatalf("candidate reasons = %#v, want metadata", candidate.Reasons)
	}
	direct := relatedResultForKey(t, balanced, "direct")
	if direct.Relationship != recall.RelationshipDirect || direct.Bridge != nil {
		t.Fatalf("direct relationship/bridge = %q/%#v", direct.Relationship, direct.Bridge)
	}

	_, rediscover := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "mode": {"rediscover"}, "limit": {"20"}})
	relatedResultForKey(t, rediscover, "direct")
	_, connect := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "mode": {"connect"}, "limit": {"20"}})
	relatedResultForKey(t, connect, "candidate")
}

func TestServer_RelatedAPI_ArbitraryLimitAndTwentyResultCap(t *testing.T) {
	files := map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\ncommon ranking vocabulary\n"),
	}
	for index := 1; index <= 22; index++ {
		name := fmt.Sprintf("Candidate%02d", index)
		files[name+".md"] = []byte(fmt.Sprintf("---\ntitle: %s\n---\ncommon ranking vocabulary candidate %d\n", name, index))
	}
	ts, _ := newHTTPServerForFiles(t, files)

	_, seven := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "limit": {"7"}})
	if len(seven.Results) != 7 || !seven.HasMore {
		t.Fatalf("limit 7 results/has_more = %d/%v", len(seven.Results), seven.HasMore)
	}
	_, twenty := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "limit": {"20"}})
	if len(twenty.Results) != 20 || !twenty.HasMore {
		t.Fatalf("limit 20 results/has_more = %d/%v", len(twenty.Results), twenty.HasMore)
	}
}

func TestServer_RelatedAPI_ActiveFeedbackAndSnapshotFiltering(t *testing.T) {
	now := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\n---\nshared words\n"),
		"Useful.md":    []byte("---\ntitle: Useful\n---\nshared words\n"),
		"Dismissed.md": []byte("---\ntitle: Dismissed\n---\nshared words\n"),
		"Later.md":     []byte("---\ntitle: Later\n---\nshared words\n"),
		"Ignored.md":   []byte("---\ntitle: Ignored\n---\nshared words\n"),
	}, func(cfg *config.Config) {
		cfg.Ignore = append(cfg.Ignore, "Ignored.md")
	}, Options{BriefNow: func() time.Time { return now }})
	store := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now }))
	for _, change := range []struct {
		candidate string
		state     recall.FeedbackState
		until     time.Time
	}{
		{candidate: "Useful.md", state: recall.FeedbackUseful},
		{candidate: "Dismissed.md", state: recall.FeedbackDismissed},
		{candidate: "Later.md", state: recall.FeedbackLater, until: now.AddDate(0, 0, 7)},
		{candidate: "Ignored.md", state: recall.FeedbackUseful},
	} {
		if err := store.SetFeedback("Source.md", change.candidate, change.state, change.until); err != nil {
			t.Fatalf("seed %s feedback: %v", change.candidate, err)
		}
	}

	_, payload := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "limit": {"1"}})
	if len(payload.Results) != 1 || payload.Results[0].Candidate.Key != "useful" || !payload.Results[0].Useful {
		t.Fatalf("visible useful result = %#v", payload.Results)
	}
	if len(payload.Feedback) != 3 {
		t.Fatalf("feedback = %#v, want three snapshot-visible entries", payload.Feedback)
	}
	states := make(map[string]relatedFeedbackResponse, len(payload.Feedback))
	for _, feedback := range payload.Feedback {
		states[feedback.Candidate.Key] = feedback
	}
	if states["useful"].State != recall.FeedbackUseful || states["dismissed"].State != recall.FeedbackDismissed {
		t.Fatalf("feedback states = %#v", states)
	}
	if states["later"].State != recall.FeedbackLater || states["later"].Until != "2026-08-25" {
		t.Fatalf("later feedback = %#v", states["later"])
	}
	if _, ok := states["ignored"]; ok {
		t.Fatalf("ignored note leaked through feedback: %#v", states["ignored"])
	}
}

func TestServer_RelatedAPI_ValidationEmptyAndMissingState(t *testing.T) {
	tests := []struct {
		name   string
		query  url.Values
		status int
	}{
		{name: "invalid mode", query: url.Values{"source": {"source"}, "mode": {"unknown"}}, status: http.StatusBadRequest},
		{name: "explicit empty mode", query: url.Values{"source": {"source"}, "mode": {""}}, status: http.StatusBadRequest},
		{name: "invalid zero limit", query: url.Values{"source": {"source"}, "limit": {"0"}}, status: http.StatusBadRequest},
		{name: "invalid cap", query: url.Values{"source": {"source"}, "limit": {"21"}}, status: http.StatusBadRequest},
		{name: "invalid limit syntax", query: url.Values{"source": {"source"}, "limit": {"five"}}, status: http.StatusBadRequest},
		{name: "missing source", query: url.Values{}, status: http.StatusNotFound},
		{name: "stale source", query: url.Values{"source": {"stale"}}, status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ts, _ := newHTTPServerForFiles(t, map[string][]byte{"Source.md": []byte("---\ntitle: Source\n---\nbody\n")})
			resp := getRelated(t, ts.URL, test.query)
			body := readResponseBody(t, resp)
			if resp.StatusCode != test.status {
				t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, test.status, body)
			}
		})
	}

	t.Run("method", func(t *testing.T) {
		ts, _ := newHTTPServerForFiles(t, map[string][]byte{"Source.md": []byte("---\ntitle: Source\n---\nbody\n")})
		req, err := http.NewRequest(http.MethodPost, ts.URL+relatedAPIRoutePath, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodGet {
			t.Fatalf("method status/allow = %d/%q; body=%s", resp.StatusCode, resp.Header.Get("Allow"), body)
		}
	})

	t.Run("empty vault", func(t *testing.T) {
		ts, _ := newHTTPServerForFiles(t, map[string][]byte{})
		resp := getRelated(t, ts.URL, url.Values{"source": {"source"}})
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("empty vault status = %d, want 404; body=%s", resp.StatusCode, body)
		}
	})

	t.Run("no eligible candidates and missing state is non-mutating", func(t *testing.T) {
		ts, root := newHTTPServerForFiles(t, map[string][]byte{"Source.md": []byte("---\ntitle: Source\n---\nbody\n")})
		_, payload := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}})
		if payload.Results == nil || payload.Feedback == nil || len(payload.Results) != 0 || len(payload.Feedback) != 0 || payload.HasMore {
			t.Fatalf("empty payload = %#v", payload)
		}
		// .bnotes itself is no longer proof of a mutation: every snapshot load
		// writes the note cache under .bnotes/index, which holds no user intent
		// and is a supported thing to delete. The claim here is about recall's
		// state store, so assert on that and on the absence of any *other*
		// sidecar rather than on the directory as a whole.
		if _, err := os.Stat(filepath.Join(root, ".bnotes", "recall.json")); !os.IsNotExist(err) {
			t.Fatalf("read-only query created recall state: %v", err)
		}
		sidecars, err := os.ReadDir(filepath.Join(root, ".bnotes"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read .bnotes: %v", err)
		}
		for _, entry := range sidecars {
			if entry.Name() != "index" {
				t.Fatalf("read-only query created .bnotes/%s", entry.Name())
			}
		}
	})

	t.Run("corrupt state conflicts before empty ranking", func(t *testing.T) {
		ts, root := newHTTPServerForFiles(t, map[string][]byte{"Source.md": []byte("---\ntitle: Source\n---\nbody\n")})
		statePath := filepath.Join(root, ".bnotes", "recall.json")
		if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, []byte("{not json\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		resp := getRelated(t, ts.URL, url.Values{"source": {"source"}})
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "recall state is unreadable") {
			t.Fatalf("corrupt state status/body = %d/%s", resp.StatusCode, body)
		}
		got, err := os.ReadFile(statePath)
		if err != nil || string(got) != "{not json\n" {
			t.Fatalf("corrupt state changed: %q, %v", got, err)
		}
	})
}

func TestServer_RelatedAPI_QuarantinedNoteNeverAppears(t *testing.T) {
	ts, root, callCount := newBlockedLoaderServer(t, map[string][]byte{
		"Source.md":  []byte("---\ntitle: Source\n---\nshared body\n"),
		"Blocked.md": []byte("---\ntitle: Blocked\n---\nshared body\n"),
		"Safe.md":    []byte("---\ntitle: Safe\n---\nshared body\n"),
	}, "Blocked.md", nil, Options{})
	if err := recall.NewStateStore(root).SetFeedback("Source.md", "Blocked.md", recall.FeedbackUseful, time.Time{}); err != nil {
		t.Fatalf("seed blocked feedback: %v", err)
	}
	_, payload := getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "limit": {"20"}})
	for _, result := range payload.Results {
		if result.Candidate.Key == "blocked" || result.Bridge != nil && result.Bridge.Key == "blocked" {
			t.Fatalf("quarantined note leaked: %#v", result)
		}
	}
	for _, feedback := range payload.Feedback {
		if feedback.Candidate.Key == "blocked" {
			t.Fatalf("quarantined feedback leaked: %#v", feedback)
		}
	}
	if got := callCount("Blocked.md"); got > 1 {
		t.Fatalf("blocked note loaded %d times, want at most once", got)
	}
}

func getRelated(t *testing.T, baseURL string, values url.Values) *http.Response {
	t.Helper()
	resp, err := http.Get(baseURL + relatedAPIRoutePath + "?" + values.Encode()) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET related: %v", err)
	}
	return resp
}

func getRelatedResponse(t *testing.T, baseURL string, values url.Values) (string, relatedResponse) {
	t.Helper()
	resp := getRelated(t, baseURL, values)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("related status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var payload relatedResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode related response: %v; body=%s", err, body)
	}
	return body, payload
}

func relatedResultForKey(t *testing.T, response relatedResponse, key string) relatedResultResponse {
	t.Helper()
	for _, result := range response.Results {
		if result.Candidate.Key == key {
			return result
		}
	}
	t.Fatalf("result %q not found in %#v", key, response.Results)
	return relatedResultResponse{}
}

func hasRelatedReason(reasons []relatedReasonResponse, kind recall.ReasonKind) bool {
	for _, reason := range reasons {
		if reason.Kind == kind && reason.Detail != "" {
			return true
		}
	}
	return false
}
