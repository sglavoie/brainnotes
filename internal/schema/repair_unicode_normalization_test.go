package schema

import (
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// \u escapes rather than literals, so a normalizing editor cannot make the two
// spellings one and turn these tests into tautologies.
const (
	cafeComposed   = "Café"
	cafeDecomposed = "Café"
)

// TestPlanRepairs_CanonicalizesAcrossNormalizationForms is the bug: an allowed
// value stored decomposed was reported value_not_allowed. It must instead be
// canonicalized to the allowed list's own spelling, case-insensitively too.
func TestPlanRepairs_CanonicalizesAcrossNormalizationForms(t *testing.T) {
	if cafeComposed == cafeDecomposed {
		t.Fatal("fixtures are the same bytes; the test proves nothing")
	}
	sch := map[string]config.PropertyDefinition{
		"status": {Type: config.KindString, Allowed: []string{cafeComposed, "Done"}},
	}
	for _, stored := range []string{cafeDecomposed, "café", "CAFÉ"} {
		result := PlanRepairs(buildFront("status", stored), sch)
		if len(result.Skips) != 0 {
			t.Errorf("%q: unexpected skips: %+v", stored, result.Skips)
		}
		if len(result.Actions) != 1 {
			t.Fatalf("%q: expected 1 action, got %+v", stored, result.Actions)
		}
		if act := result.Actions[0]; act.Kind != "canonicalize" || act.NewValue != cafeComposed {
			t.Errorf("%q: action = %+v, want canonicalize to the allowed spelling", stored, act)
		}
	}
}

func TestPlanRepairs_CanonicalizesListItemsAcrossNormalizationForms(t *testing.T) {
	sch := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString, Allowed: []string{cafeComposed, "go"}},
	}
	result := PlanRepairs(buildFront("tags", []any{"go", cafeDecomposed}), sch)
	if len(result.Skips) != 0 {
		t.Errorf("unexpected skips: %+v", result.Skips)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %+v", result.Actions)
	}
	if got, want := result.Actions[0].NewValue, []any{"go", cafeComposed}; !reflect.DeepEqual(got, want) {
		t.Errorf("new value = %q, want %q", got, want)
	}
}

// TestDedupList_CollapsesNormalizationForms keeps the first spelling, whichever
// form it is, and still leaves non-string items to reflect.DeepEqual.
func TestDedupList_CollapsesNormalizationForms(t *testing.T) {
	got := dedupList([]any{cafeDecomposed, 1, cafeComposed, 1, "cafe"})
	want := []any{cafeDecomposed, 1, "cafe"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dedupList = %q, want %q", got, want)
	}
	got = dedupList([]any{cafeComposed, cafeDecomposed})
	if want := []any{cafeComposed}; !reflect.DeepEqual(got, want) {
		t.Errorf("dedupList = %q, want %q", got, want)
	}
}

// TestPlanRepairs_DedupsNormalizationFormsWithoutAllowedList covers an
// undeclared-allowed list: the NFC duplicate is a list_dedup, first spelling
// kept, rather than surviving as a visually identical second item.
func TestPlanRepairs_DedupsNormalizationFormsWithoutAllowedList(t *testing.T) {
	sch := map[string]config.PropertyDefinition{
		"tags": {Type: config.KindList, Items: config.KindString},
	}
	result := PlanRepairs(buildFront("tags", []any{cafeDecomposed, cafeComposed}), sch)
	if len(result.Actions) != 1 || result.Actions[0].Kind != "list_dedup" {
		t.Fatalf("expected one list_dedup action, got %+v", result.Actions)
	}
	if got, want := result.Actions[0].NewValue, []any{cafeDecomposed}; !reflect.DeepEqual(got, want) {
		t.Errorf("new value = %q, want %q", got, want)
	}
}
