package frontmatter

import (
	"strings"
	"testing"
	"time"
)

// TestParseDateTimeNonUTC checks that timestamps with a non-UTC offset
// round-trip with their offset preserved (encodeTime should not falsely
// downgrade them to date-only).
func TestParseDateTimeNonUTC(t *testing.T) {
	in := "---\ncreated: 2025-12-13T00:00:00-05:00\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "2025-12-13T00:00:00-05:00") {
		t.Errorf("expected offset-preserving timestamp, got %q", string(out))
	}
	// Idempotent round-trip.
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse2: %v", err)
	}
	out2, err := Marshal(doc2, nil)
	if err != nil {
		t.Fatalf("Marshal2: %v", err)
	}
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out, out2)
	}
}

// TestTimestampSubSecondPrecisionRoundTrip — timestamps with fractional seconds
// (e.g. milliseconds, nanoseconds) must not lose their sub-second component on
// round-trip. Before the fix, encodeTime used time.RFC3339 which silently
// dropped nanoseconds, causing data loss for timestamps like
// `2025-12-13T10:30:00.5Z`.
func TestTimestampSubSecondPrecisionRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"nanoseconds", "---\ncreated: 2025-12-13T10:30:00.123456789Z\n---\nbody\n"},
		{"milliseconds", "---\ncreated: 2025-12-13T10:30:00.500Z\n---\nbody\n"},
		{"microseconds", "---\ncreated: 2025-12-13T10:30:00.000001Z\n---\nbody\n"},
		{"fractional with offset", "---\ncreated: 2025-12-13T10:30:00.5-05:00\n---\nbody\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			tm, ok := doc.Front.GetTime("created")
			if !ok {
				t.Fatalf("created should be time.Time")
			}
			if tm.Nanosecond() == 0 {
				t.Fatalf("expected non-zero nanoseconds after parse")
			}

			out, err := Marshal(doc, nil)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			// Re-parse and verify nanoseconds are preserved.
			doc2, err := Parse(out)
			if err != nil {
				t.Fatalf("Parse2: %v", err)
			}
			tm2, ok := doc2.Front.GetTime("created")
			if !ok {
				t.Fatalf("created should be time.Time after round-trip")
			}
			if tm.Nanosecond() != tm2.Nanosecond() {
				t.Errorf("nanoseconds lost: %d -> %d", tm.Nanosecond(), tm2.Nanosecond())
			}
			if !tm.Equal(tm2) {
				t.Errorf("timestamps differ: %v -> %v", tm, tm2)
			}

			// Idempotency.
			out2, err := Marshal(doc2, nil)
			if err != nil {
				t.Fatalf("Marshal2: %v", err)
			}
			if string(out) != string(out2) {
				t.Errorf("not idempotent:\n%q\n%q", out, out2)
			}
		})
	}
}

// TestTimestampNoSubSecondUnchanged — timestamps without fractional seconds
// must continue to emit the same format as before (no trailing `.0`).
func TestTimestampNoSubSecondUnchanged(t *testing.T) {
	in := "---\ncreated: 2025-12-13T10:30:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "2025-12-13T10:30:00Z") {
		t.Errorf("expected clean timestamp without fractional part, got %q", string(out))
	}
}

func TestTimestampUTCMidnightDateTimeDoesNotBecomeDateOnly(t *testing.T) {
	in := "---\ncreated: 2025-12-13T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := doc.Front.GetTime("created"); !ok {
		t.Fatalf("created should parse as time.Time")
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "created: 2025-12-13T00:00:00Z") {
		t.Fatalf("UTC midnight datetime was not preserved:\n%s", out)
	}
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse2: %v", err)
	}
	out2, err := Marshal(doc2, nil)
	if err != nil {
		t.Fatalf("Marshal2: %v", err)
	}
	if string(out) != string(out2) {
		t.Fatalf("not idempotent:\n%q\n%q", out, out2)
	}
}

func TestNestedTimestampUTCMidnightDateTimeDoesNotBecomeDateOnly(t *testing.T) {
	in := "---\nevents:\n  - at: 2025-12-13T00:00:00Z\nmeta:\n  created: 2025-12-13T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Count(string(out), "2025-12-13T00:00:00Z") != 2 {
		t.Fatalf("nested UTC midnight datetimes were not preserved:\n%s", out)
	}
	if strings.Contains(string(out), "created: 2025-12-13\n") {
		t.Fatalf("nested map timestamp collapsed to date-only:\n%s", out)
	}
	if strings.Contains(string(out), "at: 2025-12-13\n") {
		t.Fatalf("nested list timestamp collapsed to date-only:\n%s", out)
	}
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse2: %v", err)
	}
	out2, err := Marshal(doc2, nil)
	if err != nil {
		t.Fatalf("Marshal2: %v", err)
	}
	if string(out) != string(out2) {
		t.Fatalf("not idempotent:\n%q\n%q", out, out2)
	}
}

func TestTimestampMetadataFollowsRename(t *testing.T) {
	in := "---\ncreated: 2025-12-13T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := doc.Front.RenameKey("created", "updated"); err != nil {
		t.Fatalf("RenameKey: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "updated: 2025-12-13T00:00:00Z") {
		t.Fatalf("renamed timestamp lost datetime metadata:\n%s", out)
	}
}

func TestTimestampMetadataClearedBySetAndReplace(t *testing.T) {
	in := "---\ncreated: 2025-12-13T00:00:00Z\nreplaced: 2025-12-13T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	doc.Front.Set("created", time.Date(2025, 12, 14, 0, 0, 0, 0, time.UTC))

	old, ok := doc.Front.GetTime("replaced")
	if !ok {
		t.Fatalf("replaced should parse as time.Time")
	}
	if _, err := doc.Front.ReplaceValue("replaced", old, time.Date(2025, 12, 15, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "created: 2025-12-14\n") {
		t.Fatalf("Set should clear parsed datetime metadata and use date convention:\n%s", out)
	}
	if !strings.Contains(string(out), "replaced: 2025-12-15\n") {
		t.Fatalf("ReplaceValue should clear parsed datetime metadata and use date convention:\n%s", out)
	}
}
