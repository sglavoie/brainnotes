package frontmatter

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestListRemove_PreservesDatetimeFormatOfKeptItems verifies that removing one
// item from a list does not silently downgrade other midnight-UTC datetime
// items to date-only on the next Marshal. Before the fix, ListRemove cleared
// all meta under the key, losing the per-index `timestampDateOnly: false`
// records that distinguish a parsed `2025-12-13T00:00:00Z` literal from a
// parsed `2025-12-13` literal — both decode to the same time.Time.
func TestListRemove_PreservesDatetimeFormatOfKeptItems(t *testing.T) {
	in := "---\ndates:\n  - 2025-12-13T00:00:00Z\n  - 2025-12-14\n  - 2025-12-15T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lst, _ := doc.Front.GetList("dates")
	if _, err := doc.Front.ListRemove("dates", lst[1]); err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "- 2025-12-13T00:00:00Z") {
		t.Errorf("first item lost full-datetime format:\n%s", got)
	}
	if !strings.Contains(got, "- 2025-12-15T00:00:00Z") {
		t.Errorf("third item lost full-datetime format:\n%s", got)
	}
	if strings.Contains(got, "- 2025-12-14") {
		t.Errorf("removed item still present:\n%s", got)
	}

	// Idempotency.
	doc2, _ := Parse(out)
	out2, _ := Marshal(doc2, nil)
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out, out2)
	}
}

// TestReplaceValue_PreservesDatetimeFormatOfKeptListItems mirrors the above
// for ReplaceValue: changing one item must not strip metadata from the others.
func TestReplaceValue_PreservesDatetimeFormatOfKeptListItems(t *testing.T) {
	in := "---\ndates:\n  - 2025-12-13T00:00:00Z\n  - 2025-12-14\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lst, _ := doc.Front.GetList("dates")
	// Replace the second (date-only) item with a different midnight UTC time.
	newVal := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	if _, err := doc.Front.ReplaceValue("dates", lst[1], newVal); err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "- 2025-12-13T00:00:00Z") {
		t.Errorf("first item lost full-datetime format:\n%s", got)
	}
	// The replaced item gets fresh metadata (none) and was set via mutation, so
	// it should emit as date-only per the documented Set/ReplaceValue contract.
	if !strings.Contains(got, "- 2026-06-06") {
		t.Errorf("replaced item not present in date-only form:\n%s", got)
	}
	if strings.Contains(got, "- 2025-12-14") {
		t.Errorf("old replaced value still present:\n%s", got)
	}
}

// TestReplaceValue_ListReplacedIndicesGetFreshMeta verifies that replacing a
// datetime-meta item gives the replacement value default formatting (no
// inherited date-only/datetime metadata from the prior value).
func TestReplaceValue_ListReplacedIndicesGetFreshMeta(t *testing.T) {
	// Original: index 0 is full datetime, index 1 is date-only.
	in := "---\ndates:\n  - 2025-12-13T00:00:00Z\n  - 2025-12-14\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lst, _ := doc.Front.GetList("dates")
	// Replace the full-datetime item (index 0) with a fresh midnight UTC time.
	// The replacement should emit as date-only (Set-style fresh meta), while
	// index 1 (kept) should retain its date-only format too.
	newVal := time.Date(2027, 7, 7, 0, 0, 0, 0, time.UTC)
	if _, err := doc.Front.ReplaceValue("dates", lst[0], newVal); err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	out, _ := Marshal(doc, nil)
	got := string(out)
	if !strings.Contains(got, "- 2027-07-07") || strings.Contains(got, "2027-07-07T") {
		t.Errorf("replaced item should be date-only:\n%s", got)
	}
	if !strings.Contains(got, "- 2025-12-14") {
		t.Errorf("kept item should still be date-only:\n%s", got)
	}
}

