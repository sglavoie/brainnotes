package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
)

const propertyDatetimeConfigTOML = `[frontmatter.properties.published]
type = "datetime"

[frontmatter.properties.timeline]
type = "list"
items = "datetime"

[frontmatter.properties.reviewed]
type = "date"
`

func propertyDatetimeNotes() map[string]string {
	return map[string]string{
		"Alpha.md": "---\npublished: 2026-02-01T09:30:00Z\nreviewed: 2026-01-02\ntimeline:\n    - 2026-01-05T00:00:00Z\n    - 2026-01-06T00:00:00Z\ntitle: Alpha\n---\nAlpha body.\n",
	}
}

func newPropertyDatetimeServer(t *testing.T) (string, string) {
	t.Helper()
	ts, _, root := newPropertyServer(t, propertyDatetimeConfigTOML, propertyDatetimeNotes())
	return ts.URL, root
}

// classifyStoredProperty reads a note back from disk and classifies one of its
// properties, which is how a browser would see the value after a reload.
func classifyStoredProperty(t *testing.T, root, rel, key string) properties.Value {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		t.Fatalf("parse %q: %v", rel, err)
	}
	value, ok := properties.ClassifyProperty(&doc.Front, key)
	if !ok {
		t.Fatalf("%s has no %s property", rel, key)
	}
	return value
}

// TestPropertiesApply_TypedDatetimeMidnightPreservesSpelling covers the
// previewed multi-note write path. A datetime at exactly midnight UTC is
// indistinguishable from a date once it is a time.Time, so the requested kind
// has to survive all the way to the file.
func TestPropertiesApply_TypedDatetimeMidnightPreservesSpelling(t *testing.T) {
	t.Run("scalar datetime stays RFC3339", func(t *testing.T) {
		baseURL, root := newPropertyDatetimeServer(t)

		preview := previewProperty(t, baseURL, propertyMutationRequest{
			Action:   propertyActionSet,
			Property: "published",
			Notes:    []string{"Alpha.md"},
			Value:    &properties.TypedValue{Kind: config.KindDatetime, Text: "2026-03-01T00:00:00Z"},
		})
		if status, _ := applyProperty(t, baseURL, preview.PreviewID); status != http.StatusOK {
			t.Fatalf("apply status = %d", status)
		}

		if got := readVaultFile(t, root, "Alpha.md"); !strings.Contains(got, "published: 2026-03-01T00:00:00Z") {
			t.Fatalf("datetime was written as a date:\n%s", got)
		}
		if value := classifyStoredProperty(t, root, "Alpha.md", "published"); value.Kind != config.KindDatetime {
			t.Fatalf("reloaded kind = %q, want datetime", value.Kind)
		}
	})

	t.Run("list datetime elements stay RFC3339", func(t *testing.T) {
		baseURL, root := newPropertyDatetimeServer(t)

		preview := previewProperty(t, baseURL, propertyMutationRequest{
			Action:   propertyActionSet,
			Property: "timeline",
			Notes:    []string{"Alpha.md"},
			Value: &properties.TypedValue{Kind: config.KindList, Items: []properties.TypedValue{
				{Kind: config.KindDatetime, Text: "2026-03-01T00:00:00Z"},
				{Kind: config.KindDatetime, Text: "2026-03-02T14:15:00Z"},
			}},
		})
		if status, _ := applyProperty(t, baseURL, preview.PreviewID); status != http.StatusOK {
			t.Fatalf("apply status = %d", status)
		}

		got := readVaultFile(t, root, "Alpha.md")
		if !strings.Contains(got, "- 2026-03-01T00:00:00Z") {
			t.Fatalf("midnight element was written as a date:\n%s", got)
		}
		value := classifyStoredProperty(t, root, "Alpha.md", "timeline")
		if len(value.Items) != 2 || value.Items[0].Kind != config.KindDatetime {
			t.Fatalf("reloaded items = %#v", value.Items)
		}
	})

	t.Run("typed date still serializes as a date", func(t *testing.T) {
		baseURL, root := newPropertyDatetimeServer(t)

		preview := previewProperty(t, baseURL, propertyMutationRequest{
			Action:   propertyActionSet,
			Property: "reviewed",
			Notes:    []string{"Alpha.md"},
			Value:    &properties.TypedValue{Kind: config.KindDate, Text: "2026-03-05"},
		})
		if status, _ := applyProperty(t, baseURL, preview.PreviewID); status != http.StatusOK {
			t.Fatalf("apply status = %d", status)
		}

		if got := readVaultFile(t, root, "Alpha.md"); !strings.Contains(got, "reviewed: 2026-03-05\n") {
			t.Fatalf("date was written as a timestamp:\n%s", got)
		}
		if value := classifyStoredProperty(t, root, "Alpha.md", "reviewed"); value.Kind != config.KindDate {
			t.Fatalf("reloaded kind = %q, want date", value.Kind)
		}
	})

	t.Run("replacing one element leaves the others RFC3339", func(t *testing.T) {
		baseURL, root := newPropertyDatetimeServer(t)

		preview := previewProperty(t, baseURL, propertyMutationRequest{
			Action:   propertyActionReplaceValue,
			Property: "timeline",
			Notes:    []string{"Alpha.md"},
			Match:    &properties.TypedValue{Kind: config.KindDatetime, Text: "2026-01-05T00:00:00Z"},
			Value:    &properties.TypedValue{Kind: config.KindDatetime, Text: "2026-02-05T00:00:00Z"},
		})
		if status, _ := applyProperty(t, baseURL, preview.PreviewID); status != http.StatusOK {
			t.Fatalf("apply status = %d", status)
		}

		got := readVaultFile(t, root, "Alpha.md")
		if !strings.Contains(got, "- 2026-02-05T00:00:00Z") {
			t.Fatalf("replacement was written as a date:\n%s", got)
		}
		if !strings.Contains(got, "- 2026-01-06T00:00:00Z") {
			t.Fatalf("untouched element downgraded to a date:\n%s", got)
		}
	})
}

