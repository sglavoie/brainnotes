package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
)

func TestOperationOutcomeFindsExactTrashAttempt(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"A.md": []byte("body\n"),
	}, nil, Options{CSRFToken: "csrf-token"})
	const id = "0123456789abcdef0123456789abcdef"
	const otherID = "fedcba9876543210fedcba9876543210"
	if got := checkOperationOutcome(t, ts.URL, id)["status"]; got != "unconfirmed" {
		t.Fatalf("before write status = %v", got)
	}
	postTrashWithOperationID(t, ts.URL, id)
	if got := checkOperationOutcome(t, ts.URL, id); got["status"] != "confirmed" || got["journal_id"] == "" {
		t.Fatalf("recorded outcome = %#v", got)
	}
	if got := checkOperationOutcome(t, ts.URL, otherID)["status"]; got != "unconfirmed" {
		t.Fatalf("unrelated outcome = %v", got)
	}
	invalid, err := http.Get(ts.URL + operationOutcomeRoutePath + "?id=../A")
	if err != nil {
		t.Fatal(err)
	}
	invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid ID status = %d, want 400", invalid.StatusCode)
	}
}

func TestOperationOutcomeFindsImmediatePropertyValue(t *testing.T) {
	ts, _, root := newPropertyFixtureServer(t)
	const id = "11111111111111111111111111111111"
	status, result := postPropertyMutation(t, ts.URL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
		OperationID:  id,
		Note:         "Alpha.md",
		Property:     "status",
		Op:           propertyValueOpReplace,
		ExpectedHash: noteHash(t, root, "Alpha.md"),
		Value:        &properties.TypedValue{Kind: config.KindString, Text: "done"},
	})
	if status != http.StatusOK {
		t.Fatalf("property value status = %d", status)
	}
	if got := checkOperationOutcome(t, ts.URL, id); got["status"] != "confirmed" || got["journal_id"] != result.JournalID {
		t.Fatalf("property value outcome = %#v, want journal %q", got, result.JournalID)
	}
}

func TestOperationOutcomeFindsTemplateApply(t *testing.T) {
	ts, _, _ := newTemplatesFixtureServer(t)
	const id = "22222222222222222222222222222222"
	preview := previewTemplate(t, ts.URL, templateMutationRequest{
		Op: templateOpSave, Name: "New.md", Content: templateTestBody,
	})
	status, result, raw := postTemplates(t, ts.URL, templatesApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: preview.PreviewID, OperationID: id})
	if status != http.StatusOK {
		t.Fatalf("template apply status = %d, body %q", status, raw)
	}
	if got := checkOperationOutcome(t, ts.URL, id); got["status"] != "confirmed" || got["journal_id"] != result.JournalID {
		t.Fatalf("template outcome = %#v, want journal %q", got, result.JournalID)
	}
}

func checkOperationOutcome(t *testing.T, baseURL, id string) map[string]any {
	t.Helper()
	resp, err := http.Get(baseURL + operationOutcomeRoutePath + "?id=" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outcome status = %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func postTrashWithOperationID(t *testing.T, baseURL, id string) {
	t.Helper()
	form := url.Values{"key": {"a"}, "token": {"csrf-token"}, "operation_id": {id}}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/trash", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trash status = %d", response.StatusCode)
	}
}
