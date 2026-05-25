package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEndScenario exercises the full find → set → rename → move → unset
// pipeline against a representative fixture vault, asserting the on-disk
// state after each step.
func TestEndToEndScenario(t *testing.T) {
	root := buildIntegrationFixture(t)

	out, err := runCLI(t, "", "find", "--tag", "sci-fi")
	if err != nil {
		t.Fatalf("find: %v\n%s", err, out)
	}
	got := splitAndSort(out)
	want := []string{filepath.Join(root, "References", "Dune.md")}
	if !equalStrings(got, want) {
		t.Fatalf("find --tag sci-fi:\n got=%v\nwant=%v", got, want)
	}

	dunePath := filepath.Join(root, "References", "Dune.md")
	if _, err := runCLI(t, "", "set", dunePath, "status=Reading", "rating=7"); err != nil {
		t.Fatalf("set: %v", err)
	}
	body := readNote(t, dunePath)
	if !strings.Contains(body, "status: Reading") {
		t.Fatalf("expected status: Reading, got:\n%s", body)
	}
	if !strings.Contains(body, "rating: 7") {
		t.Fatalf("expected rating: 7, got:\n%s", body)
	}
	assertAlphabeticalKeys(t, body)

	if _, err := runCLI(t, "", "rename", dunePath, "Sand Worms"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	newPath := filepath.Join(root, "References", "Sand Worms.md")
	if _, err := os.Stat(dunePath); !os.IsNotExist(err) {
		t.Fatalf("expected %s gone, stat err=%v", dunePath, err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected %s present: %v", newPath, err)
	}

	review := readNote(t, filepath.Join(root, "References", "Dune Review.md"))
	wantSubs := []string{
		"[[Sand Worms]]",
		"[[Sand Worms|the novel]]",
		"[[Sand Worms#Sequels]]",
		"[[References/Sand Worms]]",
		"parent: '[[Sand Worms]]'",
	}
	for _, want := range wantSubs {
		if !strings.Contains(review, want) {
			t.Errorf("review missing %q:\n%s", want, review)
		}
	}
	if strings.Contains(review, "[[Dune") {
		t.Errorf("review still has [[Dune... refs:\n%s", review)
	}

	person := readNote(t, filepath.Join(root, "People", "Frank Herbert.md"))
	if !strings.Contains(person, "[[Sand Worms]]") {
		t.Errorf("person body not rewritten:\n%s", person)
	}

	project := readNote(t, filepath.Join(root, "Projects", "Reading List.md"))
	if !strings.Contains(project, "[[Sand Worms]]") {
		t.Errorf("project body not rewritten:\n%s", project)
	}

	if _, err := runCLI(t, "", "move", newPath, "References/Books"); err != nil {
		t.Fatalf("move: %v", err)
	}
	movedPath := filepath.Join(root, "References", "Books", "Sand Worms.md")
	if _, err := os.Stat(movedPath); err != nil {
		t.Fatalf("expected %s present: %v", movedPath, err)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}

	review = readNote(t, filepath.Join(root, "References", "Dune Review.md"))
	if !strings.Contains(review, "[[References/Books/Sand Worms]]") {
		t.Errorf("expected folder-prefixed rewrite to References/Books/Sand Worms:\n%s", review)
	}
	if !strings.Contains(review, "[[Sand Worms]]") {
		t.Errorf("expected bare [[Sand Worms]] preserved:\n%s", review)
	}

	if _, err := runCLI(t, "", "unset", movedPath, "rating"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	body = readNote(t, movedPath)
	if strings.Contains(body, "rating:") {
		t.Fatalf("rating should be gone:\n%s", body)
	}
	if !strings.Contains(body, "status: Reading") {
		t.Fatalf("other properties should remain:\n%s", body)
	}
	assertAlphabeticalKeys(t, body)
}
