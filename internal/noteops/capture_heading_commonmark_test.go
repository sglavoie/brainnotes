package noteops

import "testing"

// TestCaptureATXHeadingFollowsCommonMark checks the matcher against the ATX
// heading examples of the CommonMark spec (0.31, examples 62-79), plus the two
// cases that motivated the fix: a title ending in `#` and tab indentation.
func TestCaptureATXHeadingFollowsCommonMark(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line    string
		heading bool
		level   int
		title   string
	}{
		{"## Notes on C#", true, 2, "Notes on C#"},
		{"\t# foo", false, 0, ""},
		{"# foo", true, 1, "foo"},
		{"###### foo", true, 6, "foo"},
		{"####### foo", false, 0, ""},
		{"#5 bolt", false, 0, ""},
		{"#hashtag", false, 0, ""},
		{"\\## foo", false, 0, ""},
		{"#                  foo                     ", true, 1, "foo"},
		{" ### foo", true, 3, "foo"},
		{"   # foo", true, 1, "foo"},
		{"    # foo", false, 0, ""},
		{"## foo ##", true, 2, "foo"},
		{"  ###   bar    ###", true, 3, "bar"},
		{"# foo ##################################", true, 1, "foo"},
		{"##### foo ##", true, 5, "foo"},
		{"### foo ###     ", true, 3, "foo"},
		{"### foo ### b", true, 3, "foo ### b"},
		{"# foo#", true, 1, "foo#"},
		{"### foo \\###", true, 3, "foo \\###"},
		{"## foo #\\##", true, 2, "foo #\\##"},
		{"# foo \\#", true, 1, "foo \\#"},
		{"## ", true, 2, ""},
		{"#", true, 1, ""},
		{"### ###", true, 3, ""},
		{"## #", true, 2, ""},
		{"## Captures\r", true, 2, "Captures"},
		{"## Café ##\r", true, 2, "Café"},
	}
	for _, tc := range tests {
		m := captureATXHeading.FindStringSubmatch(tc.line)
		if (m != nil) != tc.heading {
			t.Errorf("%q: heading = %v, want %v", tc.line, m != nil, tc.heading)
			continue
		}
		if m == nil {
			continue
		}
		if len(m[1]) != tc.level || m[2] != tc.title {
			t.Errorf("%q: level %d title %q, want level %d title %q", tc.line, len(m[1]), m[2], tc.level, tc.title)
		}
	}
}

// TestPlanCaptureInsertAnchorsHeadingEndingInHash is the user-visible bug: a
// configured `## Notes on C#` used to parse as `## Notes on C`, so capture
// anchored on (or created) the wrong section.
func TestPlanCaptureInsertAnchorsHeadingEndingInHash(t *testing.T) {
	t.Parallel()
	body := "## Notes on C\n\n- wrong\n\n## Notes on C#\n\n- right\n"
	got, line, created, err := planCaptureInsert(body, "## Notes on C#", "- new")
	if err != nil {
		t.Fatalf("planCaptureInsert: %v", err)
	}
	want := "## Notes on C\n\n- wrong\n\n## Notes on C#\n\n- right\n- new\n"
	if created || got != want || line != 8 {
		t.Errorf("planCaptureInsert = (%q, %d, %v), want (%q, 8, false)", got, line, created, want)
	}
	if n := CountSectionBullets(body, "## Notes on C"); n != 1 {
		t.Errorf("CountSectionBullets(## Notes on C) = %d, want 1", n)
	}
}

// TestCaptureIgnoresTabIndentedHeading: a tab-indented `## Inbox` is indented
// code, so it neither anchors capture nor ends a section.
func TestCaptureIgnoresTabIndentedHeading(t *testing.T) {
	t.Parallel()
	body := "## Inbox\n\n- one\n\t## Inbox\n- two\n"
	if n := CountSectionBullets(body, "## Inbox"); n != 2 {
		t.Errorf("CountSectionBullets = %d, want 2 (the tab-indented line is code, not a heading)", n)
	}
}
