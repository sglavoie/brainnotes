package queryexpr

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// TestTyped_AllowedListIsNotEnforced is the point of the whole rule: you have
// to be able to find the notes that violate your own schema. `state` declares
// allowed = [alpha, beta], and E.md holds gamma.
func TestTyped_AllowedListIsNotEnforced(t *testing.T) {
	root, notes, env := typedVault(t)

	def, ok := env.Frontmatter.Definition("state")
	if !ok || len(def.Allowed) == 0 {
		t.Fatalf("fixture must declare an allow-list on state, got %+v", def)
	}

	// A value outside the allow-list compiles rather than being rejected...
	if _, err := Compile("state:gamma", env); err != nil {
		t.Fatalf("Compile(%q) = %v, want an off-allow-list value to compile", "state:gamma", err)
	}
	// ...and finds exactly the note that violates the schema.
	got := matchPaths(t, "state:gamma", root, notes, env)
	if strings.Join(got, ",") != "E.md" {
		t.Errorf("state:gamma matched %v, want [E.md]", got)
	}
	// The same holds for a list property's elements.
	if _, err := Compile("topics:anything-at-all", env); err != nil {
		t.Fatalf("Compile on a list property with an off-list element = %v, want nil", err)
	}
}

// TestTyped_UndeclaredPropertyFallback pins the prop: escape hatch: equality
// goes through the same stringify-and-fold path Tag and Status use, and
// ordering requires the stored value to really be a number or a time.
func TestTyped_UndeclaredPropertyFallback(t *testing.T) {
	root, notes, env := typedVault(t)

	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"string equality", `prop:free="Some Text"`, []string{"D.md"}},
		{"string equality folds case", "prop:free=\"some text\"", []string{"D.md"}},
		{"stringified int compares as text", "prop:custom=9", []string{"D.md"}},
		{"stringified float keeps its decimal marker", "prop:tally=3.5", []string{"D.md"}},
		{"a stored wikilink compares by title", "prop:kind=[[Books]]", []string{"D.md"}},
		{"not-equal", "prop:free!=nothing", []string{"A.md", "B.md", "C.md", "D.md", "E.md"}},

		{"int ordering", "prop:custom>5", []string{"D.md"}},
		{"int ordering at the bound", "prop:custom>=9", []string{"D.md"}},
		{"int ordering excludes the bound", "prop:custom>9", nil},
		{"float ordering", "prop:tally>3", []string{"D.md"}},
		{"date ordering", "prop:note_date>2026-01-01", []string{"D.md"}},
		{"date ordering the other way", "prop:note_date<2026-01-01", nil},

		// The plan's rule: ordering skips a note whose stored value is not an
		// int, a float64, or a time.Time. free is a string, so D is skipped
		// rather than compared as text.
		{"ordering skips a non-orderable stored value", "prop:free>3", nil},
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

// TestTyped_PropReachesDeclaredAndShadowedProperties pins that prop: is a way
// to *name* a key, not a way to bypass its declaration.
func TestTyped_PropReachesDeclaredAndShadowedProperties(t *testing.T) {
	root, notes, env := typedVault(t)

	// A declared property named through prop: still takes the typed path, so
	// the declared-type operator legality still applies.
	_, err := Compile("prop:state>alpha", env)
	perr, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("error = %v (%T), want the declared string property to reject \">\"", err, err)
	}
	if !strings.Contains(perr.Msg, `is not defined for string property "state"`) {
		t.Errorf("message = %q, want the typed operator-legality error", perr.Msg)
	}

	// ...and it still evaluates as the typed comparison does.
	if got := matchPaths(t, "prop:state=alpha", root, notes, env); strings.Join(got, ",") != "A.md" {
		t.Errorf("prop:state=alpha matched %v, want [A.md]", got)
	}

	// The parenthesised argument form is the same term.
	if got := matchPaths(t, "prop(custom)>5", root, notes, env); strings.Join(got, ",") != "D.md" {
		t.Errorf("prop(custom)>5 matched %v, want [D.md]", got)
	}
}

func TestTyped_PropErrors(t *testing.T) {
	_, _, env := typedVault(t)

	cases := []struct {
		src     string
		wantMsg string
	}{
		{"prop:rating", `field "prop" needs a property name`},
		{"prop:custom>sometime-ish", `is not declared in brainnotes.toml`},
		{"prop:custom>sometime-ish", "declare a type for predictable ordering"},
	}
	for _, tc := range cases {
		t.Run(tc.src+" "+tc.wantMsg, func(t *testing.T) {
			_, err := Compile(tc.src, env)
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Compile(%q) error = %v (%T), want *ParseError", tc.src, err, err)
			}
			if !strings.Contains(perr.Msg, tc.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", perr.Msg, tc.wantMsg)
			}
		})
	}
}

// TestTyped_RelativeLiteralsHonourEnvNow pins Env.Now as the clock relative
// literals resolve against — its first consumer, and what lets a date query be
// tested without depending on the day the suite runs.
func TestTyped_RelativeLiteralsHonourEnvNow(t *testing.T) {
	root, notes, env := typedVault(t)

	// On 2026-05-19, A.md's due date is today.
	if got := matchPaths(t, "due:today", root, notes, env); strings.Join(got, ",") != "A.md" {
		t.Fatalf("due:today at %s matched %v, want [A.md]", pinnedNow.Format("2006-01-02"), got)
	}

	// Move the clock a day forward and the same query matches nothing, without
	// the plan or the notes changing.
	tomorrow := env
	tomorrow.Now = func() time.Time { return pinnedNow.AddDate(0, 0, 1) }
	if got := matchPaths(t, "due:today", root, notes, tomorrow); len(got) != 0 {
		t.Errorf("due:today a day later matched %v, want nothing", got)
	}
	// ...while "yesterday" — a -1d offset — now finds it.
	if got := matchPaths(t, "due:\"-1d\"", root, notes, tomorrow); strings.Join(got, ",") != "A.md" {
		t.Errorf(`due:"-1d" a day later matched %v, want [A.md]`, got)
	}

	// A plan compiled once evaluates against whichever clock it is given, since
	// Predicate re-walks the expression rather than caching the literal.
	plan, err := Compile("due:today", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	first, err := plan.Predicate(env)
	if err != nil {
		t.Fatalf("predicate: %v", err)
	}
	second, err := plan.Predicate(tomorrow)
	if err != nil {
		t.Fatalf("predicate: %v", err)
	}
	var a *vault.Note
	for _, n := range notes {
		if filepath.Base(n.Path) == "A.md" {
			a = n
		}
	}
	if a == nil {
		t.Fatal("fixture is missing A.md")
	}
	if !first(a) || second(a) {
		t.Errorf("one plan, two clocks: got %v and %v, want true then false", first(a), second(a))
	}
}

// TestTyped_MissingPropertyNeverMatchesAComparison pins the asymmetry `!=`
// depends on: the comparison itself never matches a note that has no value, and
// it is the negation wrapped around it that lets `!=` do so.
func TestTyped_MissingPropertyNeverMatchesAComparison(t *testing.T) {
	root, notes, env := typedVault(t)

	for _, src := range []string{"rating>0", "rating<1000", "state:anything", "due<+9999d"} {
		got := matchPaths(t, src, root, notes, env)
		for _, path := range got {
			if path == "C.md" {
				t.Errorf("%q matched C.md, which has no such property", src)
			}
		}
	}
	if got := matchPaths(t, "rating!=4", root, notes, env); !slices.Contains(got, "C.md") {
		t.Errorf("rating!=4 matched %v, want it to include C.md", got)
	}
}
