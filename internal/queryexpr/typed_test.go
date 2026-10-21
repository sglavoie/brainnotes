package queryexpr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// pinnedNow is the clock every typed test runs against, so a relative literal
// such as "+30d" has one answer rather than one per day the suite is run.
var pinnedNow = time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)

// typedVault builds a vault declaring one property of every PropertyKind, plus
// undeclared properties for the fallback path.
//
// Notes:
//
//	A — every declared property, populated
//	B — every declared property, populated with the other end of each range
//	C — no properties at all, so "does a missing value match?" has an answer
//	D — undeclared properties, plus a declared int written as a string
//	E — state: gamma, which its own `allowed` list forbids
func typedVault(t *testing.T) (string, []*vault.Note, Env) {
	t.Helper()

	files := map[string]string{
		"A.md": "---\n" +
			"state: alpha\n" +
			"parent: \"[[Deep Work]]\"\n" +
			"due: 2026-05-19\n" +
			"reviewed: 2026-05-19T10:30:00Z\n" +
			"rating: 4\n" +
			"score: 2.5\n" +
			"draft: true\n" +
			"topics: [go, rust]\n" +
			"areas: [\"[[Areas/Focus.md|Focus]]\"]\n" +
			"---\nA.\n",
		"B.md": "---\n" +
			"state: beta\n" +
			"parent: \"[[Shallow Work]]\"\n" +
			"due: 2026-01-01\n" +
			"reviewed: 2026-01-01T00:00:00Z\n" +
			"rating: 2\n" +
			"score: 7.25\n" +
			"draft: false\n" +
			"topics: [web]\n" +
			"areas: [\"[[Other]]\"]\n" +
			"---\nB.\n",
		"C.md": "---\ntitle: C\n---\nC has none of them.\n",
		"D.md": "---\n" +
			// An accented value written decomposed: the query spells it
			// composed and uppercased, and folding has to bridge both.
			"state: \"café\"\n" +
			// A declared int written as a string, which schema.TryCoerce
			// salvages rather than skipping.
			"rating: \"5\"\n" +
			// Undeclared properties, reachable only through prop:.
			"custom: 9\n" +
			"tally: 3.5\n" +
			"note_date: 2026-03-03\n" +
			"free: Some Text\n" +
			"kind: \"[[Reference/Books.md|Reading]]\"\n" +
			"---\nD.\n",
		"E.md": "---\nstate: gamma\n---\nE violates its own allow-list.\n",
	}

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
	v, err := vault.Open(&config.Config{Vault: root})
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("load notes: %v", err)
	}

	env := Env{
		Root: v.Root,
		Frontmatter: &config.FrontmatterConfig{Properties: map[string]config.PropertyDefinition{
			// `allowed` is declared here precisely so a test can prove queries
			// ignore it.
			"state":    {Type: config.KindString, Allowed: []string{"alpha", "beta"}},
			"parent":   {Type: config.KindWikilink},
			"due":      {Type: config.KindDate},
			"reviewed": {Type: config.KindDatetime},
			"rating":   {Type: config.KindInt},
			"score":    {Type: config.KindFloat},
			"draft":    {Type: config.KindBool},
			"topics":   {Type: config.KindList, Items: config.KindString},
			"areas":    {Type: config.KindList, Items: config.KindWikilink},
		}},
		Now: func() time.Time { return pinnedNow },
	}
	return v.Root, notes, env
}

