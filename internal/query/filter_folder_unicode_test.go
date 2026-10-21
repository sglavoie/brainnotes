package query

import (
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// TestFolderComparesUnderNFCAndCase builds notes by path rather than on disk,
// so the decomposed folder name reaches the predicate byte for byte whatever
// the test machine's filesystem does with it. The fixtures are \u escapes so a
// normalizing editor cannot make the two spellings one.
func TestFolderComparesUnderNFCAndCase(t *testing.T) {
	const (
		composed   = "Café"
		decomposed = "Café"
	)
	if composed == decomposed {
		t.Fatal("fixtures are the same bytes; the test proves nothing")
	}
	root := t.TempDir()
	note := func(rel string) *vault.Note {
		return &vault.Note{Path: filepath.Join(root, filepath.FromSlash(rel))}
	}
	cases := []struct {
		name   string
		folder string
		rel    string
		want   bool
	}{
		{"composed query, decomposed folder", composed, decomposed + "/Note.md", true},
		{"decomposed query, composed folder", decomposed, composed + "/Note.md", true},
		{"descendant across forms", composed, decomposed + "/Sub/Note.md", true},
		{"nested prefix across forms", "Projects/" + composed, "Projects/" + decomposed + "/Note.md", true},
		{"case-insensitive like other string fields", "café", "CAFÉ/Note.md", true},
		{"ascii case-insensitive", "archive", "Archive/Old.md", true},
		{"sibling with shared prefix still excluded", composed, decomposed + "s/Note.md", false},
	}
	for _, tc := range cases {
		if got := Folder(tc.folder, root)(note(tc.rel)); got != tc.want {
			t.Errorf("%s: Folder(%q) on %q = %v, want %v", tc.name, tc.folder, tc.rel, got, tc.want)
		}
	}
}
