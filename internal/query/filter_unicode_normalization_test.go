package query

import "testing"

// A frontmatter value written in decomposed form (as macOS tools often write
// it) must match the composed spelling a user types, and the reverse, for every
// value predicate the flag surface exposes.
func TestValuePredicatesCompareUnderNFC(t *testing.T) {
	const composed = "café"
	const decomposed = "café"
	_, notes := newVault(t, map[string]string{
		"tag.md":      "---\ntags: [" + decomposed + "]\n---\nbody\n",
		"category.md": "---\ncategories: [" + decomposed + "]\n---\nbody\n",
		"status.md":   "---\nstatus: " + decomposed + "\n---\nbody\n",
		"type.md":     "---\ntype: " + decomposed + "\n---\nbody\n",
		"kind.md":     "---\nkind: " + composed + "\n---\nbody\n",
	})
	cases := []struct {
		name string
		pred Predicate
		note string
	}{
		{"tag", Tag(composed), "tag.md"},
		{"category", Category(composed), "category.md"},
		{"status", Status(composed), "status.md"},
		{"type", Type(composed), "type.md"},
		{"kind_with_decomposed_query", Type(decomposed), "kind.md"},
	}
	for _, tc := range cases {
		if !tc.pred(notes[tc.note]) {
			t.Errorf("%s: a value differing only in normalization form did not match", tc.name)
		}
	}
}