// TestTyped_ComparisonPerKind walks one comparison of every declared kind,
// including the literal spellings each kind accepts.
func TestTyped_ComparisonPerKind(t *testing.T) {
	root, notes, env := typedVault(t)

	cases := []struct {
		name string
		src  string
		want []string
	}{
		// string — fold-equal, not substring.
		{"string match", "state:alpha", []string{"A.md"}},
		{"string equals", "state=beta", []string{"B.md"}},
		{"string is not a prefix search", "state:alph", nil},
		{"string folds case and unicode form", "state=CAFÉ", []string{"D.md"}},
		// `!=` negates the whole predicate, so a note with no value matches.
		{"string not-equal includes notes without the property", "state!=alpha",
			[]string{"B.md", "C.md", "D.md", "E.md"}},

		// wikilink — compared by resolved title key, either side bracketed.
		{"wikilink value form", "parent:[[Deep Work]]", []string{"A.md"}},
		{"wikilink bare literal", "parent:\"deep work\"", []string{"A.md"}},
		{"wikilink equals", "parent=[[Shallow Work]]", []string{"B.md"}},

		// bool.
		{"bool true", "draft:true", []string{"A.md"}},
		{"bool false", "draft=false", []string{"B.md"}},

		// int — and the string-stored 5 that TryCoerce salvages.
		{"int greater", "rating>3", []string{"A.md", "D.md"}},
		{"int equal", "rating:4", []string{"A.md"}},
		{"int at least", "rating>=5", []string{"D.md"}},
		{"int less", "rating<4", []string{"B.md"}},

		// float.
		{"float at least", "score>=2.5", []string{"A.md", "B.md"}},
		{"float less", "score<3", []string{"A.md"}},

		// date — calendar-day compare, absolute and relative literals.
		{"date absolute", "due:2026-05-19", []string{"A.md"}},
		{"date today", "due:today", []string{"A.md"}},
		{"date after a past offset", "due>\"-7d\"", []string{"A.md"}},
		{"date before a future offset", "due<+30d", []string{"A.md", "B.md"}},
		{"date at or before", "due<=2026-01-01", []string{"B.md"}},

		// datetime — instant compare.
		{"datetime after a date", "reviewed>2026-05-01", []string{"A.md"}},
		{"datetime before now", "reviewed<now", []string{"A.md", "B.md"}},
		// An RFC3339 literal has to be quoted: its own ":" separators would
		// otherwise end the word and be read as the argument operator.
		{"datetime exact rfc3339", "reviewed=\"2026-05-19T10:30:00Z\"", []string{"A.md"}},
		{"datetime relative hours", "reviewed>\"-2h\"", []string{"A.md"}},
		{"datetime relative hours excludes", "reviewed>\"-1h\"", nil},

		// list — `:` is contains, elements parsed as def.Items.
		{"list contains", "topics:go", []string{"A.md"}},
		{"list any-of", "topics:[go, web]", []string{"A.md", "B.md"}},
		{"list of wikilink resolves the element", "areas:[[Focus]]", []string{"A.md"}},
		{"list miss", "topics:elixir", nil},

		// Composition with the rest of the language still works.
		{"conjunction with a reserved field", "rating>3 and state:alpha", []string{"A.md"}},
		{"negation", "not draft:true", []string{"B.md", "C.md", "D.md", "E.md"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchPaths(t, tc.src, root, notes, env)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("%q matched %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

// TestTyped_OperatorLegalityPerKind is the table the plan specifies: which
// operators each declared type defines, checked in both directions.
func TestTyped_OperatorLegalityPerKind(t *testing.T) {
	_, _, env := typedVault(t)

	equality := []Op{OpMatch, OpEq, OpNe}
	ordering := []Op{OpLt, OpLe, OpGt, OpGe}
	all := append(append([]Op{}, equality...), ordering...)

	cases := []struct {
		property string
		literal  string
		legal    []Op
	}{
		{"state", "alpha", equality},
		{"parent", "[[Deep Work]]", equality},
		{"draft", "true", equality},
		{"due", "2026-05-19", all},
		{"reviewed", "\"2026-05-19T10:30:00Z\"", all},
		{"rating", "4", all},
		{"score", "2.5", all},
		// A list is contains-only: `:` and nothing else.
		{"topics", "go", []Op{OpMatch}},
		{"areas", "[[Focus]]", []Op{OpMatch}},
	}

	for _, tc := range cases {
		t.Run(tc.property, func(t *testing.T) {
			legal := make(map[Op]bool, len(tc.legal))
			for _, op := range tc.legal {
				legal[op] = true
			}
			for _, op := range all {
				src := tc.property + string(op) + tc.literal
				_, err := Compile(src, env)
				if legal[op] {
					if err != nil {
						t.Errorf("Compile(%q) = %v, want it to compile", src, err)
					}
					continue
				}
				perr, ok := err.(*ParseError)
				if !ok {
					t.Errorf("Compile(%q) error = %v (%T), want *ParseError", src, err, err)
					continue
				}
				if !strings.Contains(perr.Msg, "is not defined for") {
					t.Errorf("Compile(%q) message = %q, want an operator-legality message", src, perr.Msg)
				}
			}
		})
	}
}

// TestTyped_OrderingUnorderedKindErrorNamesTheDeclaration pins the exact
// diagnostic the plan specifies, including the column it points at.
func TestTyped_OrderingUnorderedKindErrorNamesTheDeclaration(t *testing.T) {
	env := Env{Frontmatter: &config.FrontmatterConfig{Properties: map[string]config.PropertyDefinition{
		"rating": {Type: config.KindString},
		"topics": {Type: config.KindList, Items: config.KindString},
	}}}

	cases := []struct {
		src     string
		wantMsg string
	}{
		{
			"rating>3",
			`operator ">" is not defined for string property "rating" (declared type = "string" in brainnotes.toml)`,
		},
		{
			"topics=go",
			`operator "=" is not defined for list property "topics" (declared type = "list of string" in brainnotes.toml)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			_, err := Compile(tc.src, env)
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Compile(%q) error = %v (%T), want *ParseError", tc.src, err, err)
			}
			if perr.Msg != tc.wantMsg {
				t.Errorf("message  = %q\nwant     = %q", perr.Msg, tc.wantMsg)
			}
			if perr.Column != 1 {
				t.Errorf("column = %d, want 1", perr.Column)
			}
			// A correctly spelled property must never be offered as a typo.
			if len(perr.Suggest) != 0 {
				t.Errorf("suggestions = %v, want none", perr.Suggest)
			}
		})
	}

	// The column tracks the term, not the query.
	_, err := Compile("tag:x and rating>3", env)
	perr, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("error = %v (%T), want *ParseError", err, err)
	}
	if perr.Column != 11 {
		t.Errorf("column = %d, want 11", perr.Column)
	}
}

// TestTyped_LiteralParseErrors covers the literal grammar of each kind,
// including the plan's rule that a float literal on an int property is a
// compile error rather than a silent truncation.
func TestTyped_LiteralParseErrors(t *testing.T) {
	_, _, env := typedVault(t)

	cases := []struct {
		src     string
		wantMsg string
	}{
		{"rating>3.5", `property "rating" is declared int, so "3.5" must be a whole number`},
		{"rating:many", `property "rating" is declared int, so it expects a whole number`},
		{"score>lots", `property "score" is declared float, so it expects a number`},
		{"draft:maybe", `property "draft" is declared bool, so it expects true or false`},
		{"due>someday", `property "due" is declared date`},
		{"due>2026-13-45", `property "due" is declared date`},
		{"reviewed>whenever", `property "reviewed" is declared datetime`},
		// Ordering compares one value against one bound, never against a list.
		{"rating>[1, 2]", `property "rating" takes a single value with ">", not a list`},
		{"rating(4)", `property "rating" takes no argument`},
	}

	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			_, err := Compile(tc.src, env)
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Compile(%q) error = %v (%T), want *ParseError", tc.src, err, err)
			}
			if !strings.Contains(perr.Msg, tc.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", perr.Msg, tc.wantMsg)
			}
			if perr.Column < 1 {
				t.Errorf("column = %d, want a 1-based column", perr.Column)
			}
		})
	}
}
