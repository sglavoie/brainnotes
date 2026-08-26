package web

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestParseNoteFiltersParams(t *testing.T) {
	graph := query.BuildGraph(nil)
	params := url.Values{}
	params.Set("q", "  Needle  ")
	params.Add("tag", "foo")
	params.Add("tag", "bar")
	params.Add("category", "Books")
	params.Set("status", "active")
	params.Set("type", "project")
	params.Set("folder", "Work")
	params.Set("has", "status")
	params.Set("missing", "done")
	params.Set("age", ">30d")
	params.Set("created", "<7d")
	params.Set("orphan", "1")
	params.Set("broken", "true")
	params.Set("dead_end", "on")
	params.Set("min_backlinks", "2")
	params.Set("max_backlinks", "5")
	params.Set("min_links", "3")
	params.Set("max_links", "8")

	filters, err := parseNoteFilters(params, "/vault", graph, nil)
	if err != nil {
		t.Fatalf("parseNoteFilters: %v", err)
	}

	flags := filters.flags
	if filters.Query != "  Needle  " || filters.foldedQuery != "needle" {
		t.Fatalf("query text: got raw=%q folded=%q", filters.Query, filters.foldedQuery)
	}
	if got, want := flags.Tags, []string{"foo", "bar"}; !sameStrings(got, want) {
		t.Fatalf("tags: got %#v, want %#v", got, want)
	}
	if got, want := flags.Categories, []string{"Books"}; !sameStrings(got, want) {
		t.Fatalf("categories: got %#v, want %#v", got, want)
	}
	if flags.Status != "active" || flags.Type != "project" || flags.Folder != "Work" {
		t.Fatalf("basic fields not parsed: %#v", flags)
	}
	if flags.Has != "status" || flags.Missing != "done" || flags.Age != ">30d" || flags.Created != "<7d" {
		t.Fatalf("presence/age fields not parsed: %#v", flags)
	}
	if !flags.Orphan || !flags.BrokenLinks || !flags.DeadEnd {
		t.Fatalf("issue flags not parsed: %#v", flags)
	}
	if flags.MinBacklinks != 2 || flags.MaxBacklinks != 5 || flags.MinLinks != 3 || flags.MaxLinks != 8 {
		t.Fatalf("link counts not parsed: %#v", flags)
	}
	if flags.Root != "/vault" || flags.Graph != graph {
		t.Fatalf("root/graph not attached: root=%q graph=%p", flags.Root, flags.Graph)
	}
}

func TestParseNoteFiltersCountSentinels(t *testing.T) {
	filters, err := parseNoteFilters(url.Values{}, "", nil, nil)
	if err != nil {
		t.Fatalf("parseNoteFilters: %v", err)
	}

	flags := filters.flags
	if flags.MinBacklinks != -1 || flags.MaxBacklinks != -1 || flags.MinLinks != -1 || flags.MaxLinks != -1 {
		t.Fatalf("unset link counts should use -1 sentinels, got %#v", flags)
	}
}