// TestListRemove_NestedMetaCleared verifies that removing a list item whose
// value was a nested map drops the nested-meta entries (otherwise they'd
// linger and could leak format hints to future items that happen to land at
// the same numeric index).
func TestListRemove_NestedMetaCleared(t *testing.T) {
	in := "---\nitems:\n  - at: 2025-12-13T00:00:00Z\n  - at: 2025-12-14T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lst, _ := doc.Front.GetList("items")
	if _, err := doc.Front.ListRemove("items", lst[0]); err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "2025-12-13") {
		t.Errorf("removed item should be gone:\n%s", got)
	}
	if !strings.Contains(got, "at: 2025-12-14T00:00:00Z") {
		t.Errorf("kept nested datetime lost its format:\n%s", got)
	}

	// Idempotency.
	doc2, _ := Parse(out)
	out2, _ := Marshal(doc2, nil)
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%s\n%s", out, out2)
	}
}

// TestListRemove_RemapPreservesAllKeptIndices is a stronger version: a
// 4-element list with mixed formats has the middle two removed; the
// surviving items at new indices 0 and 1 keep their formats.
func TestListRemove_RemapPreservesAllKeptIndices(t *testing.T) {
	in := "---\ndates:\n  - 2025-12-13T00:00:00Z\n  - 2025-12-14\n  - 2025-12-15\n  - 2025-12-16T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lst, _ := doc.Front.GetList("dates")
	if _, err := doc.Front.ListRemove("dates", lst[1], lst[2]); err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	out, _ := Marshal(doc, nil)
	got := string(out)
	if !strings.Contains(got, "- 2025-12-13T00:00:00Z") {
		t.Errorf("first item lost datetime format:\n%s", got)
	}
	if !strings.Contains(got, "- 2025-12-16T00:00:00Z") {
		t.Errorf("last item lost datetime format:\n%s", got)
	}
	doc2, _ := Parse(out)
	lst2, _ := doc2.Front.GetList("dates")
	if len(lst2) != 2 {
		t.Fatalf("expected 2 items, got %d", len(lst2))
	}
}

// TestReplaceValue_TopLevelScalarDoesNotAffectOtherKeys guards against the
// scalar-replacement path accidentally widening its meta clear to other keys.
func TestReplaceValue_TopLevelScalarDoesNotAffectOtherKeys(t *testing.T) {
	in := "---\nalpha: 2025-12-13T00:00:00Z\nbeta: 2025-12-14T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	old, _ := doc.Front.GetTime("beta")
	newVal := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	if _, err := doc.Front.ReplaceValue("beta", old, newVal); err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	out, _ := Marshal(doc, nil)
	got := string(out)
	if !strings.Contains(got, "alpha: 2025-12-13T00:00:00Z") {
		t.Errorf("alpha should keep its full-datetime format:\n%s", got)
	}
	if !strings.Contains(got, "beta: 2026-06-06\n") {
		t.Errorf("beta should be date-only (fresh meta):\n%s", got)
	}
}

// TestRemapListIndexMeta_RoundTripIdempotent makes sure that after a ListRemove
// the document is fully stable across additional parse/marshal cycles.
func TestRemapListIndexMeta_RoundTripIdempotent(t *testing.T) {
	in := "---\ndates:\n  - 2025-12-13T00:00:00Z\n  - 2025-12-14\n  - 2025-12-15T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lst, _ := doc.Front.GetList("dates")
	if _, err := doc.Front.ListRemove("dates", lst[0]); err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	out1, _ := Marshal(doc, nil)
	doc2, _ := Parse(out1)
	out2, _ := Marshal(doc2, nil)
	if string(out1) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out1, out2)
	}
	// Sanity: indices remapped correctly, no spurious extra items.
	lst2, _ := doc2.Front.GetList("dates")
	if len(lst2) != 2 {
		t.Fatalf("expected 2 items, got %d: %v", len(lst2), lst2)
	}
}

