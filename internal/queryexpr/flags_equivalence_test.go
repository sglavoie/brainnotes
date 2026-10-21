package queryexpr

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// This file is the deliverable of the flags bridge, not a supplement to it.
//
// query.FromFlags and queryexpr.FromFlags are two lowerings of one Flags
// struct, and internal/query cannot import internal/queryexpr to share the
// first with the second — queryexpr imports query, so the reverse edge is a
// cycle. The accepted cost is drift, and the only thing that makes it
// acceptable is this table: every Flags value below is run through both paths
// against the same fixture vault, and the two must select identical sorted path
// lists or agree that the value is rejected.
//
// The round trip is through *text* on purpose. It is not enough that
// FromFlags builds an equivalent AST; the AST is rendered with String() and
// recompiled, so the test also pins that every flag value survives quoting,
// lexing, and re-parsing. A flag carrying a space, a keyword, or a wikilink is
// therefore covered by construction rather than by a separate round-trip suite.

// baseFlags is a Flags with every link bound at its "not set" sentinel. The Go
// zero value would activate MaxBacklinks and MaxLinks (internal/query/parse.go:14-19),
// so every case starts here and overrides only what it means to test.
func baseFlags() query.Flags {
	return query.Flags{MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1}
}

// queryPaths selects through query.FromFlags.
func queryPaths(root string, notes []*vault.Note, f query.Flags) ([]string, error) {
	pred, err := query.FromFlags(f)
	if err != nil {
		return nil, err
	}
	return selectPaths(root, notes, pred), nil
}

// exprPaths selects through queryexpr.FromFlags, going all the way out to
// canonical text and back so the guard covers rendering as well as lowering.
//
// The Env is built from the same Flags fields query.FromFlags reads off the
// struct, and the graph is passed through rather than built: a case that
// withholds the graph must fail on this path too, just later — at
// Plan.Predicate rather than at FromFlags.
func exprPaths(root string, notes []*vault.Note, f query.Flags, env Env) ([]string, string, error) {
	expr, err := FromFlags(f)
	if err != nil {
		return nil, "", err
	}
	src := expr.String()
	env.Root = f.Root
	env.Graph = f.Graph
	plan, err := Compile(src, env)
	if err != nil {
		return nil, src, err
	}
	pred, err := plan.Predicate(env)
	if err != nil {
		return nil, src, err
	}
	return selectPaths(root, notes, pred), src, nil
}

