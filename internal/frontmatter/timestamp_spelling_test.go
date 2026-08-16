package frontmatter

import (
	"strings"
	"testing"
	"time"
)

// TestMarshalTypedDatetimeMidnightSpelling covers the write side of the
// date/datetime ambiguity: a midnight-UTC time.Time is indistinguishable from a
// date, so a caller that sets one programmatically must be able to say which
// spelling it meant.
func TestMarshalTypedDatetimeMidnightSpelling(t *testing.T) {
	midnight := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	t.Run("scalar datetime stays a timestamp", func(t *testing.T) {
		doc := newDoc()
		doc.Front.Set("published", midnight)
		doc.Front.SetTimeSpelling("published", false)

		got := marshalString(t, doc)
		if !strings.Contains(got, "published: 2026-03-01T00:00:00Z") {
			t.Fatalf("scalar datetime downgraded to a date:\n%s", got)
		}

		reparsed := parseString(t, got)
		if reparsed.Front.TimeIsDateOnly("published") {
			t.Fatal("reloaded value reads as a date")
		}
	})

	t.Run("scalar date stays a date", func(t *testing.T) {
		doc := newDoc()
		doc.Front.Set("reviewed", midnight)
		doc.Front.SetTimeSpelling("reviewed", true)

		got := marshalString(t, doc)
		if !strings.Contains(got, "reviewed: 2026-03-01\n") {
			t.Fatalf("scalar date was written as a timestamp:\n%s", got)
		}
	})

	t.Run("a set value with no declared spelling still reads as a date", func(t *testing.T) {
		doc := newDoc()
		doc.Front.Set("reviewed", midnight)

		got := marshalString(t, doc)
		if !strings.Contains(got, "reviewed: 2026-03-01\n") {
			t.Fatalf("undeclared spelling changed:\n%s", got)
		}
	})

	t.Run("list elements keep their own spelling", func(t *testing.T) {
		doc := newDoc()
		doc.Front.Set("timeline", []any{midnight, midnight.AddDate(0, 0, 1)})
		doc.Front.SetListTimeSpelling("timeline", 0, false)
		doc.Front.SetListTimeSpelling("timeline", 1, true)

		got := marshalString(t, doc)
		if !strings.Contains(got, "- 2026-03-01T00:00:00Z") {
			t.Fatalf("list datetime element downgraded to a date:\n%s", got)
		}
		if !strings.Contains(got, "- 2026-03-02\n") {
			t.Fatalf("list date element was written as a timestamp:\n%s", got)
		}

		reparsed := parseString(t, got)
		if reparsed.Front.ListTimeIsDateOnly("timeline", 0) {
			t.Fatal("reloaded element 0 reads as a date")
		}
		if !reparsed.Front.ListTimeIsDateOnly("timeline", 1) {
			t.Fatal("reloaded element 1 reads as a datetime")
		}
	})

	t.Run("a declared spelling never truncates a time of day", func(t *testing.T) {
		doc := newDoc()
		doc.Front.Set("published", time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC))
		doc.Front.SetTimeSpelling("published", true)

		got := marshalString(t, doc)
		if !strings.Contains(got, "published: 2026-03-01T09:30:00Z") {
			t.Fatalf("time of day was dropped:\n%s", got)
		}
	})

	t.Run("spelling a non-time value is a no-op", func(t *testing.T) {
		doc := newDoc()
		doc.Front.Set("title", "Alpha")
		doc.Front.SetTimeSpelling("title", false)
		doc.Front.SetListTimeSpelling("title", 0, false)

		got := marshalString(t, doc)
		if !strings.Contains(got, "title: Alpha") {
			t.Fatalf("string value changed:\n%s", got)
		}
	})
}

func newDoc() Document {
	return Document{Front: NewMap(), Body: "body\n"}
}

func marshalString(t *testing.T, doc Document) string {
	t.Helper()
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(out)
}

func parseString(t *testing.T, source string) Document {
	t.Helper()
	doc, err := Parse([]byte(source))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}
