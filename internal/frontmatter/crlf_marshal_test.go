package frontmatter

import "testing"

func TestMarshalPreservesCRLFOnNoopRoundTrip(t *testing.T) {
	src := "---\r\ntitle: Windows\r\n---\r\nBody.\r\n"

	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(out) != src {
		t.Fatalf("round-trip changed bytes:\n got %q\nwant %q", out, src)
	}
}

func TestMarshalPreservesCRLFWhenFrontmatterMutates(t *testing.T) {
	src := "---\r\ntitle: Windows\r\n---\r\nBody.\r\n"

	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Front.Set("tags", []any{"one", "two"})

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := "---\r\ntags:\r\n  - one\r\n  - two\r\ntitle: Windows\r\n---\r\nBody.\r\n"
	if string(out) != want {
		t.Fatalf("mutated output changed line endings:\n got %q\nwant %q", out, want)
	}
}
