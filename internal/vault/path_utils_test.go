package vault

import (
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// TestCleanSlashRelEdgeCases exercises the unexported cleanSlashRel directly
// for inputs Walk/ResolveByTitle/ignoreSet rely on: whitespace, dot segments,
// duplicate slashes, backslashes, and traversal forms.
func TestCleanSlashRelEdgeCases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{".", ""},
		{"./", ""},
		{"/", ""},
		{"//", ""},
		{"/.", ""},
		{"/./", ""},
		{"a", "a"},
		{"a/", "a"},
		{"/a", "a"},
		{"/a/", "a"},
		{"a//b", "a/b"},
		{"a/./b", "a/b"},
		{`a\b`, "a/b"},
		{`a\b\c`, "a/b/c"},
		{"a/b/..", "a"},
		{"..", ".."},
		{"../foo", "../foo"},
		{"a/../..", ".."},
		{"/..", ".."},
	}
	for _, tc := range cases {
		got := cleanSlashRel(tc.in)
		if got != tc.want {
			t.Errorf("cleanSlashRel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRelEscapesRoot verifies the escape predicate for outputs of
// cleanSlashRel: anything matching ".." or "../*" escapes.
func TestRelEscapesRoot(t *testing.T) {
	escape := []string{"..", "../foo", "../../bar"}
	safe := []string{"", "a", "a/b", "...", "....", "a/..b"}
	for _, p := range escape {
		if !relEscapesRoot(p) {
			t.Errorf("relEscapesRoot(%q) = false, want true", p)
		}
	}
	for _, p := range safe {
		if relEscapesRoot(p) {
			t.Errorf("relEscapesRoot(%q) = true, want false", p)
		}
	}
}

// TestIgnoreSetSkipsEmptyAndEscaping verifies that empty/whitespace ignore
// entries are dropped, and entries that normalize to a traversal form are
// rejected rather than added to the set.
func TestIgnoreSetSkipsEmptyAndEscaping(t *testing.T) {
	cfg := &config.Config{
		Ignore: []string{"", "  ", "Archive", "../escape", "Trash/"},
		Templates: config.TemplatesConfig{
			Dir: "../../bad", // also escaping; must not land in the set.
		},
	}
	set := ignoreSet(cfg)
	if _, ok := set["Archive"]; !ok {
		t.Error("Archive should be in ignore set")
	}
	if _, ok := set["Trash"]; !ok {
		t.Error("Trash (from Trash/) should be in ignore set")
	}
	if _, ok := set[""]; ok {
		t.Error("empty string should not be in ignore set")
	}
	if _, ok := set["../escape"]; ok {
		t.Error("escaping entry should not be in ignore set")
	}
	if _, ok := set["../../bad"]; ok {
		t.Error("escaping templates dir should not be in ignore set")
	}
}