func TestParseNoteFiltersInvalidParams(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params url.Values
	}{
		{
			name:   "bad integer",
			params: url.Values{"min_backlinks": {"many"}},
		},
		{
			name:   "negative count below sentinel",
			params: url.Values{"max_links": {"-2"}},
		},
		{
			name:   "inverted range",
			params: url.Values{"min_backlinks": {"5"}, "max_backlinks": {"2"}},
		},
		{
			name:   "bad age",
			params: url.Values{"age": {"old"}},
		},
		{
			name:   "bad boolean",
			params: url.Values{"orphan": {"maybe"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseNoteFilters(tc.params, "", query.BuildGraph(nil), nil); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

func TestMatchNoteWithFiltersANDSemantics(t *testing.T) {
	v, snapshot, notes := newFilterTestSnapshot(t, map[string]string{
		"Work/Match.md":      "---\ntags: [foo, bar]\ncategories:\n  - \"[[Books]]\"\nstatus: active\ntype: note\n---\nNeedle body.\n",
		"Work/MissingTag.md": "---\ntags: [foo]\ncategories:\n  - \"[[Books]]\"\nstatus: active\ntype: note\n---\nNeedle body.\n",
		"Work/NoText.md":     "---\ntags: [foo, bar]\ncategories:\n  - \"[[Books]]\"\nstatus: active\ntype: note\n---\nDifferent body.\n",
		"Other/Folder.md":    "---\ntags: [foo, bar]\ncategories:\n  - \"[[Books]]\"\nstatus: active\ntype: note\n---\nNeedle body.\n",
	})
	params := url.Values{}
	params.Set("q", "needle")
	params.Add("tag", "foo")
	params.Add("tag", "bar")
	params.Set("category", "books")
	params.Set("status", "active")
	params.Set("type", "note")
	params.Set("folder", "Work")
	params.Set("has", "status")
	params.Set("missing", "done")

	filters, err := parseNoteFilters(params, v.Root, snapshot.graph, nil)
	if err != nil {
		t.Fatalf("parseNoteFilters: %v", err)
	}

	assertMatch(t, filters, notes["Work/Match.md"], true)
	assertMatch(t, filters, notes["Work/MissingTag.md"], false)
	assertMatch(t, filters, notes["Work/NoText.md"], false)
	assertMatch(t, filters, notes["Other/Folder.md"], false)
}

func TestMatchNoteWithFiltersSupportsFilterOnlyQueries(t *testing.T) {
	v, snapshot, notes := newFilterTestSnapshot(t, map[string]string{
		"Match.md": "---\ntags: [foo]\n---\nBody.\n",
		"Other.md": "---\ntags: [bar]\n---\nBody.\n",
	})
	params := url.Values{"tag": {"foo"}}

	filters, err := parseNoteFilters(params, v.Root, snapshot.graph, nil)
	if err != nil {
		t.Fatalf("parseNoteFilters: %v", err)
	}

	assertMatch(t, filters, notes["Match.md"], true)
	assertMatch(t, filters, notes["Other.md"], false)
}

func TestParseNoteFiltersGraphRequiredFilters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params url.Values
	}{
		{name: "orphan", params: url.Values{"orphan": {"1"}}},
		{name: "broken", params: url.Values{"broken": {"1"}}},
		{name: "dead end", params: url.Values{"dead_end": {"1"}}},
		{name: "min backlinks", params: url.Values{"min_backlinks": {"1"}}},
		{name: "max backlinks", params: url.Values{"max_backlinks": {"0"}}},
		{name: "min links", params: url.Values{"min_links": {"1"}}},
		{name: "max links", params: url.Values{"max_links": {"0"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseNoteFilters(tc.params, "", nil, nil); err == nil {
				t.Fatal("expected graph-required filter to fail without graph")
			}
		})
	}
}

func TestMatchNoteWithFiltersGraphFilters(t *testing.T) {
	v, snapshot, notes := newFilterTestSnapshot(t, map[string]string{
		"Source.md":   "---\ntitle: Source\n---\n[[Target]]\n",
		"Target.md":   "---\ntitle: Target\n---\nNo outgoing links.\n",
		"Isolated.md": "---\ntitle: Isolated\n---\nNo graph edges.\n",
		"Broken.md":   "---\ntitle: Broken\n---\n[[Missing]]\n",
		"Exempt.md":   "---\ntitle: Exempt\ndead_end: false\n---\nNo graph edges.\n",
	})

	orphanDeadEnd, err := parseNoteFilters(url.Values{
		"orphan":   {"1"},
		"dead_end": {"1"},
	}, v.Root, snapshot.graph, nil)
	if err != nil {
		t.Fatalf("parse orphan/dead_end filters: %v", err)
	}

	assertMatch(t, orphanDeadEnd, notes["Isolated.md"], true)
	assertMatch(t, orphanDeadEnd, notes["Source.md"], false)
	assertMatch(t, orphanDeadEnd, notes["Target.md"], false)
	assertMatch(t, orphanDeadEnd, notes["Exempt.md"], false)

	maxLinksZero, err := parseNoteFilters(url.Values{"max_links": {"0"}}, v.Root, snapshot.graph, nil)
	if err != nil {
		t.Fatalf("parse max_links filter: %v", err)
	}
	assertMatch(t, maxLinksZero, notes["Exempt.md"], true)

	broken, err := parseNoteFilters(url.Values{"broken": {"1"}}, v.Root, snapshot.graph, nil)
	if err != nil {
		t.Fatalf("parse broken filter: %v", err)
	}
	assertMatch(t, broken, notes["Broken.md"], true)
	assertMatch(t, broken, notes["Source.md"], false)
}

func newFilterTestSnapshot(t *testing.T, files map[string]string) (*vault.Vault, vaultSnapshot, map[string]*vault.Note) {
	t.Helper()

	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	_, v := testVaultOpen(t, root, "127.0.0.1:0")
	loaded, err := v.Notes()
	if err != nil {
		t.Fatalf("notes: %v", err)
	}

	notesByRel := make(map[string]*vault.Note, len(loaded))
	for _, note := range loaded {
		rel, err := filepath.Rel(v.Root, note.Path)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		notesByRel[filepath.ToSlash(rel)] = note
	}

	snapshot := vaultSnapshot{
		notes:    loaded,
		graph:    query.BuildGraphForRoot(v.Root, loaded),
		loadedAt: time.Now(),
	}
	return v, snapshot, notesByRel
}

func assertMatch(t *testing.T, filters noteFilters, note *vault.Note, want bool) {
	t.Helper()

	_, got, err := matchNoteWithFilters(note, filters)
	if err != nil {
		t.Fatalf("matchNoteWithFilters: %v", err)
	}
	if got != want {
		t.Fatalf("matchNoteWithFilters(%q) = %v, want %v", note.Path, got, want)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestParseNoteFiltersExprComposesWithFacets(t *testing.T) {
	v, snapshot, notes := newFilterTestSnapshot(t, map[string]string{
		"Work/Both.md":     "---\ntags: [foo]\nstatus: wip\n---\nBody.\n",
		"Work/TagOnly.md":  "---\ntags: [foo]\nstatus: done\n---\nBody.\n",
		"Work/ExprOnly.md": "---\ntags: [bar]\nstatus: wip\n---\nBody.\n",
	})

	params := url.Values{"tag": {"foo"}, "expr": {"status:wip"}}
	filters, err := parseNoteFilters(params, v.Root, snapshot.graph, nil)
	if err != nil {
		t.Fatalf("parseNoteFilters: %v", err)
	}
	if filters.Expr != "status:wip" {
		t.Fatalf("expr text: got %q, want %q", filters.Expr, "status:wip")
	}
	if filters.exprPredicate == nil {
		t.Fatal("expected a compiled expression predicate")
	}

	assertMatch(t, filters, notes["Work/Both.md"], true)
	assertMatch(t, filters, notes["Work/TagOnly.md"], false)
	assertMatch(t, filters, notes["Work/ExprOnly.md"], false)
}

func TestNoteFiltersExprCountsAsNonTextFilter(t *testing.T) {
	graph := query.BuildGraph(nil)

	exprOnly, err := parseNoteFilters(url.Values{"expr": {"tag:foo"}}, "/vault", graph, nil)
	if err != nil {
		t.Fatalf("parseNoteFilters: %v", err)
	}
	if !exprOnly.hasNonTextFilters() {
		t.Fatal("an expression-only request must report non-text filters, or search and graph early-return an empty set")
	}

	blank, err := parseNoteFilters(url.Values{"expr": {"   "}}, "/vault", graph, nil)
	if err != nil {
		t.Fatalf("parseNoteFilters blank expr: %v", err)
	}
	if blank.Expr != "" || blank.exprPredicate != nil || blank.hasNonTextFilters() {
		t.Fatalf("a blank expression must be treated as absent, got %#v", blank.Expr)
	}
}

func TestNoteFiltersWithoutQueryKeepsExpr(t *testing.T) {
	v, snapshot, notes := newFilterTestSnapshot(t, map[string]string{
		"Keep.md": "---\nstatus: wip\n---\nNeedle body.\n",
		"Drop.md": "---\nstatus: done\n---\nNeedle body.\n",
	})

	filters, err := parseNoteFilters(url.Values{
		"q":    {"needle"},
		"expr": {"status:wip"},
	}, v.Root, snapshot.graph, nil)
	if err != nil {
		t.Fatalf("parseNoteFilters: %v", err)
	}

	focus := filters.withoutQuery()
	if focus.Expr != "status:wip" || focus.exprPredicate == nil {
		t.Fatalf("withoutQuery dropped the expression: %#v", focus.Expr)
	}
	if focus.foldedQuery != "" {
		t.Fatalf("withoutQuery kept the free-text query: %q", focus.foldedQuery)
	}
	if !focus.hasNonTextFilters() {
		t.Fatal("expression must still narrow the set in focus mode")
	}

	assertMatch(t, focus, notes["Keep.md"], true)
	assertMatch(t, focus, notes["Drop.md"], false)
}

func TestParseNoteFiltersInvalidExprIsTyped(t *testing.T) {
	graph := query.BuildGraph(nil)

	_, err := parseNoteFilters(url.Values{"expr": {"statuz:wip"}}, "/vault", graph, nil)
	if err == nil {
		t.Fatal("expected an unknown-field error")
	}

	var exprErr *exprFilterError
	if !errors.As(err, &exprErr) {
		t.Fatalf("error type: got %T, want *exprFilterError", err)
	}
	if exprErr.Expr != "statuz:wip" {
		t.Fatalf("expression text: got %q", exprErr.Expr)
	}

	var parseErr *queryexpr.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("wrapped error: got %T, want *queryexpr.ParseError", exprErr.Err)
	}

	payload := buildFilterErrorResponse(err)
	if payload.Error.Param != "expr" || payload.Error.Query != "statuz:wip" {
		t.Fatalf("payload identity: %#v", payload.Error)
	}
	if payload.Error.Position == nil || payload.Error.Position.Line != 1 || payload.Error.Position.Column != 1 {
		t.Fatalf("payload position: %#v", payload.Error.Position)
	}
	if !sameStrings(payload.Error.Suggestions, []string{"status"}) {
		t.Fatalf("payload suggestions: got %#v, want [status]", payload.Error.Suggestions)
	}
}

func TestBuildFilterErrorResponseNonExprError(t *testing.T) {
	_, err := parseNoteFilters(url.Values{"min_backlinks": {"many"}}, "", query.BuildGraph(nil), nil)
	if err == nil {
		t.Fatal("expected an integer parse error")
	}

	payload := buildFilterErrorResponse(err)
	if payload.Error.Param != "" || payload.Error.Position != nil {
		t.Fatalf("a non-expression filter error carries no caret payload: %#v", payload.Error)
	}
	if !strings.Contains(payload.Error.Message, "min_backlinks") {
		t.Fatalf("message should name the parameter: %q", payload.Error.Message)
	}
}

func TestParseErrorPositionMultiLineExpression(t *testing.T) {
	source := "tag:foo\nand statuz:wip"
	start := strings.Index(source, "statuz")
	err := queryexpr.NewParseError(source, queryexpr.Span{Start: start, End: start + len("statuz")}, "unknown field", nil)

	position := parseErrorPosition(err)
	if position.Line != 2 || position.Column != 5 {
		t.Fatalf("position: got line=%d column=%d, want line=2 column=5", position.Line, position.Column)
	}
	if position.Start != start || position.End != start+len("statuz") {
		t.Fatalf("span: got %d-%d, want %d-%d", position.Start, position.End, start, start+len("statuz"))
	}
}
