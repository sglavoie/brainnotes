package threads

import (
	"strings"
	"testing"
)

func TestCollectionRoundTripAndEdits(t *testing.T) {
	prefix := "---\ntitle: Habits\n---\n\n"
	original := prefix + NewBody("What survives stress?") + "A draft paragraph.\n"
	d, err := Parse(original)
	if err != nil {
		t.Fatal(err)
	}
	quote := "A <script> & [[link]] **bold** `code`\n\nAnother line."
	item := Item{Group: "Evidence", Link: "[[Notes/Book]]", Quote: quote, Commentary: "My **commentary**.", Revision: strings.Repeat("a", 64)}
	requireNoError(t, d.Add(item))
	item.Group = "Counterexamples"
	requireNoError(t, d.Add(item))
	saved := d.String()
	parsed, err := Parse(saved)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Items[0].Quote != quote || parsed.String() != saved {
		t.Fatal("quotation or bytes changed")
	}
	requireNoError(t, parsed.Move(1, -1))
	requireNoError(t, parsed.Group(1, "Still unclear"))
	if parsed.Items[0].Group != "Counterexamples" || parsed.Items[1].Group != "Still unclear" {
		t.Fatal("ordering/grouping failed")
	}
	if parsed.Before != d.Before || parsed.After != d.After {
		t.Fatal("writing outside collection changed")
	}
}

func TestRefuseUnrecognizedCollection(t *testing.T) {
	for _, content := range []string{Start + "\nMy external edit\n" + End, End + Start, NewBody("Q") + Start} {
		if _, err := Parse(content); err == nil {
			t.Fatalf("accepted %q", content)
		}
	}
	d, _ := Parse(NewBody("Q"))
	if err := d.Add(Item{Group: "Evidence", Link: "[[A]]", Revision: strings.Repeat("a", 64), Commentary: Start}); err == nil {
		t.Fatal("accepted marker injection")
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
