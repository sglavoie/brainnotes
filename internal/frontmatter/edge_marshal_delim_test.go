package frontmatter

import "testing"

// TestMarshalEmptyFrontPreservesBodyStartingWithDelimiter — when the Front map
// becomes empty (e.g. after Unset removes all keys) and the body happens to
// begin with a parsable frontmatter block, Marshal must emit an empty
// `---\n---\n` block so the body is not silently reinterpreted as the new
// frontmatter on the next Parse. Before the fix, a doc whose body began with
// `---\nother: yaml\n---\n...` would round-trip into a doc whose Front contained
// `{other: yaml}` and a truncated body — silent corruption.
func TestMarshalEmptyFrontPreservesBodyStartingWithDelimiter(t *testing.T) {
	src := "---\nfoo: bar\n---\n---\nother: yaml\n---\nreal body\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Front.Unset("foo")
	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse2: %v", err)
	}
	if !doc2.Front.IsEmpty() {
		t.Errorf("Front should remain empty after round-trip; got keys=%v", doc2.Front.Keys())
	}
	wantBody := "---\nother: yaml\n---\nreal body\n"
	if doc2.Body != wantBody {
		t.Errorf("Body corrupted:\n got %q\nwant %q", doc2.Body, wantBody)
	}

	// Idempotency: marshal again must produce the same bytes.
	out2, err := Marshal(doc2, nil)
	if err != nil {
		t.Fatalf("Marshal2: %v", err)
	}
	if string(out) != string(out2) {
		t.Errorf("not idempotent:\n%q\n%q", out, out2)
	}
}

func TestMarshalEmptyFrontDoesNotGuardUnterminatedDelimiterBody(t *testing.T) {
	cases := map[string]string{
		"lf":       "---\n# body\n",
		"crlf":     "---\r\n# body\r\n",
		"bom lf":   "\ufeff---\ntitle: Body only\n",
		"bom crlf": "\ufeff---\r\ntitle: Body only\r\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			doc := Document{Front: NewMap(), Body: body}
			out, err := Marshal(doc, nil)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(out) != body {
				t.Fatalf("body-only marshal changed bytes:\n got %q\nwant %q", out, body)
			}
			doc2, err := Parse(out)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !doc2.Front.IsEmpty() {
				t.Fatalf("unterminated delimiter body should not parse as frontmatter")
			}
			if doc2.Body != body {
				t.Fatalf("body after parse changed:\n got %q\nwant %q", doc2.Body, body)
			}
		})
	}
}