// selectPaths returns the sorted vault-relative paths a predicate matches.
func selectPaths(root string, notes []*vault.Note, pred query.Predicate) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		if pred(n) {
			rel, err := filepath.Rel(root, n.Path)
			if err != nil {
				rel = n.Path
			}
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

func TestFromFlags_MatchesQueryFromFlags(t *testing.T) {
	root, notes, env := fixtureVault(t)
	g := query.BuildGraphForRoot(root, notes)

	// withGraph returns f with the link graph attached, for the cases whose
	// flags read link structure.
	withGraph := func(f query.Flags) query.Flags {
		f.Graph = g
		return f
	}
	tagged := func(mutate func(*query.Flags)) query.Flags {
		f := baseFlags()
		mutate(&f)
		return f
	}

	cases := []struct {
		name string
		// wantErr marks a Flags value both paths must reject. The paths reject
		// it at different calls — query.FromFlags immediately, the expression
		// path at Compile or Plan.Predicate — so the assertion is that neither
		// produces a result, not that the errors read alike.
		wantErr bool
		flags   query.Flags
	}{
		// --- the identity and the trivial terms -------------------------------
		{name: "empty flags match everything", flags: baseFlags()},
		{name: "single tag", flags: tagged(func(f *query.Flags) { f.Tags = []string{"alpha"} })},
		{name: "two tags are an AND", flags: tagged(func(f *query.Flags) { f.Tags = []string{"alpha", "beta"} })},
		{name: "blank tag entries are skipped", flags: tagged(func(f *query.Flags) { f.Tags = []string{"alpha", "   ", ""} })},
		{name: "tag matching nothing", flags: tagged(func(f *query.Flags) { f.Tags = []string{"nosuchtag"} })},
		{name: "category", flags: tagged(func(f *query.Flags) { f.Categories = []string{"Books"} })},
		{name: "category written as a wikilink", flags: tagged(func(f *query.Flags) { f.Categories = []string{"[[Books]]"} })},
		{name: "status", flags: tagged(func(f *query.Flags) { f.Status = "wip" })},
		{name: "status is trimmed", flags: tagged(func(f *query.Flags) { f.Status = "  done  " })},
		{name: "status is case-insensitive", flags: tagged(func(f *query.Flags) { f.Status = "WIP" })},
		{name: "type", flags: tagged(func(f *query.Flags) { f.Type = "note" })},
		{name: "has", flags: tagged(func(f *query.Flags) { f.Has = "rating" })},
		{name: "missing", flags: tagged(func(f *query.Flags) { f.Missing = "rating" })},
		{name: "pinned", flags: tagged(func(f *query.Flags) { f.Pinned = true })},

		// --- values that only survive the text round trip when quoted ---------
		{name: "status spelled as a keyword", flags: tagged(func(f *query.Flags) { f.Status = "and" })},
		{name: "tag containing a space", flags: tagged(func(f *query.Flags) { f.Tags = []string{"deep work"} })},
		{name: "tag containing punctuation", flags: tagged(func(f *query.Flags) { f.Tags = []string{"a:b,c(d)"} })},
		{name: "tag starting with a hyphen", flags: tagged(func(f *query.Flags) { f.Tags = []string{"-alpha"} })},

		// --- age ---------------------------------------------------------------
		{name: "age older than a day", flags: tagged(func(f *query.Flags) { f.Age = ">1d" })},
		{name: "age newer than a day", flags: tagged(func(f *query.Flags) { f.Age = "<1d" })},
		{name: "age in hours", flags: tagged(func(f *query.Flags) { f.Age = ">48h" })},
		{name: "age is trimmed", flags: tagged(func(f *query.Flags) { f.Age = "  >1d  " })},
		{name: "created reads its own property", flags: tagged(func(f *query.Flags) { f.Created = ">1d" })},
		{name: "age and created together", flags: tagged(func(f *query.Flags) { f.Age = ">1d"; f.Created = "<9999d" })},
		{name: "malformed age", wantErr: true, flags: tagged(func(f *query.Flags) { f.Age = "bogus" })},
		{name: "age with an unsupported operator", wantErr: true, flags: tagged(func(f *query.Flags) { f.Age = ">=1d" })},
		{name: "age with a bad unit", wantErr: true, flags: tagged(func(f *query.Flags) { f.Created = ">5y" })},

		// --- folder -------------------------------------------------------------
		{name: "folder with a root", flags: tagged(func(f *query.Flags) { f.Folder = "Archive"; f.Root = root })},
		{name: "folder matching nothing", flags: tagged(func(f *query.Flags) { f.Folder = "Nope"; f.Root = root })},
		{name: "folder without a root", wantErr: true, flags: tagged(func(f *query.Flags) { f.Folder = "Archive" })},

		// --- graph-reading flags -------------------------------------------------
		{name: "orphan", flags: withGraph(tagged(func(f *query.Flags) { f.Orphan = true }))},
		{name: "orphan without a graph", wantErr: true, flags: tagged(func(f *query.Flags) { f.Orphan = true })},
		{name: "dead-end", flags: withGraph(tagged(func(f *query.Flags) { f.DeadEnd = true }))},
		{name: "broken-links", flags: withGraph(tagged(func(f *query.Flags) { f.BrokenLinks = true }))},
		{name: "broken-links without a graph", wantErr: true, flags: tagged(func(f *query.Flags) { f.BrokenLinks = true })},

		// --- the asymmetric activation rules -------------------------------------
		// MinBacklinks > 0 vs MaxBacklinks >= 0: a minimum of 0 filters nothing
		// and stays inactive (so it needs no graph), while a maximum of 0 is the
		// meaningful "nothing links here" filter and does activate.
		{name: "min-backlinks 1", flags: withGraph(tagged(func(f *query.Flags) { f.MinBacklinks = 1 }))},
		{name: "min-backlinks 0 is inactive and needs no graph", flags: tagged(func(f *query.Flags) { f.MinBacklinks = 0 })},
		{name: "max-backlinks 0", flags: withGraph(tagged(func(f *query.Flags) { f.MaxBacklinks = 0 }))},
		{name: "max-backlinks 0 without a graph", wantErr: true, flags: tagged(func(f *query.Flags) { f.MaxBacklinks = 0 })},
		{name: "max-backlinks 1", flags: withGraph(tagged(func(f *query.Flags) { f.MaxBacklinks = 1 }))},
		{name: "min-links 1", flags: withGraph(tagged(func(f *query.Flags) { f.MinLinks = 1 }))},
		{name: "min-links 0 is inactive and needs no graph", flags: tagged(func(f *query.Flags) { f.MinLinks = 0 })},
		{name: "max-links 0", flags: withGraph(tagged(func(f *query.Flags) { f.MaxLinks = 0 }))},
		{name: "backlinks range", flags: withGraph(tagged(func(f *query.Flags) { f.MinBacklinks = 1; f.MaxBacklinks = 2 }))},
		{name: "links range", flags: withGraph(tagged(func(f *query.Flags) { f.MinLinks = 1; f.MaxLinks = 2 }))},
		{name: "zero-value Flags activates both maxima", wantErr: true, flags: query.Flags{}},

		// --- bound validation ------------------------------------------------------
		{name: "min-backlinks below the sentinel", wantErr: true, flags: tagged(func(f *query.Flags) { f.MinBacklinks = -2 })},
		{name: "max-links below the sentinel", wantErr: true, flags: tagged(func(f *query.Flags) { f.MaxLinks = -3 })},
		{name: "min-backlinks above max-backlinks", wantErr: true, flags: withGraph(tagged(func(f *query.Flags) { f.MinBacklinks = 5; f.MaxBacklinks = 2 }))},
		{name: "min-links above max-links", wantErr: true, flags: withGraph(tagged(func(f *query.Flags) { f.MinLinks = 5; f.MaxLinks = 2 }))},

		// --- combinations ------------------------------------------------------------
		{name: "tag and status", flags: tagged(func(f *query.Flags) { f.Tags = []string{"alpha"}; f.Status = "done" })},
		{name: "tag and folder and graph bound", flags: withGraph(tagged(func(f *query.Flags) {
			f.Tags = []string{"beta"}
			f.Folder = "Archive"
			f.Root = root
			f.MaxBacklinks = 0
		}))},
		{name: "everything at once", flags: withGraph(tagged(func(f *query.Flags) {
			f.Tags = []string{"alpha"}
			f.Categories = []string{"Books"}
			f.Status = "done"
			f.Age = "<9999d"
			f.Folder = ""
			f.Root = root
			f.MinBacklinks = 1
			f.MaxLinks = 3
		}))},
		{name: "contradiction selects nothing", flags: tagged(func(f *query.Flags) { f.Status = "wip"; f.Type = "note"; f.Tags = []string{"beta"} })},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, wantErr := queryPaths(root, notes, tc.flags)
			got, src, gotErr := exprPaths(root, notes, tc.flags, env)

			if tc.wantErr {
				if wantErr == nil {
					t.Fatalf("query.FromFlags accepted a value the case expects rejected (selected %v)", want)
				}
				if gotErr == nil {
					t.Fatalf("queryexpr path accepted %q, which query.FromFlags rejects with: %v", src, wantErr)
				}
				return
			}
			if wantErr != nil {
				t.Fatalf("query.FromFlags: %v", wantErr)
			}
			if gotErr != nil {
				t.Fatalf("queryexpr path on %q: %v", src, gotErr)
			}
			if !equalPaths(want, got) {
				t.Fatalf("selection differs for %q:\n query.FromFlags: %v\n queryexpr:       %v", src, want, got)
			}
		})
	}
}

