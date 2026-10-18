package web

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func TestRelatedNotesCrossLayerContract(t *testing.T) {
	templateBody := readContractFile(t, "templates/note.html")
	for _, attribute := range []string{
		`data-related-notes`,
		`data-related-source-key="{{.Key}}"`,
		`data-related-endpoint="/api/related"`,
		`data-related-item-endpoint="/api/related/item"`,
		`data-related-csrf="{{.CSRFToken}}"`,
		`data-related-status`,
	} {
		if !strings.Contains(templateBody, attribute) {
			t.Fatalf("note-page related mount missing %q", attribute)
		}
	}
	if bodyIndex, relatedIndex, brokenIndex := strings.Index(templateBody, `aria-label="Note content"`), strings.Index(templateBody, `data-related-notes`), strings.Index(templateBody, `<h2>Broken links</h2>`); bodyIndex < 0 || relatedIndex < bodyIndex || brokenIndex >= 0 && relatedIndex > brokenIndex {
		t.Fatalf("Related Notes mount must remain after Body and before Broken links: body=%d related=%d broken=%d", bodyIndex, relatedIndex, brokenIndex)
	}

	trueValue := true
	readPayload := relatedResponse{
		ServerDate: "2026-08-19",
		Source:     relatedNoteIdentity{Key: "source", Title: "Source", Path: "Source.md", Route: "/n/source"},
		Mode:       recall.ModeBalanced,
		HasMore:    true,
		Results: []relatedResultResponse{{
			Candidate:    relatedNoteIdentity{Key: "candidate", Title: "Candidate", Path: "Candidate.md", Route: "/n/candidate"},
			Excerpt:      "Unicode résumé excerpt",
			Relationship: recall.RelationshipSharedNeighbor,
			Bridge:       &relatedNoteIdentity{Key: "bridge", Title: "Bridge", Path: "Bridge.md", Route: "/n/bridge"},
			Reasons:      []relatedReasonResponse{{Kind: recall.ReasonText, Detail: "Shared phrase"}},
			Useful:       true,
		}},
		Feedback: []relatedFeedbackResponse{{
			Candidate: relatedNoteIdentity{Key: "candidate", Title: "Candidate", Path: "Candidate.md", Route: "/n/candidate"},
			State:     recall.FeedbackLater,
			Until:     "2026-08-20",
		}},
	}
	assertJSONKeys(t, readPayload, []string{"feedback", "has_more", "mode", "results", "server_date", "source"})
	assertNestedJSONKeys(t, readPayload, "source", []string{"key", "path", "route", "title"})
	assertNestedArrayJSONKeys(t, readPayload, "results", 0, []string{"bridge", "candidate", "excerpt", "reasons", "relationship", "useful"})
	assertNestedArrayJSONKeys(t, readPayload, "feedback", 0, []string{"candidate", "state", "until"})

	itemPayload := relatedItemResponse{
		OK:        true,
		Action:    "open_editor",
		Source:    readPayload.Source,
		Candidate: readPayload.Results[0].Candidate,
		State:     recall.FeedbackUseful,
		Until:     "2026-08-20",
		Opened:    true,
		Recorded:  &trueValue,
		Warning:   "warning",
	}
	assertJSONKeys(t, itemPayload, []string{"action", "candidate", "ok", "opened", "recorded", "source", "state", "until", "warning"})

	typescriptFixture := readContractFile(t, "static_src/related-api.test.ts")
	for _, field := range []string{
		`server_date:`, `has_more:`, `source:`, `mode:`, `results:`, `feedback:`,
		`candidate:`, `excerpt:`, `relationship:`, `bridge:`, `reasons:`, `useful:`,
		`state: "later"`, `until:`, `action: "open_editor"`, `opened: true`, `recorded: false`, `warning:`,
	} {
		if !strings.Contains(typescriptFixture, field) {
			t.Fatalf("TypeScript normalization fixture missing Go JSON contract field %q", field)
		}
	}
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func assertJSONKeys(t *testing.T, value any, want []string) {
	t.Helper()
	var object map[string]json.RawMessage
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal contract value: %v", err)
	}
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decode contract object: %v", err)
	}
	got := sortedJSONKeys(object)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %q, want %q; payload=%s", got, want, data)
	}
}

func assertNestedJSONKeys(t *testing.T, value any, field string, want []string) {
	t.Helper()
	var object map[string]json.RawMessage
	data, _ := json.Marshal(value)
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(object[field], &nested); err != nil {
		t.Fatalf("decode nested field %s: %v", field, err)
	}
	if got := sortedJSONKeys(nested); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s JSON keys = %q, want %q", field, got, want)
	}
}

func assertNestedArrayJSONKeys(t *testing.T, value any, field string, index int, want []string) {
	t.Helper()
	var object map[string]json.RawMessage
	data, _ := json.Marshal(value)
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	var nested []map[string]json.RawMessage
	if err := json.Unmarshal(object[field], &nested); err != nil {
		t.Fatalf("decode nested field %s: %v", field, err)
	}
	if got := sortedJSONKeys(nested[index]); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s[%d] JSON keys = %q, want %q", field, index, got, want)
	}
}

func sortedJSONKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
