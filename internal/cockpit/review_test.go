package cockpit

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// frontFrom parses a YAML frontmatter block (without the --- fences) into a Map.
func frontFrom(t *testing.T, yaml string) *frontmatter.Map {
	t.Helper()
	doc := "---\n" + yaml + "\n---\n"
	parsed, err := frontmatter.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse frontmatter: %v", err)
	}
	return &parsed.Front
}

func TestStatusValue(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"string", "status: Active", "Active"},
		{"whitespace trimmed", "status: '  Active  '", "Active"},
		{"int coerced", "status: 5", "5"},
		{"bool coerced", "status: true", "true"},
		{"absent", "type: note", ""},
		{"empty", "status: ''", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusValue(frontFrom(t, tc.yaml)); got != tc.want {
				t.Errorf("statusValue = %q, want %q", got, tc.want)
			}
		})
	}
	if got := statusValue(nil); got != "" {
		t.Errorf("statusValue(nil) = %q, want empty", got)
	}
}

func TestStatusInSet(t *testing.T) {
	allowed := []string{"Reading", "  In Progress  "}
	cases := []struct {
		status string
		want   bool
	}{
		{"reading", true},
		{"READING", true},
		{"in progress", true},
		{"done", false},
		{"", false},
		{"  ", false},
	}
	for _, tc := range cases {
		if got := statusInSet(tc.status, allowed); got != tc.want {
			t.Errorf("statusInSet(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
	if statusInSet("reading", nil) {
		t.Errorf("statusInSet against empty list should never match")
	}
}

func TestNoteTimestamp(t *testing.T) {
	front := frontFrom(t, "last: 2026-01-02\ncreated: 2025-06-01\ntitle: note")

	got, ok := noteTimestamp(front, "last", "created")
	if !ok {
		t.Fatalf("noteTimestamp: expected a match")
	}
	if want := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("noteTimestamp = %v, want %v", got, want)
	}

	// Falls through to the second property when the first is absent.
	got, ok = noteTimestamp(front, "missing", "created")
	if !ok || got.Year() != 2025 {
		t.Errorf("noteTimestamp fallback = %v ok=%v, want 2025", got, ok)
	}

	// A non-time property is not treated as a timestamp.
	if _, ok := noteTimestamp(front, "title"); ok {
		t.Errorf("noteTimestamp should not match a non-time property")
	}
	if _, ok := noteTimestamp(nil, "last"); ok {
		t.Errorf("noteTimestamp(nil) should not match")
	}
}

func TestDaysSince(t *testing.T) {
	now := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		then time.Time
		want int
	}{
		{"31 days", time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), 31},
		{"same instant", now, 0},
		{"future", now.Add(48 * time.Hour), 0},
		{"zero time", time.Time{}, 0},
		{"partial day floors down", now.Add(-36 * time.Hour), 1},
	}
	for _, tc := range cases {
		if got := daysSince(now, tc.then); got != tc.want {
			t.Errorf("%s: daysSince = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestDaysSinceCountsLocalCalendarDays pins the age to the user's calendar at
// 21:00 in UTC-4, when the UTC date has already rolled over. A YAML review date
// parses as midnight UTC, and measuring elapsed hours from that instant made a
// date reviewed two local days ago read as three.
func TestDaysSinceCountsLocalCalendarDays(t *testing.T) {
	zone := time.FixedZone("UTC-4", -4*60*60)
	now := time.Date(2026, 5, 20, 21, 0, 0, 0, zone)
	cases := []struct {
		name string
		then time.Time
		want int
	}{
		{"YAML date two local days back", time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), 2},
		{"YAML date today", time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), 0},
		{"YAML date tomorrow", time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC), 0},
		{"local timestamp late yesterday", time.Date(2026, 5, 19, 23, 30, 0, 0, zone), 1},
		{"local timestamp earlier today", time.Date(2026, 5, 20, 0, 30, 0, 0, zone), 0},
	}
	for _, tc := range cases {
		if got := daysSince(now, tc.then); got != tc.want {
			t.Errorf("%s: daysSince = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestIsHubNote(t *testing.T) {
	hubTypes := []string{"moc", "hub"}
	if !isHubNote(frontFrom(t, "type: MOC"), hubTypes) {
		t.Errorf("type=MOC should be a hub (case-insensitive)")
	}
	if !isHubNote(frontFrom(t, "kind: Hub"), hubTypes) {
		t.Errorf("kind=Hub should be a hub (kind consulted like query.Type)")
	}
	if isHubNote(frontFrom(t, "type: note"), hubTypes) {
		t.Errorf("type=note should not be a hub")
	}
	if isHubNote(frontFrom(t, "type: MOC"), nil) {
		t.Errorf("empty hubTypes should disable hub detection")
	}
	if isHubNote(nil, hubTypes) {
		t.Errorf("nil front should not be a hub")
	}
}

func TestEmptyHubHeadings(t *testing.T) {
	body := "# Title\n\n" +
		"## Synthesis\n\n\n" + // empty
		"## Notes\n\nSome real content here.\n\n" + // filled
		"## Open Questions\n" // empty, trailing at EOF

	got := emptyHubHeadings(body, []string{"Synthesis", "Notes", "Open Questions", "Missing"})
	want := []string{"Synthesis", "Open Questions"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("emptyHubHeadings = %v, want %v", got, want)
	}

	// A fenced code block counts as content even when it looks blank inside.
	fenced := "## Synthesis\n```\n\n\n```\n"
	if got := emptyHubHeadings(fenced, []string{"Synthesis"}); len(got) != 0 {
		t.Errorf("fenced content should not be empty: got %v", got)
	}

	// Headings inside a fence are ignored entirely.
	inFence := "## Synthesis\n```\n## Not A Heading\n```\n"
	if got := emptyHubHeadings(inFence, []string{"Not A Heading"}); len(got) != 0 {
		t.Errorf("headings inside a fence must be ignored: got %v", got)
	}

	// Only a closer of the same character and at least the opener's length ends
	// a fence: the "~~~" and short "```" here are content of the outer block.
	mismatched := "## Synthesis\n````\n~~~\n```\n## Not A Heading\n````\n"
	if got := emptyHubHeadings(mismatched, []string{"Not A Heading"}); len(got) != 0 {
		t.Errorf("a non-matching fence line must not close the block: got %v", got)
	}

	if got := emptyHubHeadings("", []string{"Synthesis"}); got != nil {
		t.Errorf("empty body should yield nil, got %v", got)
	}
	if got := emptyHubHeadings(body, nil); got != nil {
		t.Errorf("no wanted titles should yield nil, got %v", got)
	}
}

func TestEmptyHubHeadingsFilledSectionNotFlagged(t *testing.T) {
	// A same-named heading that is filled must not flag the title even if an
	// earlier occurrence was empty: qualification requires an empty occurrence,
	// so exercise the "filled only" path explicitly.
	body := "## Synthesis\n\nReal content.\n"
	if got := emptyHubHeadings(body, []string{"Synthesis"}); len(got) != 0 {
		t.Errorf("filled section must not be flagged: got %v", got)
	}
}

func TestLimitDaily(t *testing.T) {
	tasks := []Task{{ID: "1"}, {ID: "2"}, {ID: "3"}}

	if got := limitDaily(tasks, 2); len(got) != 2 {
		t.Errorf("limitDaily(2) len = %d, want 2", len(got))
	}
	if got := limitDaily(tasks, 0); len(got) != 3 {
		t.Errorf("limitDaily(0) should not cap: len = %d, want 3", len(got))
	}
	if got := limitDaily(tasks, -1); len(got) != 3 {
		t.Errorf("limitDaily(-1) should not cap: len = %d, want 3", len(got))
	}
	if got := limitDaily(tasks, 10); len(got) != 3 {
		t.Errorf("limitDaily above length should return all: len = %d, want 3", len(got))
	}
}
