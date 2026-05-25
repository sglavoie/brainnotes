package frontmatter

import (
	"strings"
	"testing"
)

// TestParseBOMWithCRLF covers a UTF-8 BOM combined with Windows line endings —
// both normalisations have to happen for the opening fence to be recognised.
func TestParseBOMWithCRLF(t *testing.T) {
	in := append([]byte{0xEF, 0xBB, 0xBF}, []byte("---\r\ntitle: T\r\n---\r\nbody\r\n")...)
	doc, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "T" {
		t.Errorf("title: got %q want T", got)
	}
	if doc.Body != "body\r\n" {
		t.Errorf("body: got %q want %q", doc.Body, "body\r\n")
	}
}

// TestParseClosingFenceAtEOF accepts a closing `---` with no trailing newline
// (last byte of file).
func TestParseClosingFenceAtEOF(t *testing.T) {
	in := "---\ntitle: T\n---"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "T" {
		t.Errorf("title: got %q want T", got)
	}
	if doc.Body != "" {
		t.Errorf("body: got %q want empty", doc.Body)
	}
}

// TestParseClosingFenceTrailingWhitespaceCRLF mirrors the open-delimiter
// tolerance for the close delimiter, including CRLF.
func TestParseClosingFenceTrailingWhitespaceCRLF(t *testing.T) {
	in := "---\r\ntitle: T\r\n--- \t\r\nbody\r\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "T" {
		t.Errorf("title: got %q want T", got)
	}
	if doc.Body != "body\r\n" {
		t.Errorf("body: got %q want %q", doc.Body, "body\r\n")
	}
}

// TestParseEmptyFrontmatterBlock — `---\n---\n` is an empty mapping. Marshal
// then drops the empty block (no keys) and emits body-only.
func TestParseEmptyFrontmatterBlock(t *testing.T) {
	in := "---\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty front")
	}
	if doc.Body != "body\n" {
		t.Errorf("body: got %q", doc.Body)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != "body\n" {
		t.Errorf("out: got %q want %q", string(out), "body\n")
	}
}

// TestParseBodyNoTrailingNewline verifies that a missing trailing newline is
// preserved verbatim through Marshal.
func TestParseBodyNoTrailingNewline(t *testing.T) {
	in := "---\ntitle: T\n---\nbody no newline"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Body != "body no newline" {
		t.Errorf("body: got %q", doc.Body)
	}
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasSuffix(string(out), "body no newline") {
		t.Errorf("Marshal lost body suffix: %q", string(out))
	}
}

// TestParseIndentedDelimiterIsBody — a leading-space `---` line is NOT a
// frontmatter delimiter (Obsidian and Markdown both require the fence at
// column 0).
func TestParseIndentedDelimiterIsBody(t *testing.T) {
	in := " ---\ntitle: T\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("indented `---` should be treated as body, not a delimiter")
	}
	if doc.Body != in {
		t.Errorf("body should equal input verbatim")
	}
}

// TestParseEmbeddedDelimiterMultiline — a multi-line block scalar inside the
// YAML that contains an indented `---` line must not be picked up as a closing
// delimiter (isDelimLine only matches a flush-left `---`).
func TestParseEmbeddedDelimiterMultiline(t *testing.T) {
	in := "---\ndesc: |\n  Line 1\n  ---\n  Line 3\ntitle: T\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := doc.Front.GetString("title"); got != "T" {
		t.Errorf("title: got %q want T", got)
	}
	desc, _ := doc.Front.GetString("desc")
	if !strings.Contains(desc, "---") {
		t.Errorf("desc lost embedded `---`: %q", desc)
	}
}

// TestParseWhitespaceOnlyFrontmatter — whitespace-only content between the
// delimiters should parse as an empty Map (no keys).
func TestParseWhitespaceOnlyFrontmatter(t *testing.T) {
	in := "---\n  \n\t\n---\nbody\n"
	doc, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Front.IsEmpty() {
		t.Errorf("expected empty front for whitespace-only content, got %d keys", doc.Front.Len())
	}
	if doc.Body != "body\n" {
		t.Errorf("body: got %q", doc.Body)
	}
}
