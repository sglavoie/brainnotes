package query

import (
	"testing"
)

// TestAndOrDeeplyNested pins down that combinators compose to arbitrary depth
// without losing short-circuit behaviour. The previous combinator tests cover
// one level of nesting; this exercises three levels.
func TestAndOrDeeplyNested(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\nstatus: Reading\ntype: Book\n---\n",
		"b.md": "---\ntags: [bar]\nstatus: Reading\ntype: Article\n---\n",
		"c.md": "---\ntags: [foo]\nstatus: Done\ntype: Book\n---\n",
	})
	// ((tag=foo AND status=Reading) OR (tag=bar AND type=Article)) AND has=status
	p := And(
		Or(
			And(Tag("foo"), Status("Reading")),
			And(Tag("bar"), Type("Article")),
		),
		Has("status"),
	)
	cases := map[string]bool{
		"a.md": true,  // foo+Reading
		"b.md": true,  // bar+Article
		"c.md": false, // foo+Done, type=Book — neither inner AND matches
	}
	for path, want := range cases {
		if got := p(notes[path]); got != want {
			t.Errorf("deeply nested predicate on %s = %v, want %v", path, got, want)
		}
	}
}

// TestParseAgeNumericOverflowSeconds locks down the overflow guard for
// large second-unit values. Hours and days overflow at much smaller integers
// than seconds, but the guard must apply uniformly.
