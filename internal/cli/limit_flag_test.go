package cli

import (
	"slices"
	"strings"
	"testing"
)

// --limit caps find after ordering, and replaces a view's stored limit.
func TestFindLimitCapsAfterOrdering(t *testing.T) {
	root := buildViewExecutionVault(t, `
[[views]]
name = "Capped"
query = "status:wip"
limit = 2
`)
	got := runFindOrdered(t, root, "find", "-q", "status:wip", "--sort", "-modified", "--limit", "1")
	if want := []string{"Notes/Delta.md"}; !slices.Equal(got, want) {
		t.Fatalf("find --sort -modified --limit 1: got=%v want=%v", got, want)
	}
	got = runFindOrdered(t, root, "find", "--view", "Capped", "--limit", "3")
	if want := []string{"Notes/Alpha.md", "Notes/Bravo.md", "Notes/Delta.md"}; !slices.Equal(got, want) {
		t.Fatalf("find --view Capped --limit 3: got=%v want=%v", got, want)
	}
	got = runFindOrdered(t, root, "find", "--view", "Capped", "--limit", "1")
	if want := []string{"Notes/Alpha.md"}; !slices.Equal(got, want) {
		t.Fatalf("find --view Capped --limit 1: got=%v want=%v", got, want)
	}
}

func TestLimitRejectsBelowOne(t *testing.T) {
	root := buildSearchVault(t)
	for _, args := range [][]string{
		{"find", "--limit", "0"},
		{"find", "--limit", "-2"},
		{"search", "keyword", "--limit", "0"},
	} {
		err := runFindSortError(t, root, args...)
		want := args[0] + ": --limit must be 1 or more (got " + args[len(args)-1] + ")"
		if err.Error() != want {
			t.Fatalf("bn %v: err = %q, want %q", args, err.Error(), want)
		}
	}
}

func TestSearchLimit(t *testing.T) {
	root := buildSearchVault(t)
	out, err := runSearch(t, root, "keyword", "--limit", "1")
	if err != nil {
		t.Fatalf("search --limit 1: %v", err)
	}
	if got := searchRels(t, root, out); !slices.Equal(got, []string{"Notes/Alpha.md"}) {
		t.Fatalf("search --limit 1 paths = %v", got)
	}
	out, err = runSearch(t, root, "keyword", "--count", "--limit", "1")
	if err != nil {
		t.Fatalf("search --count --limit 1: %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n") + 1; lines != 1 {
		t.Fatalf("search --count --limit 1 printed %d lines:\n%s", lines, out)
	}
	out, err = runSearch(t, root, "keyword", "--context", "0", "--limit", "1")
	if err != nil {
		t.Fatalf("search --context --limit 1: %v", err)
	}
	if strings.Contains(out, "Beta") {
		t.Fatalf("search --limit 1 printed a second note:\n%s", out)
	}
	for _, combo := range [][]string{{"-i"}, {"--open"}} {
		args := append([]string{"keyword", "--limit", "1"}, combo...)
		if _, err := runSearch(t, root, args...); err == nil || !strings.Contains(err.Error(), "--limit cannot be combined") {
			t.Fatalf("search %v: err = %v, want a combination refusal", args, err)
		}
	}
}
