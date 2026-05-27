package frontmatter

import (
	"testing"
)

func TestMutateRoundTrip(t *testing.T) {
	src := readFixture(t, "mutate.in.md")
	cfg := defaultCfg()

	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if _, err := doc.Front.ListAdd("topics", "yaml"); err != nil {
		t.Fatalf("ListAdd topics: %v", err)
	}
	if _, err := doc.Front.ListRemove("topics", "go"); err != nil {
		t.Fatalf("ListRemove topics: %v", err)
	}
	if _, err := doc.Front.ReplaceValue("status", "draft", "published"); err != nil {
		t.Fatalf("ReplaceValue status: %v", err)
	}

	got, err := Marshal(doc, cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := readFixture(t, "mutate.out.md")
	if string(got) != string(want) {
		t.Errorf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	doc2, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	out2, err := Marshal(doc2, cfg)
	if err != nil {
		t.Fatalf("Marshal round-trip: %v", err)
	}
	if string(out2) != string(got) {
		t.Errorf("not idempotent after mutation:\nfirst:\n%s\nsecond:\n%s", got, out2)
	}
}