// TestParseIndexPart_RejectsNonIndexSegments locks down the helper that
// distinguishes list-index meta segments from nested-map segments. A regression
// here would cause remapListIndexMeta to either drop unrelated nested-map meta
// or attempt to renumber non-list segments.
func TestParseIndexPart_RejectsNonIndexSegments(t *testing.T) {
	cases := map[string]struct {
		n  int
		ok bool
	}{
		"i0":     {0, true},
		"i42":    {42, true},
		"m4:key": {0, false},
		"i":      {0, false},
		"ix":     {0, false},
		"":       {0, false},
		"-1":     {0, false},
	}
	for in, want := range cases {
		n, ok := parseIndexPart(in)
		if ok != want.ok || (ok && n != want.n) {
			t.Errorf("parseIndexPart(%q): got (%d, %v) want (%d, %v)", in, n, ok, want.n, want.ok)
		}
	}
}

// TestSplitMetaTail covers the helper that splits a meta key tail at the
// first NUL byte.
func TestSplitMetaTail(t *testing.T) {
	cases := []struct {
		in         string
		head, rest string
	}{
		{"i0", "i0", ""},
		{"i0\x00m1:x", "i0", "m1:x"},
		{"i0\x00m1:x\x00i1", "i0", "m1:x\x00i1"},
		{"", "", ""},
	}
	for _, c := range cases {
		h, r := splitMetaTail(c.in)
		if h != c.head || r != c.rest {
			t.Errorf("splitMetaTail(%q): got (%q, %q) want (%q, %q)", c.in, h, r, c.head, c.rest)
		}
	}
}

// TestListRemove_KeepsListUnchangedWhenNothingMatches ensures the no-op path
// does not touch meta.
func TestListRemove_KeepsListUnchangedWhenNothingMatches(t *testing.T) {
	in := "---\ndates:\n  - 2025-12-13T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := doc.Front.ListRemove("dates", "not present"); err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	out, _ := Marshal(doc, nil)
	if !strings.Contains(string(out), "2025-12-13T00:00:00Z") {
		t.Errorf("no-op remove altered datetime format:\n%s", out)
	}
}

// TestReplaceValue_KeepsListUnchangedWhenNothingMatches ensures the no-op path
// does not touch meta.
func TestReplaceValue_KeepsListUnchangedWhenNothingMatches(t *testing.T) {
	in := "---\ndates:\n  - 2025-12-13T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := doc.Front.ReplaceValue("dates", "absent", "ignored"); err != nil {
		t.Fatalf("ReplaceValue: %v", err)
	}
	out, _ := Marshal(doc, nil)
	if !strings.Contains(string(out), "2025-12-13T00:00:00Z") {
		t.Errorf("no-op replace altered datetime format:\n%s", out)
	}
}

// TestRemapListIndexMeta_HandlesSparseRemove covers the wrap-around case where
// a removal in the middle shifts higher indices down. The kept items at new
// indices must reflect the original meta from the OLD indices, not their new
// numeric position.
func TestRemapListIndexMeta_HandlesSparseRemove(t *testing.T) {
	// Original: index 0 date-only, index 1 date-only, index 2 datetime.
	in := "---\ndates:\n  - 2025-12-13\n  - 2025-12-14\n  - 2025-12-15T00:00:00Z\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lst, _ := doc.Front.GetList("dates")
	// Remove index 1 (date-only). Index 2 (datetime) should move to new index 1
	// AND keep its datetime format.
	if _, err := doc.Front.ListRemove("dates", lst[1]); err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	out, _ := Marshal(doc, nil)
	got := string(out)
	if !strings.Contains(got, "- 2025-12-13\n") {
		t.Errorf("first kept item should stay date-only:\n%s", got)
	}
	if !strings.Contains(got, "- 2025-12-15T00:00:00Z") {
		t.Errorf("third item (shifted to index 1) should keep datetime format:\n%s", got)
	}
	// Idempotent.
	doc2, _ := Parse(out)
	lst2, _ := doc2.Front.GetList("dates")
	if !reflect.DeepEqual([]int{lst2[0].(time.Time).Year(), lst2[1].(time.Time).Year()}, []int{2025, 2025}) {
		t.Errorf("years: %v", lst2)
	}
}
