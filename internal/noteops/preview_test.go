package noteops

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestFormatPreviewValue(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "null"},
		{"string", "hello", "hello"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"int", 42, "42"},
		{"int64", int64(7), "7"},
		{"whole float", 3.0, "3.0"},
		{"negative whole float", -7.0, "-7.0"},
		{"fractional float", 1.5, "1.5"},
		{"list", []any{"a", "b", "c"}, "[a, b, c]"},
		{"nested list", []any{"[[X]]", 2}, "[[[X]], 2]"},
		{"utc midnight date", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-01-01"},
		{"utc datetime", time.Date(2026, 1, 1, 14, 30, 0, 0, time.UTC), "2026-01-01T14:30:00Z"},
		// FixedZone("", 0) has offset 0 but is not time.UTC, so a midnight
		// value there must render as full RFC3339, never date-only.
		{"fixedzone midnight not date-only", time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("", 0)), "2026-01-01T00:00:00Z"},
		{"fractional seconds preserved", time.Date(2026, 1, 1, 14, 30, 0, 123456789, time.UTC), "2026-01-01T14:30:00.123456789Z"},
	}
	for _, c := range cases {
		if got := FormatPreviewValue(c.in); got != c.want {
			t.Errorf("%s: FormatPreviewValue(%v) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestRenderNotePreview_FrontmatterChanges(t *testing.T) {
	preview := NotePreview{
		RelPath: "Areas/Alpha.md",
		Changes: []PreviewChange{
			FrontmatterChange("status", true, "wip", true, "shipped"),
			FrontmatterChange("added", false, nil, true, "value"),
			FrontmatterChange("removed", true, "gone", false, nil),
		},
	}

	var buf bytes.Buffer
	if err := RenderNotePreview(&buf, preview); err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "--- Areas/Alpha.md\n" +
		"frontmatter[status]: wip -> shipped\n" +
		"frontmatter[added]:  -> value\n" +
		"frontmatter[removed]: gone -> \n"
	if got := buf.String(); got != want {
		t.Fatalf("render mismatch:\n--- want ---\n%q\n--- got ---\n%q", want, got)
	}
}

func TestRenderNotePreview_PathAndBody(t *testing.T) {
	preview := NotePreview{
		RelPath: "note.md",
		Changes: []PreviewChange{
			{Kind: PreviewPath, Old: PresentText("Old/note.md"), New: PresentText("New/note.md")},
			{Kind: PreviewBody, Line: 6, Old: PresentText("[[A]]"), New: PresentText("[[B]]")},
		},
	}

	var buf bytes.Buffer
	if err := RenderNotePreview(&buf, preview); err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "--- note.md\n" +
		"path: Old/note.md -> New/note.md\n" +
		"body:6: [[A]] -> [[B]]\n"
	if got := buf.String(); got != want {
		t.Fatalf("render mismatch:\n--- want ---\n%q\n--- got ---\n%q", want, got)
	}
}

func TestRenderNotePreview_NoChangesEmitsNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderNotePreview(&buf, NotePreview{RelPath: "note.md"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("expected no output for a preview with no changes, got %q", got)
	}
}

func TestRenderNotePreviews_MultipleNotes(t *testing.T) {
	previews := []NotePreview{
		{RelPath: "a.md", Changes: []PreviewChange{FrontmatterChange("status", false, nil, true, "done")}},
		{RelPath: "unchanged.md"},
		{RelPath: "b.md", Changes: []PreviewChange{FrontmatterChange("status", false, nil, true, "done")}},
	}

	var buf bytes.Buffer
	if err := RenderNotePreviews(&buf, previews); err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "--- a.md\nfrontmatter[status]:  -> done\n" +
		"--- b.md\nfrontmatter[status]:  -> done\n"
	if got := buf.String(); got != want {
		t.Fatalf("render mismatch:\n--- want ---\n%q\n--- got ---\n%q", want, got)
	}
}

// TestNotePreview_JSONRoundTrip pins that the shared preview model is JSON-safe
// for the web APIs and preserves the present/absent distinction.
func TestNotePreview_JSONRoundTrip(t *testing.T) {
	preview := NotePreview{
		RelPath: "note.md",
		Changes: []PreviewChange{
			FrontmatterChange("removed", true, "gone", false, nil),
		},
	}

	data, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got NotePreview
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(got.Changes))
	}
	change := got.Changes[0]
	if change.Kind != PreviewFrontmatter || change.Key != "removed" {
		t.Fatalf("unexpected change kind/key: %+v", change)
	}
	if !change.Old.Present || change.Old.Text != "gone" {
		t.Fatalf("expected present old value 'gone', got %+v", change.Old)
	}
	if change.New.Present {
		t.Fatalf("expected absent new value, got %+v", change.New)
	}
}
