package query

import "testing"

func TestPinnedPredicate(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"ranked.md":   "---\npinned: 1.5\n---\nbody\n",
		"int.md":      "---\npinned: 3\n---\nbody\n",
		"unranked.md": "---\npinned: true\n---\nbody\n",
		"hostile.md":  "---\npinned: high\n---\nbody\n",
		"off.md":      "---\npinned: false\n---\nbody\n",
		"plain.md":    "---\ntitle: plain\n---\nbody\n",
	})

	p := Pinned()
	cases := map[string]bool{
		"ranked.md":   true,
		"int.md":      true,
		"unranked.md": true,
		"hostile.md":  true,
		"off.md":      false,
		"plain.md":    false,
	}
	for path, want := range cases {
		if got := p(notes[path]); got != want {
			t.Errorf("Pinned()(%s) = %v, want %v", path, got, want)
		}
	}
	if p(nil) {
		t.Error("Pinned()(nil) = true, want false")
	}
}

func TestFromFlags_PinnedNeedsNoDeclarationOrGraph(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"pinned.md": "---\npinned: 1.0\n---\nbody\n",
		"plain.md":  "---\ntitle: plain\n---\nbody\n",
	})

	p, err := FromFlags(Flags{
		Pinned:       true,
		MinBacklinks: -1,
		MaxBacklinks: -1,
		MinLinks:     -1,
		MaxLinks:     -1,
	})
	if err != nil {
		t.Fatalf("FromFlags: %v", err)
	}
	if !p(notes["pinned.md"]) {
		t.Error("pinned.md should match --pinned")
	}
	if p(notes["plain.md"]) {
		t.Error("plain.md should not match --pinned")
	}
}
