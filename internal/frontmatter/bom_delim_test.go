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
