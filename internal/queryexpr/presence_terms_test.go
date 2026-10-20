package queryexpr

import "testing"

// TestPresenceTerms pins the lowering behind the CLI's repeatable --has and
// --missing: one term per non-blank value, has before missing, in order.
func TestPresenceTerms(t *testing.T) {
	got := (&And{Operands: PresenceTerms([]string{"a", " ", "b"}, []string{"c"})}).String()
	if want := "has:a and has:b and missing:c"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if terms := PresenceTerms(nil, []string{""}); len(terms) != 0 {
		t.Fatalf("blank values produced %d terms", len(terms))
	}
}
