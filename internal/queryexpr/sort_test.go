package queryexpr

import (
	"sort"
	"strings"
	"testing"
)

func TestParseSort(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  SortSpec
		valid bool
	}{
		// The default. An unset --sort is the path ordering, spelled out
		// rather than left as the empty key so a caller never has to know that
		// "" and "path" mean the same thing.
		{name: "empty is path", in: "", want: SortSpec{Key: SortPath}, valid: true},
		{name: "blank is path", in: "   ", want: SortSpec{Key: SortPath}, valid: true},

		// Every key in the vocabulary.
		{name: "path", in: "path", want: SortSpec{Key: SortPath}, valid: true},
		{name: "title", in: "title", want: SortSpec{Key: SortTitle}, valid: true},
		{name: "modified", in: "modified", want: SortSpec{Key: SortModified}, valid: true},
		{name: "created", in: "created", want: SortSpec{Key: SortCreated}, valid: true},
		{name: "backlinks", in: "backlinks", want: SortSpec{Key: SortBacklinks}, valid: true},
		{name: "degree", in: "degree", want: SortSpec{Key: SortDegree}, valid: true},
		{name: "pinned", in: "pinned", want: SortSpec{Key: SortPinned}, valid: true},
		{name: "relevance", in: "relevance", want: SortSpec{Key: SortRelevance}, valid: true},

		// Direction by sign, the sort(1) habit.
		{name: "sign descending", in: "-modified", want: SortSpec{Key: SortModified, Descending: true}, valid: true},
		{name: "sign ascending", in: "+modified", want: SortSpec{Key: SortModified}, valid: true},
		{name: "sign on the default key", in: "-path", want: SortSpec{Key: SortPath, Descending: true}, valid: true},

		// Direction by suffix, the query-language habit.
		{name: "suffix desc", in: "modified:desc", want: SortSpec{Key: SortModified, Descending: true}, valid: true},
		{name: "suffix descending", in: "modified:descending", want: SortSpec{Key: SortModified, Descending: true}, valid: true},
		{name: "suffix asc", in: "title:asc", want: SortSpec{Key: SortTitle}, valid: true},
		{name: "suffix ascending", in: "title:ascending", want: SortSpec{Key: SortTitle}, valid: true},

		// Case and space are not part of the value.
		{name: "upper case key", in: "MODIFIED", want: SortSpec{Key: SortModified}, valid: true},
		{name: "mixed case suffix", in: "Title:DESC", want: SortSpec{Key: SortTitle, Descending: true}, valid: true},
		{name: "surrounding space", in: "  -degree  ", want: SortSpec{Key: SortDegree, Descending: true}, valid: true},
		{name: "space around the suffix", in: "degree: desc ", want: SortSpec{Key: SortDegree, Descending: true}, valid: true},

		// RelativeTo is never parsed from --sort: it names a note, so it is its
		// own flag. `relevance` parses like any other key and leaves it empty.
		{name: "relevance descending leaves RelativeTo empty", in: "-relevance", want: SortSpec{Key: SortRelevance, Descending: true}, valid: true},

		// Rejections.
		{name: "unknown key", in: "sizee"},
		{name: "unknown key that is a query field", in: "tag"},
		{name: "unknown direction", in: "modified:sideways"},
		{name: "empty direction", in: "modified:"},
		{name: "sign with no key", in: "-"},
		{name: "sign and suffix together", in: "-modified:desc"},
		// Even when the two spellings agree, giving the direction twice is an
		// error rather than a precedence rule to remember.
		{name: "sign and agreeing suffix", in: "+modified:asc"},
		{name: "key only after a colon", in: ":desc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSort(tc.in)
			if !tc.valid {
				if err == nil {
					t.Fatalf("ParseSort(%q) = %+v, want an error", tc.in, got)
				}
				if got != (SortSpec{}) {
					t.Fatalf("ParseSort(%q) returned %+v alongside its error, want the zero spec", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSort(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseSort(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseSort_ErrorNamesTheVocabulary pins that an unknown key tells the user
// what the alternatives are. `bn find --sort` is typed from memory, so the
// error is the documentation.
func TestParseSort_ErrorNamesTheVocabulary(t *testing.T) {
	_, err := ParseSort("backlinkz")
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	for _, key := range SortKeys() {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not mention the valid key %q", err, key)
		}
	}
}

// TestSortSpecStringRoundTrips pins that the rendering ParseSort accepts is the
// one SortSpec.String produces, which is what lets a saved view store an
// ordering as text.
func TestSortSpecStringRoundTrips(t *testing.T) {
	for _, key := range append(SortKeys(), "") {
		for _, desc := range []bool{false, true} {
			spec := SortSpec{Key: key, Descending: desc}
			round, err := ParseSort(spec.String())
			if err != nil {
				t.Fatalf("ParseSort(%q): %v", spec.String(), err)
			}
			want := spec
			if want.Key == "" {
				want.Key = SortPath
			}
			if round != want {
				t.Fatalf("round trip of %+v via %q gave %+v", spec, spec.String(), round)
			}
		}
	}
}

// TestSortSpecStringDropsRelativeTo pins that the anchor is not folded into the
// --sort spelling. It is a separate flag, so including it would produce a
// string ParseSort could not read back.
func TestSortSpecStringDropsRelativeTo(t *testing.T) {
	spec := SortSpec{Key: SortRelevance, RelativeTo: "Areas/Deep Work.md"}
	if got := spec.String(); got != "relevance" {
		t.Fatalf("String() = %q, want %q", got, "relevance")
	}
}

// TestSortKeysIsSortedAndComplete pins the vocabulary itself. ValidSortKey uses
// a binary search, so the slice being sorted is a correctness requirement, not
// a cosmetic one.
func TestSortKeysIsSortedAndComplete(t *testing.T) {
	keys := SortKeys()
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("SortKeys() is not sorted: %v", keys)
	}
	want := []string{"backlinks", "created", "degree", "modified", "path", "pinned", "relevance", "title"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("SortKeys() = %v, want %v", keys, want)
	}
	for _, key := range keys {
		if !ValidSortKey(key) {
			t.Errorf("ValidSortKey(%q) = false", key)
		}
	}
	if !ValidSortKey("") {
		t.Error(`ValidSortKey("") = false, want true: the empty key is the default path ordering`)
	}
	if ValidSortKey("nope") {
		t.Error(`ValidSortKey("nope") = true`)
	}

	// SortKeys returns a copy: a caller sorting or truncating the result must
	// not be able to corrupt the binary search behind ValidSortKey.
	keys[0] = "zzz"
	if !ValidSortKey("backlinks") {
		t.Error("mutating the SortKeys() result changed ValidSortKey")
	}
}

// TestSortNotes_DistinguishesUnknownFromUnordered pins the queryexpr-never-ranks
// boundary. A key ParseSort would reject is a bad spec; a *valid* key the
// in-memory evaluator does not order by is the caller's job, and the two errors
// say so differently so a caller can tell which mistake it made.
func TestSortNotes_DistinguishesUnknownFromUnordered(t *testing.T) {
	unknown := sortNotes(nil, SortSpec{Key: "nope"})
	if unknown == nil || !strings.Contains(unknown.Error(), "unknown sort key") {
		t.Fatalf("sortNotes with an unknown key gave %v", unknown)
	}
	for _, key := range []string{SortTitle, SortModified, SortCreated, SortBacklinks, SortDegree, SortPinned, SortRelevance} {
		err := sortNotes(nil, SortSpec{Key: key})
		if err == nil {
			t.Fatalf("sortNotes(%q) succeeded; the evaluator must not silently ignore an ordering", key)
		}
		if strings.Contains(err.Error(), "unknown sort key") {
			t.Errorf("sortNotes(%q) reported a valid key as unknown: %v", key, err)
		}
	}
	if err := sortNotes(nil, SortSpec{Key: SortPath}); err != nil {
		t.Errorf("sortNotes(path): %v", err)
	}
	if err := sortNotes(nil, SortSpec{}); err != nil {
		t.Errorf("sortNotes(default): %v", err)
	}
}