// equalPaths compares two sorted path lists, treating nil and empty alike: a
// predicate that matches nothing yields one or the other depending on which
// path built it, and that is not a difference in selection.
func equalPaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFromFlags_RendersCanonicalSource pins the surface spelling each flag
// lowers onto. The equivalence table proves the two paths agree; this proves
// they agree on the query a user would have had to type, which is what makes
// `bn find --orphan` and `bn find -q 'orphan:true'` one feature rather than two.
func TestFromFlags_RendersCanonicalSource(t *testing.T) {
	cases := []struct {
		name  string
		flags query.Flags
		want  string
	}{
		{"empty", baseFlags(), ""},
		{"tag", func() query.Flags { f := baseFlags(); f.Tags = []string{"alpha"}; return f }(), "tag:alpha"},
		{"tags AND", func() query.Flags { f := baseFlags(); f.Tags = []string{"a", "b"}; return f }(), "tag:a and tag:b"},
		{"category", func() query.Flags { f := baseFlags(); f.Categories = []string{"Books"}; return f }(), "category:Books"},
		{"status", func() query.Flags { f := baseFlags(); f.Status = "wip"; return f }(), "status:wip"},
		{"type", func() query.Flags { f := baseFlags(); f.Type = "note"; return f }(), "type:note"},
		{"has", func() query.Flags { f := baseFlags(); f.Has = "rating"; return f }(), "has:rating"},
		{"missing", func() query.Flags { f := baseFlags(); f.Missing = "rating"; return f }(), "missing:rating"},
		// --age uses age's own default property, so it renders without an
		// argument; --created names the property explicitly.
		{"age", func() query.Flags { f := baseFlags(); f.Age = ">90d"; return f }(), "age>90d"},
		{"created", func() query.Flags { f := baseFlags(); f.Created = "<7d"; return f }(), "age:created<7d"},
		{"pinned", func() query.Flags { f := baseFlags(); f.Pinned = true; return f }(), "pinned:true"},
		{"folder", func() query.Flags { f := baseFlags(); f.Folder = "Archive"; f.Root = "/v"; return f }(), "folder:Archive"},
		{"orphan", func() query.Flags { f := baseFlags(); f.Orphan = true; return f }(), "orphan:true"},
		{"dead-end", func() query.Flags { f := baseFlags(); f.DeadEnd = true; return f }(), "dead-end:true"},
		{"broken-links", func() query.Flags { f := baseFlags(); f.BrokenLinks = true; return f }(), "broken-links:true"},
		// The bounds are inclusive on both sides of the bridge, so they render
		// as >= and <= rather than as an off-by-one over > and <.
		{"min-backlinks", func() query.Flags { f := baseFlags(); f.MinBacklinks = 3; return f }(), "backlinks>=3"},
		{"max-backlinks", func() query.Flags { f := baseFlags(); f.MaxBacklinks = 0; return f }(), "backlinks<=0"},
		{"min-links", func() query.Flags { f := baseFlags(); f.MinLinks = 2; return f }(), "links>=2"},
		{"max-links", func() query.Flags { f := baseFlags(); f.MaxLinks = 4; return f }(), "links<=4"},
		{"backlinks range", func() query.Flags { f := baseFlags(); f.MinBacklinks = 1; f.MaxBacklinks = 4; return f }(), "backlinks>=1 and backlinks<=4"},
		// A minimum of 0 is inactive; a maximum of 0 is not. This is the
		// asymmetry the bridge exists to preserve.
		{"min 0 emits nothing", func() query.Flags { f := baseFlags(); f.MinBacklinks = 0; f.MinLinks = 0; return f }(), ""},
		// Quoting is Atom.String's job, so a keyword or a spaced value comes
		// back out in a form the lexer reads identically.
		{"keyword value is quoted", func() query.Flags { f := baseFlags(); f.Status = "not"; return f }(), `status:"not"`},
		{"spaced value is quoted", func() query.Flags { f := baseFlags(); f.Tags = []string{"deep work"}; return f }(), `tag:"deep work"`},
		{"leading hyphen is quoted", func() query.Flags { f := baseFlags(); f.Type = "-x"; return f }(), `type:"-x"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := FromFlags(tc.flags)
			if err != nil {
				t.Fatalf("FromFlags: %v", err)
			}
			if got := expr.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
			// Every rendering must re-lex, or the equivalence table is testing
			// a query the user could not have typed.
			if _, err := Parse(expr.String()); err != nil {
				t.Fatalf("re-parsing %q: %v", expr.String(), err)
			}
		})
	}
}

// TestFromFlags_AlwaysReturnsAnd pins the identity convention: nothing active
// is a zero-operand And, the same shape query.And() with no arguments has.
func TestFromFlags_AlwaysReturnsAnd(t *testing.T) {
	expr, err := FromFlags(baseFlags())
	if err != nil {
		t.Fatalf("FromFlags: %v", err)
	}
	and, ok := expr.(*And)
	if !ok {
		t.Fatalf("FromFlags returned %T, want *And", expr)
	}
	if len(and.Operands) != 0 {
		t.Fatalf("empty flags produced %d operands, want 0", len(and.Operands))
	}
}
