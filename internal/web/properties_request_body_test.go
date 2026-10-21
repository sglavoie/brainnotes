package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

// chunkedReader hides a body's length from net/http, so the request goes out
// chunked and declares no Content-Length for requestGuard to refuse up front.
type chunkedReader struct{ io.Reader }

// postRawPropertyBody sends body to route without a declared length and returns
// the status and the decoded error code.
func postRawPropertyBody(t *testing.T, baseURL, route string, body []byte) (int, noteops.ErrorCode) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, baseURL+route, chunkedReader{bytes.NewReader(body)})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeaderName, propertyTestToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", route, err)
	}
	defer resp.Body.Close()
	var payload noteops.ErrorResult
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode error body %q: %v", raw, err)
	}
	return resp.StatusCode, payload.Code
}

// TestPropertyRequestRoutesAnswerAnOversizedBodyWith413 pins the claim
// requestGuard makes: a body over the bound is a 413, not a 400 "invalid request
// body", even when it is chunked and so declared no length to refuse up front.
// The settings route shares the decoder, so it is held to the same answer.
func TestPropertyRequestRoutesAnswerAnOversizedBodyWith413(t *testing.T) {
	ts, _, _ := newSettingsServer(t)
	body := []byte(`{"token":"` + propertyTestToken + `","key":"` + strings.Repeat("x", propertyRequestBodyLimit) + `"}`)

	for _, route := range []string{propertyPreviewAPIRoutePath, settingsPreviewAPIRoutePath} {
		status, code := postRawPropertyBody(t, ts.URL, route, body)
		if status != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: status = %d, want 413", route, status)
		}
		if code != noteops.CodeInvalidRequest {
			t.Fatalf("%s: code = %q, want %q", route, code, noteops.CodeInvalidRequest)
		}
	}
}

// TestPropertyRequestRoutesRefuseTrailingData keeps the body to one JSON value:
// a second value, or garbage after the first, is a malformed request rather
// than something silently ignored.
func TestPropertyRequestRoutesRefuseTrailingData(t *testing.T) {
	ts, _, _ := newSettingsServer(t)

	for _, trailer := range []string{`{"token":"x"}`, `garbage`} {
		body := []byte(`{"token":"` + propertyTestToken + `"}` + trailer)
		status, code := postRawPropertyBody(t, ts.URL, settingsPreviewAPIRoutePath, body)
		if status != http.StatusBadRequest || code != noteops.CodeInvalidRequest {
			t.Fatalf("trailer %q: status = %d code = %q, want 400 %q", trailer, status, code, noteops.CodeInvalidRequest)
		}
	}
}

func TestDecodePropertyRequestClassifiesItsBody(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantErr  bool
		tooLarge bool
	}{
		{name: "single value", body: `{"token":"t"}`},
		{name: "trailing whitespace", body: "{\"token\":\"t\"}\n\t "},
		{name: "second value", body: `{"token":"t"}{}`, wantErr: true},
		{name: "trailing garbage", body: `{"token":"t"} x`, wantErr: true},
		{name: "malformed", body: `{"token":`, wantErr: true},
		{name: "oversized", body: `{"token":"` + strings.Repeat("x", propertyRequestBodyLimit) + `"}`, wantErr: true, tooLarge: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, propertyPreviewAPIRoutePath, strings.NewReader(tc.body))
			var dst struct {
				Token string `json:"token"`
			}
			err := decodePropertyRequest(r, &dst)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil {
				return
			}
			if code := noteops.CodeOf(err); code != noteops.CodeInvalidRequest {
				t.Fatalf("code = %q, want %q", code, noteops.CodeInvalidRequest)
			}
			rec := httptest.NewRecorder()
			writeManagementError(rec, err)
			want := http.StatusBadRequest
			if tc.tooLarge {
				want = http.StatusRequestEntityTooLarge
			}
			if rec.Code != want {
				t.Fatalf("status = %d, want %d", rec.Code, want)
			}
		})
	}
}