func TestBrowserRepairPreservesMidnightDatetimeSpelling(t *testing.T) {
	files := map[string]string{
		"Alpha.md": "---\n" +
			"published: '2026-03-01T00:00:00Z'\n" +
			"reviewed: '2026-03-05'\n" +
			"timeline:\n" +
			"  - '2026-03-02T00:00:00Z'\n" +
			"  - '2026-03-02T00:00:00Z'\n" +
			"---\nBody.\n",
	}
	ts, _, root := newPropertyServer(t, propertyDatetimeConfigTOML, files)
	for _, property := range []string{"published", "timeline", "reviewed"} {
		preview := previewProperty(t, ts.URL, propertyMutationRequest{
			Action:   propertyActionRepair,
			Property: property,
			Notes:    []string{"Alpha.md"},
		})
		if preview.Affected != 1 {
			t.Fatalf("%s affected = %d, want 1", property, preview.Affected)
		}
		if status, _ := applyProperty(t, ts.URL, preview.PreviewID); status != http.StatusOK {
			t.Fatalf("%s apply status = %d", property, status)
		}
	}

	data, err := os.ReadFile(filepath.Join(root, "Alpha.md"))
	if err != nil {
		t.Fatalf("read repaired note: %v", err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		t.Fatalf("reparse repaired note: %v", err)
	}
	published, _ := properties.ClassifyProperty(&doc.Front, "published")
	if published.Kind != config.KindDatetime {
		t.Fatalf("published kind = %q, want datetime", published.Kind)
	}
	timeline, _ := properties.ClassifyProperty(&doc.Front, "timeline")
	if len(timeline.Items) != 1 || timeline.Items[0].Kind != config.KindDatetime {
		t.Fatalf("timeline = %#v, want one datetime", timeline)
	}
	reviewed, _ := properties.ClassifyProperty(&doc.Front, "reviewed")
	if reviewed.Kind != config.KindDate {
		t.Fatalf("reviewed kind = %q, want date", reviewed.Kind)
	}
	definitions := map[string]config.PropertyDefinition{
		"published": {Type: config.KindDatetime},
		"timeline":  {Type: config.KindList, Items: config.KindDatetime},
		"reviewed":  {Type: config.KindDate},
	}
	if violations := schema.ValidateNote(&doc.Front, "Alpha.md", definitions); len(violations) != 0 {
		t.Fatalf("violations after repair = %#v", violations)
	}
	got := string(data)
	for _, want := range []string{
		"published: 2026-03-01T00:00:00Z",
		"- 2026-03-02T00:00:00Z",
		"reviewed: 2026-03-05\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repaired note missing %q:\n%s", want, got)
		}
	}
}

// TestPropertyValue_TypedDatetimeMidnightImmediateEdits covers the unpreviewed
// single-note edits, which write through their own path.
func TestPropertyValue_TypedDatetimeMidnightImmediateEdits(t *testing.T) {
	t.Run("list add appends a timestamp", func(t *testing.T) {
		baseURL, root := newPropertyDatetimeServer(t)

		status, payload := postPropertyMutation(t, baseURL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
			Note:         "Alpha.md",
			Property:     "timeline",
			Op:           propertyValueOpListAdd,
			ExpectedHash: noteHash(t, root, "Alpha.md"),
			Value:        &properties.TypedValue{Kind: config.KindDatetime, Text: "2026-03-01T00:00:00Z"},
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
		if payload.Affected != 1 {
			t.Fatalf("affected = %d, want 1", payload.Affected)
		}

		got := readVaultFile(t, root, "Alpha.md")
		if !strings.Contains(got, "- 2026-03-01T00:00:00Z") {
			t.Fatalf("added element was written as a date:\n%s", got)
		}
		if !strings.Contains(got, "- 2026-01-05T00:00:00Z") {
			t.Fatalf("existing element downgraded to a date:\n%s", got)
		}
	})

	t.Run("replacing a date with the same instant as a datetime rewrites it", func(t *testing.T) {
		ts, _, root := newPropertyServer(t, propertyDatetimeConfigTOML, map[string]string{
			"Alpha.md": "---\npublished: 2026-03-01\ntitle: Alpha\n---\nAlpha body.\n",
		})
		baseURL := ts.URL

		status, payload := postPropertyMutation(t, baseURL, propertyValueAPIRoutePath, propertyTestToken, propertyValueRequest{
			Note:         "Alpha.md",
			Property:     "published",
			Op:           propertyValueOpReplace,
			ExpectedHash: noteHash(t, root, "Alpha.md"),
			Value:        &properties.TypedValue{Kind: config.KindDatetime, Text: "2026-03-01T00:00:00Z"},
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
		if payload.Affected != 1 {
			t.Fatalf("affected = %d, want 1: a spelling change is a change", payload.Affected)
		}
		if got := readVaultFile(t, root, "Alpha.md"); !strings.Contains(got, "published: 2026-03-01T00:00:00Z") {
			t.Fatalf("the note kept the date spelling:\n%s", got)
		}
	})
}
