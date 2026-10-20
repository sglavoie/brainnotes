package cockpit

import (
	"reflect"
	"testing"
)

// TestEmptyHubHeadingsReadsATXHeadingsPerCommonMark covers the matcher's two
// CommonMark corrections: a title ending in `#` keeps it, so `## C#` is not the
// heading `## C`, and a tab-indented heading is indented code, so it neither
// counts as a heading nor ends the section above it.
func TestEmptyHubHeadingsReadsATXHeadingsPerCommonMark(t *testing.T) {
	body := "## C\n\nfilled\n\n## C#\n\n"
	if got, want := emptyHubHeadings(body, []string{"C", "C#"}), []string{"C#"}; !reflect.DeepEqual(got, want) {
		t.Errorf("emptyHubHeadings = %v, want %v", got, want)
	}

	tabbed := "## Synthesis\n\t## Synthesis\n"
	if got := emptyHubHeadings(tabbed, []string{"Synthesis"}); len(got) != 0 {
		t.Errorf("a tab-indented heading is code, so the section is filled: got %v", got)
	}
}
