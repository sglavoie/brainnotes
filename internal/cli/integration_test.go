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

// TestCaptureUndoScenario exercises capture → history → undo, covering both the
// bootstrap case (undo deletes the note capture created) and the append case
// (undo restores the note's prior bytes).
func TestCaptureUndoScenario(t *testing.T) {
	root := buildIntegrationFixture(t)
	fixCaptureClock(t)
	inbox := filepath.Join(root, "Inbox.md")

	out, err := runCLI(t, "", "capture", "remember this")
	if err != nil {
		t.Fatalf("capture: %v\n%s", err, out)
	}
	if got := readNote(t, inbox); !strings.Contains(got, "- 2026-08-17 09:30 remember this") {
		t.Fatalf("bootstrapped inbox missing the capture:\n%s", got)
	}

	id := latestHistoryEntryID(t)
	if !strings.Contains(out, "journal_id: "+id) {
		t.Fatalf("capture output %q does not report journal id %q", out, id)
	}
	if _, err := runCLI(t, "", "undo", id); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if _, err := os.Stat(inbox); !os.IsNotExist(err) {
		t.Fatalf("expected undo to delete the bootstrapped inbox, stat err=%v", err)
	}

	original := "---\ntitle: Inbox\n---\n## Captures\n- 2026-08-16 08:00 older\n"
	if err := os.WriteFile(inbox, []byte(original), 0o644); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
	if out, err := runCLI(t, "", "capture", "second thought"); err != nil {
		t.Fatalf("capture: %v\n%s", err, out)
	}
	if got := readNote(t, inbox); got != original+"- 2026-08-17 09:30 second thought\n" {
		t.Fatalf("append did not land as expected:\n%q", got)
	}
	if _, err := runCLI(t, "", "undo", latestHistoryEntryID(t)); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if got := readNote(t, inbox); got != original {
		t.Fatalf("undo did not restore the note:\n--- want ---\n%s--- got ---\n%s", original, got)
	}
}

// TestCaptureUndoConflictRequiresForce pins the guidance printed by `bn capture`:
// ordinary undo refuses once the inbox changed after the capture, and only
// --force restores the snapshot over those later edits.
func TestCaptureUndoConflictRequiresForce(t *testing.T) {
	root := buildIntegrationFixture(t)
	fixCaptureClock(t)
	inbox := filepath.Join(root, "Inbox.md")

	original := "---\ntitle: Inbox\n---\n## Captures\n- 2026-08-16 08:00 older\n"
	if err := os.WriteFile(inbox, []byte(original), 0o644); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
	if out, err := runCLI(t, "", "capture", "third thought"); err != nil {
		t.Fatalf("capture: %v\n%s", err, out)
	}
	id := latestHistoryEntryID(t)

	edited := original + "- 2026-08-17 09:30 third thought\n\nUnrelated later edit.\n"
	if err := os.WriteFile(inbox, []byte(edited), 0o644); err != nil {
		t.Fatalf("edit inbox: %v", err)
	}

	out, err := runCLI(t, "", "undo", id)
	if err == nil {
		t.Fatalf("expected undo to refuse after a later edit:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("conflict error does not mention --force: %v", err)
	}
	if got := readNote(t, inbox); got != edited {
		t.Fatalf("refused undo still wrote:\n%q", got)
	}

	if out, err := runCLI(t, "", "undo", id, "--force"); err != nil {
		t.Fatalf("undo --force: %v\n%s", err, out)
	}
	if got := readNote(t, inbox); got != original {
		t.Fatalf("--force did not restore the snapshot over the later edit:\n--- want ---\n%s--- got ---\n%s", original, got)
	}
}

// latestHistoryEntryID returns the ID column of the newest `bn history` row.
func latestHistoryEntryID(t *testing.T) string {
	t.Helper()
	out, err := runCLI(t, "", "history")
	if err != nil {
		t.Fatalf("history: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("history has no entries:\n%s", out)
	}
	fields := strings.Split(lines[1], "\t")
	if len(fields) < 3 {
		t.Fatalf("unexpected history row %q", lines[1])
	}
	return fields[1]
}
