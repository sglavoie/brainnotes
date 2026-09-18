package attachments

import (
	"errors"
	"path"
	"strings"
	"testing"
)

func TestCleanFilename_KeepsUsableNames(t *testing.T) {
	cases := map[string]string{
		"photo.png":                "photo.png",
		"  spaced name.PNG  ":      "spaced name.PNG",
		"Réunion été.pdf":          "Réunion été.pdf",
		`C:\Users\me\report.xlsx`:  "report.xlsx",
		"nested/dir/diagram.svg":   "diagram.svg",
		"../../etc/passwd":         "passwd",
		"dir/":                     "dir",
		"screen shot 09:41:22.png": "screen shot 09-41-22.png",
		"tabbed\tname.txt":         "tabbed-name.txt",
		"trailing dots...png":      "trailing dots...png",
		"trailing.":                "trailing",
		"no-extension":             "no-extension",
	}
	for input, want := range cases {
		got, err := cleanFilename(input)
		if err != nil {
			t.Errorf("cleanFilename(%q): unexpected error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("cleanFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCleanFilename_RefusesNamesThatCouldNotBeAttachments(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"only spaces":      "   ",
		"dot":              ".",
		"dot dot":          "..",
		"hidden":           ".env",
		"hidden after cut": "some/dir/.bashrc",
		"note":             "notes.md",
		"note uppercase":   "NOTES.MD",
		"markdown":         "notes.markdown",
		"root":             "/",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := cleanFilename(input)
			if !errors.Is(err, ErrInvalidFilename) {
				t.Fatalf("cleanFilename(%q) = %q, %v; want ErrInvalidFilename", input, got, err)
			}
		})
	}
}

// A client-supplied name never becomes more than one path component. It is cut
// down to a base name rather than refused, because a browser uploading a folder
// legitimately sends a relative path and the person did nothing wrong; what
// matters is that no rule after this one has to think about traversal.
func TestCleanFilename_ReducesEveryNameToOneComponent(t *testing.T) {
	inputs := []string{
		"../../etc/passwd",
		`..\..\windows\system32\drivers\etc\hosts`,
		"a/b/c/photo.png",
		"/absolute/photo.png",
		"..//..//photo.png",
	}
	for _, input := range inputs {
		got, err := cleanFilename(input)
		if err != nil {
			t.Errorf("cleanFilename(%q): %v", input, err)
			continue
		}
		if strings.ContainsAny(got, `/\`) || got == "." || got == ".." {
			t.Errorf("cleanFilename(%q) = %q, which is not one plain component", input, got)
		}
	}
}

// A filename is the one client-supplied string that becomes a path component, so
// the refusal of invalid UTF-8 is tested on its own: everything downstream —
// the record's JSON, the collision key's normalization, the link written into
// the note — assumes the name is text.
func TestCleanFilename_RefusesInvalidUTF8(t *testing.T) {
	if _, err := cleanFilename("bad\xffname.png"); !errors.Is(err, ErrInvalidFilename) {
		t.Fatalf("want ErrInvalidFilename for invalid UTF-8, got %v", err)
	}
}

func TestCleanFilename_ShortensWithoutLosingTheExtensionOrSplittingARune(t *testing.T) {
	long := strings.Repeat("é", 200) + ".png"
	got, err := cleanFilename(long)
	if err != nil {
		t.Fatalf("cleanFilename: %v", err)
	}
	if len(got) > filenameMaxLen {
		t.Fatalf("cleaned name is %d bytes, over the %d cap", len(got), filenameMaxLen)
	}
	if path.Ext(got) != ".png" {
		t.Fatalf("cleaned name %q lost its extension", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("cleaned name %q split a rune", got)
	}
}

func TestDestinationCandidates_StartAtTheChosenNameAndEndSomewhereUnique(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	got := destinationCandidates("attachments", "photo.png", id)
	if got[0] != "attachments/photo.png" {
		t.Fatalf("first candidate = %q, want the name the user chose", got[0])
	}
	if got[1] != "attachments/photo-1.png" {
		t.Fatalf("second candidate = %q, want the numbered disambiguation", got[1])
	}
	last := got[len(got)-1]
	if last != "attachments/photo-"+id[:8]+".png" {
		t.Fatalf("last candidate = %q, want the id-suffixed name", last)
	}
	seen := map[string]bool{}
	for _, candidate := range got {
		if seen[candidate] {
			t.Fatalf("candidate %q repeats, so a taken name would be retried", candidate)
		}
		seen[candidate] = true
	}
}
