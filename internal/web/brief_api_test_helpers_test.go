package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/brief"
	"github.com/sglavoie/brainnotes/internal/config"
)

func newHTTPServerForExistingRootWithOptions(
	t *testing.T,
	root string,
	configure func(cfg *config.Config),
	opts Options,
) *httptest.Server {
	t.Helper()
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	if configure != nil {
		configure(cfg)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postBriefViewRequest(t *testing.T, baseURL, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/brief/view", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/brief/view: %v", err)
	}
	return resp
}

func postBriefItemRequest(t *testing.T, baseURL, cardID, action, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if cardID != "" {
		form.Set("card_id", cardID)
	}
	if action != "" {
		form.Set("action", action)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/brief/item", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/brief/item: %v", err)
	}
	return resp
}

func postBriefInsightRequest(t *testing.T, baseURL, cardID, headerToken, formToken string) *http.Response {
	t.Helper()
	return postBriefInsightRequestWithPrompt(t, baseURL, cardID, "", headerToken, formToken)
}

func postBriefInsightRequestWithPrompt(t *testing.T, baseURL, cardID, prompt, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if cardID != "" {
		form.Set("card_id", cardID)
	}
	if prompt != "" {
		form.Set("prompt", prompt)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/brief/insight", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/brief/insight: %v", err)
	}
	return resp
}

func decodeBriefViewResponse(t *testing.T, body string) briefViewResponse {
	t.Helper()
	var payload briefViewResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode brief view response: %v; body=%q", err, body)
	}
	return payload
}

func decodeBriefItemResponse(t *testing.T, body string) briefItemResponse {
	t.Helper()
	var payload briefItemResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode brief item response: %v; body=%q", err, body)
	}
	return payload
}

func decodeBriefInsightResponse(t *testing.T, body string) briefInsightResponse {
	t.Helper()
	var payload briefInsightResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode brief insight response: %v; body=%q", err, body)
	}
	return payload
}

func mustBriefCardID(t *testing.T, now time.Time, slot brief.SlotKind, sourcePath, candidatePath string) brief.CardID {
	t.Helper()
	cardID, err := brief.CardIDFor(brief.LocalDate(now.Format("2006-01-02")), slot, sourcePath, candidatePath)
	if err != nil {
		t.Fatalf("card id: %v", err)
	}
	return cardID
}

func mustReadBriefDailySelection(t *testing.T, root string, now time.Time) brief.DailySelection {
	t.Helper()
	store := brief.NewFileStateStore(root, brief.WithClock(func() time.Time { return now }))
	date := brief.LocalDate(now.Format("2006-01-02"))
	selection, ok, err := store.ReadDailySelection(date)
	if err != nil {
		t.Fatalf("ReadDailySelection(%s): %v", date, err)
	}
	if !ok {
		t.Fatalf("ReadDailySelection(%s): no selection", date)
	}
	return selection
}

func mustSelectionCardForSlot(t *testing.T, selection brief.DailySelection, slot brief.SlotKind) brief.Card {
	t.Helper()
	for _, card := range selection.Cards {
		if card.Slot == slot {
			return card
		}
	}
	t.Fatalf("selection missing slot %q: %#v", slot, selection)
	return brief.Card{}
}

func mustDailyCardForSlot(t *testing.T, daily brief.DailyBrief, slot brief.SlotKind) brief.Card {
	t.Helper()
	for _, result := range daily.Slots {
		if result.Slot == slot && result.Card != nil {
			return *result.Card
		}
	}
	t.Fatalf("daily brief missing card slot %q: %#v", slot, daily)
	return brief.Card{}
}

func mustAffectedCardForSlot(t *testing.T, affected []brief.SlotResult, slot brief.SlotKind) brief.Card {
	t.Helper()
	for _, result := range affected {
		if result.Slot == slot && result.Card != nil {
			return *result.Card
		}
	}
	t.Fatalf("affected results missing card slot %q: %#v", slot, affected)
	return brief.Card{}
}
