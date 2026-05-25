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

// A valid BOM-prefixed frontmatter document should keep the BOM when written
// back. Parse needs to strip it for delimiter recognition, but dropping it on
// Marshal changes the file encoding marker for an otherwise valid note.
func TestMarshalPreservesUTF8BOMWithFrontmatter(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(bom, []byte("---\ntitle: BOM\n---\nbody\n")...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(out) < len(bom) || string(out[:len(bom)]) != string(bom) {
		t.Fatalf("Marshal dropped UTF-8 BOM: %q", out)
	}
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	out2, err := Marshal(doc2, nil)
	if err != nil {
		t.Fatalf("Marshal round-trip: %v", err)
	}
	if string(out2) != string(out) {
		t.Fatalf("BOM round-trip not idempotent:\n%q\n%q", out, out2)
	}
}

func TestMarshalPreservesUTF8BOMWhenFrontmatterBecomesEmpty(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(bom, []byte("---\ntitle: BOM\n---\n---\nbody: literal\n---\n")...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Front.Unset("title")
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(out) < len(bom) || string(out[:len(bom)]) != string(bom) {
		t.Fatalf("Marshal dropped UTF-8 BOM after emptying frontmatter: %q", out)
	}
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	if !doc2.Front.IsEmpty() {
		t.Fatalf("guarded body delimiter was reinterpreted as frontmatter: %v", doc2.Front.Keys())
	}
	if doc2.Body != "---\nbody: literal\n---\n" {
		t.Fatalf("body changed:\n got %q\nwant %q", doc2.Body, "---\nbody: literal\n---\n")
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

func TestMarshalPreservesUTF8BOMForBodyOnlyNotes(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	cases := map[string]string{
		"plain body":               string(append(bom, []byte("# Body\n")...)),
		"unterminated frontmatter": string(append(bom, []byte("---\ntitle: Body only\n")...)),
	}
	for name, in := range cases {
		doc, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("%s: Parse: %v", name, err)
		}
		out, err := Marshal(doc, nil)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		if string(out) != in {
			t.Fatalf("%s: Marshal changed bytes:\n got %q\nwant %q", name, out, in)
		}
	}
}

func TestMarshalKeepsUTF8BOMAtFileStartWhenAddingFrontmatter(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(bom, []byte("# Body\n")...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Front.Set("title", "Added")

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := string(append(bom, []byte("---\ntitle: Added\n---\n# Body\n")...))
	if string(out) != want {
		t.Fatalf("Marshal misplaced BOM when adding frontmatter:\n got %q\nwant %q", out, want)
	}

	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	if got, _ := doc2.Front.GetString("title"); got != "Added" {
		t.Fatalf("round-trip title: got %q want %q", got, "Added")
	}
	if doc2.Body != "# Body\n" {
		t.Fatalf("round-trip body: got %q want %q", doc2.Body, "# Body\n")
	}
}

func TestMarshalPreservesBodyLeadingBOMAfterFrontmatter(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(append(bom, []byte("---\ntitle: BOM\n---\n")...), append(bom, []byte("body\n")...)...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("Marshal stripped a real body BOM:\n got %q\nwant %q", out, in)
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
