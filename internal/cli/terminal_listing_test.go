package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLIOnTerminal runs the command as if stdout were a terminal, with colour
// off so the assertions read the text a person would.
func runCLIOnTerminal(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	prev := stdoutIsTerminal
	stdoutIsTerminal = func(io.Writer) bool { return true }
	defer func() { stdoutIsTerminal = prev }()
	out, err := runCLI(t, "", args...)
	if err != nil {
		t.Fatalf("bn %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func terminalListingFixture(t *testing.T) string {
	t.Helper()
	root := buildSetFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "Books"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ntitle: Deep Work\n---\nDepth is rare.\nProtect deep blocks.\nGo DEEP daily.\nDeep again.\nAnd deep once more.\n"
	if err := os.WriteFile(filepath.Join(root, "Books", "Deep Work.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// A terminal listing names notes relative to the vault and says how many there
// were; the same command in a pipeline still prints absolute paths and nothing
// else, because that is what `bn find | bn set -` reads.
func TestFindListsVaultRelativePathsOnATerminal(t *testing.T) {
	root := terminalListingFixture(t)

	out := runCLIOnTerminal(t, "find", "--folder", "Books")
	if out != "Books/Deep Work.md\n1 note\n" {
		t.Fatalf("terminal find = %q", out)
	}

	piped, err := runCLI(t, "", "find", "--folder", "Books")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "Books", "Deep Work.md") + "\n"; piped != want {
		t.Fatalf("piped find = %q, want %q", piped, want)
	}

	// -0 is a machine form wherever it is written.
	if nul := runCLIOnTerminal(t, "find", "--folder", "Books", "-0"); nul != filepath.Join(root, "Books", "Deep Work.md")+"\x00" {
		t.Fatalf("terminal find -0 = %q", nul)
	}
	if empty := runCLIOnTerminal(t, "find", "--folder", "Nowhere"); empty != "no notes\n" {
		t.Fatalf("terminal find with no match = %q", empty)
	}
}

func TestSearchShowsMatchingLinesOnATerminal(t *testing.T) {
	root := terminalListingFixture(t)

	out := runCLIOnTerminal(t, "search", "deep")
	want := "Books/Deep Work.md\n" +
		"  5: Protect deep blocks.\n" +
		"  6: Go DEEP daily.\n" +
		"  7: Deep again.\n" +
		"  … 1 more\n" +
		"1 note\n"
	if out != want {
		t.Fatalf("terminal search =\n%s\nwant\n%s", out, want)
	}

	piped, err := runCLI(t, "", "search", "deep")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "Books", "Deep Work.md") + "\n"; piped != want {
		t.Fatalf("piped search = %q, want %q", piped, want)
	}
}

func TestHighlightLiteral(t *testing.T) {
	style := termStyle{color: true}
	if got, want := highlightLiteral(style, "Go DEEP, deep.", "deep", false), "Go \x1b[1;36mDEEP\x1b[0m, \x1b[1;36mdeep\x1b[0m."; got != want {
		t.Errorf("case-insensitive highlight = %q, want %q", got, want)
	}
	if got, want := highlightLiteral(style, "Go DEEP, deep.", "deep", true), "Go DEEP, \x1b[1;36mdeep\x1b[0m."; got != want {
		t.Errorf("case-sensitive highlight = %q, want %q", got, want)
	}
	// Folding İ changes its byte length, so offsets would be wrong: plain.
	if line := "İstanbul deep"; highlightLiteral(style, line, "deep", false) != line {
		t.Errorf("a line whose fold changes length must stay plain")
	}
	if line := "Go deep."; highlightLiteral(termStyle{}, line, "deep", false) != line {
		t.Errorf("no colour means no escapes")
	}
}

func TestCaptureIsBriefOnATerminal(t *testing.T) {
	root := buildSetFixture(t)

	out := runCLIOnTerminal(t, "capture", "a passing thought")
	lines := nonEmptyLines(out)
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "✓ Captured to ") || !strings.Contains(lines[1], "a passing thought") || !strings.Contains(lines[2], "bn undo") {
		t.Fatalf("terminal capture =\n%s", out)
	}
	if strings.Contains(out, "journal_id") {
		t.Fatalf("the journal id belongs to the piped form:\n%s", out)
	}

	piped, err := runCLI(t, "", "capture", "another thought")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(piped, "captured to ") || !strings.Contains(piped, "journal_id: ") {
		t.Fatalf("piped capture must keep its records:\n%s", piped)
	}
	_ = root
}

func TestDoctorGroupsTasksOnATerminal(t *testing.T) {
	buildSetFixture(t)

	out := runCLIOnTerminal(t, "doctor")
	if strings.Contains(out, "orphan_note:") || strings.Contains(out, "actions:") {
		t.Fatalf("terminal doctor must not print rule identifiers:\n%s", out)
	}
	if !strings.Contains(out, "Orphan note (") || !strings.Contains(out, "active task") {
		t.Fatalf("terminal doctor =\n%s", out)
	}

	piped, err := runCLI(t, "", "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(piped, "orphan_note: ") {
		t.Fatalf("piped doctor must keep one block per task:\n%s", piped)
	}
}
