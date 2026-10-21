package query

import (
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestAndOr(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo, bar]\nstatus: Reading\n---\n",
		"b.md": "---\ntags: [foo]\nstatus: Completed\n---\n",
	})
	combined := And(Tag("foo"), Status("Reading"))
	if !combined(notes["a.md"]) {
		t.Errorf("And: a should match foo+Reading")
	}
	if combined(notes["b.md"]) {
		t.Errorf("And: b should not match foo+Reading")
	}

	either := Or(Tag("bar"), Status("Completed"))
	if !either(notes["a.md"]) || !either(notes["b.md"]) {
		t.Errorf("Or should match both notes")
	}

	// And with no terms = match everything
	all := And()
	for _, n := range notes {
		if !all(n) {
			t.Errorf("And() should match every note")
		}
	}
	if all(nil) {
		t.Errorf("And() should reject nil note")
	}
}

// TestOrEmpty pins down the documented behaviour of Or with no terms: it is the
// identity element for logical OR (false), and it must still reject a nil note
// for consistency with And.
func TestOrEmpty(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\n---\n",
	})
	none := Or()
	if none(notes["a.md"]) {
		t.Errorf("Or() with no terms should match nothing (identity is false)")
	}
	if none(nil) {
		t.Errorf("Or() should reject nil note")
	}
}

// TestAndShortCircuits verifies that And stops calling predicates after the
// first failure. Without short-circuiting, a query with an expensive late
// predicate would always pay its cost even when an early cheap predicate has
// already excluded the note.
func TestAndShortCircuits(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\n---\n",
	})
	calls := 0
	count := Predicate(func(n *vault.Note) bool {
		calls++
		return true
	})
	fail := Predicate(func(n *vault.Note) bool { return false })

	if And(fail, count)(notes["a.md"]) {
		t.Errorf("And(fail, count) should be false")
	}
	if calls != 0 {
		t.Errorf("And should short-circuit on first false; count called %d times, want 0", calls)
	}
}

// TestOrShortCircuits verifies that Or stops calling predicates after the
// first success.
func TestOrShortCircuits(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\n---\n",
	})
	calls := 0
	count := Predicate(func(n *vault.Note) bool {
		calls++
		return true
	})
	pass := Predicate(func(n *vault.Note) bool { return true })

	if !Or(pass, count)(notes["a.md"]) {
		t.Errorf("Or(pass, count) should be true")
	}
	if calls != 0 {
		t.Errorf("Or should short-circuit on first true; count called %d times, want 0", calls)
	}
}

// TestAndOrSkipNilPredicates verifies the documented behaviour that nil
// entries in the predicate list are silently skipped rather than panicking.
// This matters because FromFlags may compose lists where a builder returned
// nil to mean "no predicate for this flag".
func TestAndOrSkipNilPredicates(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\n---\n",
	})
	if !And(nil, Tag("foo"), nil)(notes["a.md"]) {
		t.Errorf("And should skip nil predicates and still match")
	}
	if Or(nil, nil)(notes["a.md"]) {
		t.Errorf("Or with only nil predicates should match nothing")
	}
	if !Or(nil, Tag("foo"))(notes["a.md"]) {
		t.Errorf("Or with a nil mixed in should still consider the non-nil predicate")
	}
}

// TestAndOrCompositionNested verifies that combinators compose recursively:
// And(Or(A, B), C) should work as expected.
func TestAndOrCompositionNested(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\nstatus: Reading\n---\n",
		"b.md": "---\ntags: [bar]\nstatus: Reading\n---\n",
		"c.md": "---\ntags: [baz]\nstatus: Reading\n---\n",
		"d.md": "---\ntags: [foo]\nstatus: Done\n---\n",
	})
	// (tag=foo OR tag=bar) AND status=Reading
	p := And(Or(Tag("foo"), Tag("bar")), Status("Reading"))
	cases := map[string]bool{
		"a.md": true,  // foo + Reading
		"b.md": true,  // bar + Reading
		"c.md": false, // baz + Reading: missing tag
		"d.md": false, // foo + Done: wrong status
	}
	for path, want := range cases {
		if got := p(notes[path]); got != want {
			t.Errorf("(foo OR bar) AND Reading on %s = %v, want %v", path, got, want)
		}
	}
}
