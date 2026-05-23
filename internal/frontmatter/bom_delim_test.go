package frontmatter

import "testing"

// A UTF-8 BOM at the start of the file previously caused the opening `---\n`
// check to fail, treating valid frontmatter as body-only.
func TestParseStripsUTF8BOM(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(bom, []byte("---\ntitle: BOM\n---\nbody\n")...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "BOM" {
		t.Errorf("title: got %q want BOM", got)
	}
	if doc.Body != "body\n" {
		t.Errorf("body: got %q want %q", doc.Body, "body\n")
	}
}

// A body-only note that starts with a UTF-8 BOM should keep its bytes
// verbatim. BOM stripping is only part of recognising a real frontmatter
// fence, not a general body normalisation step.
func TestParsePreservesUTF8BOMWithoutFrontmatter(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(bom, []byte("# Body\n")...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Fatal("expected empty frontmatter")
	}
	if doc.Body != string(in) {
		t.Errorf("body: got %q want %q", doc.Body, string(in))
	}
}

// If a BOM-prefixed file looks like frontmatter but has no closing delimiter,
// Parse treats it as body-only and must still preserve the original bytes.
func TestParsePreservesUTF8BOMForUnterminatedFrontmatter(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(bom, []byte("---\ntitle: Body only\n")...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Fatal("expected empty frontmatter")
	}
	if doc.Body != string(in) {
		t.Errorf("body: got %q want %q", doc.Body, string(in))
	}
}

// Trailing whitespace on the `---` delimiter line is common when editors auto-
// insert it; the previous exact-match check rejected such files as body-only.
func TestParseDelimitersTolerateTrailingWhitespace(t *testing.T) {
	cases := map[string]string{
		"open trailing spaces":  "---  \ntitle: T\n---\nbody\n",
		"close trailing spaces": "---\ntitle: T\n---  \nbody\n",
		"open trailing tab":     "---\t\ntitle: T\n---\nbody\n",
		"open CRLF spaces":      "---  \r\ntitle: T\r\n---\r\nbody\r\n",
	}
	for name, in := range cases {
		doc, err := Parse([]byte(in))
		if err != nil {
			t.Errorf("%s: Parse: %v", name, err)
			continue
		}
		if got, _ := doc.Front.GetString("title"); got != "T" {
			t.Errorf("%s: title got %q want T", name, got)
		}
	}
}
