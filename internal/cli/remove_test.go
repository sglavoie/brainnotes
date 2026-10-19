package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildRemoveFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "Projects", "Nested", "Remove me.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("---\ntitle: Remove me\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	useVaultRoot(t, root, testPropertyDefinitions)
	return root
}

func pickRemoveNote(t *testing.T, root string) {
	t.Helper()
	original := notePicker
	t.Cleanup(func() { notePicker = original })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{filepath.Join(root, "Projects", "Nested", "Remove me.md")}, nil
	}
}

func TestRemove_ConfirmedMovesNestedNoteAndJournals(t *testing.T) {
	pretendTerminal(t)
	root := buildRemoveFixture(t)
	pickRemoveNote(t, root)

	out, err := runCLI(t, "yes\n", "remove")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	source := filepath.Join(root, "Projects", "Nested", "Remove me.md")
	destination := filepath.Join(root, "Trash", "Projects", "Nested", "Remove me.md")
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("trash destination missing: %v", err)
	}
	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("journal entry count = %d, want 1", len(entries))
	}
	if entries[0].Command != "bn remove" {
		t.Fatalf("journal command = %q, want bn remove", entries[0].Command)
	}
	for _, want := range []string{
		"Move Projects/Nested/Remove me.md to Trash? [y/N]",
		"removed: Projects/Nested/Remove me.md -> Trash/Projects/Nested/Remove me.md",
		"journal_id: " + entries[0].ID,
		"recover with: bn undo " + entries[0].ID,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRemove_YesSkipsConfirmationInput(t *testing.T) {
	root := buildRemoveFixture(t)
	pickRemoveNote(t, root)

	out, err := runCLI(t, "", "remove", "--yes")
	if err != nil {
		t.Fatalf("remove --yes: %v\n%s", err, out)
	}
	if strings.Contains(out, "Move ") {
		t.Fatalf("--yes should not prompt:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "Trash", "Projects", "Nested", "Remove me.md")); err != nil {
		t.Fatalf("trash destination missing: %v", err)
	}
}

func TestRemove_CancelledResponsesLeaveNoteUnchanged(t *testing.T) {
	pretendTerminal(t)
	for _, tc := range []struct {
		name  string
		stdin string
	}{
		{name: "declined", stdin: "n\n"},
		{name: "blank", stdin: "\n"},
		{name: "eof", stdin: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildRemoveFixture(t)
			pickRemoveNote(t, root)

			out, err := runCLI(t, tc.stdin, "remove")
			if !errors.Is(err, ErrCanceled) {
				t.Fatalf("err = %v, want ErrCanceled\n%s", err, out)
			}
			if err.Error() != "remove: canceled" {
				t.Fatalf("err = %q, want %q", err, "remove: canceled")
			}
			if _, err := os.Stat(filepath.Join(root, "Projects", "Nested", "Remove me.md")); err != nil {
				t.Fatalf("source should remain: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "Trash")); !os.IsNotExist(err) {
				t.Fatalf("Trash should not exist, stat err=%v", err)
			}
			if got := len(loadJournalEntries(t, root)); got != 0 {
				t.Fatalf("journal entry count = %d, want 0", got)
			}
		})
	}
}

func TestRemove_EmptyOrDismissedPickerIsNoOp(t *testing.T) {
	t.Run("empty vault", func(t *testing.T) {
		root := t.TempDir()
		useVaultRoot(t, root, testPropertyDefinitions)

		out, err := runCLI(t, "", "remove")
		if !errors.Is(err, ErrNothingSelected) {
			t.Fatalf("err = %v, want ErrNothingSelected\n%s", err, out)
		}
		if out != "" {
			t.Fatalf("empty vault output = %q, want empty", out)
		}
		if got := len(loadJournalEntries(t, root)); got != 0 {
			t.Fatalf("journal entry count = %d, want 0", got)
		}
	})

	t.Run("dismissed picker", func(t *testing.T) {
		root := buildRemoveFixture(t)
		original := notePicker
		t.Cleanup(func() { notePicker = original })
		notePicker = func(items []string, extraArgs []string) ([]string, error) { return nil, nil }

		out, err := runCLI(t, "", "remove")
		if !errors.Is(err, ErrNothingSelected) {
			t.Fatalf("err = %v, want ErrNothingSelected\n%s", err, out)
		}
		if out != "" {
			t.Fatalf("dismissed picker output = %q, want empty", out)
		}
		if _, err := os.Stat(filepath.Join(root, "Projects", "Nested", "Remove me.md")); err != nil {
			t.Fatalf("source should remain: %v", err)
		}
	})
}

func TestRemove_RejectsMultipleArguments(t *testing.T) {
	buildRemoveFixture(t)

	_, err := runCLI(t, "", "remove", "one", "two")
	if err == nil {
		t.Fatal("remove should reject multiple arguments")
	}
	if !strings.Contains(err.Error(), "unknown command") && !strings.Contains(err.Error(), "accepts at most 1 arg(s)") {
		t.Fatalf("unexpected argument error: %v", err)
	}
}

func TestRemove_PropagatesTrashFailureWithoutArtifacts(t *testing.T) {
	root := buildRemoveFixture(t)
	original := notePicker
	t.Cleanup(func() { notePicker = original })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{filepath.Join(root, "Projects", "Nested", "missing.md")}, nil
	}

	_, err := runCLI(t, "", "remove", "--yes")
	if err == nil {
		t.Fatal("remove should propagate a missing source failure")
	}
	if _, statErr := os.Stat(filepath.Join(root, "Trash")); !os.IsNotExist(statErr) {
		t.Fatalf("Trash should not exist, stat err=%v", statErr)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("journal entry count = %d, want 0", got)
	}
}

func TestRemove_ExplicitTargetSkipsPickerAndCanBeUndone(t *testing.T) {
	pretendTerminal(t)
	for _, target := range []string{"remove ME", "Projects/Nested/Remove me.md"} {
		t.Run(target, func(t *testing.T) {
			root := buildRemoveFixture(t)
			original := notePicker
			t.Cleanup(func() { notePicker = original })
			notePicker = func(items []string, extraArgs []string) ([]string, error) {
				t.Fatal("explicit target must not open picker")
				return nil, nil
			}
			out, err := runCLI(t, "yes\n", "remove", target)
			if err != nil {
				t.Fatalf("remove: %v\n%s", err, out)
			}
			if !strings.Contains(out, "Move Projects/Nested/Remove me.md to Trash? [y/N]") {
				t.Fatalf("missing confirmation: %s", out)
			}
			entries := loadJournalEntries(t, root)
			if len(entries) != 1 {
				t.Fatalf("journal entries = %d", len(entries))
			}
			if _, err := os.Stat(filepath.Join(root, "Trash/Projects/Nested/Remove me.md")); err != nil {
				t.Fatal(err)
			}
			if _, err := runCLI(t, "", "undo", entries[0].ID); err != nil {
				t.Fatalf("undo: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "Projects/Nested/Remove me.md")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRemove_ExplicitTargetDeclined(t *testing.T) {
	pretendTerminal(t)
	root := buildRemoveFixture(t)
	if _, err := runCLI(t, "n\n", "remove", "Remove me"); !errors.Is(err, ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Projects/Nested/Remove me.md")); err != nil {
		t.Fatal(err)
	}
	if len(loadJournalEntries(t, root)) != 0 {
		t.Fatal("declining created a journal entry")
	}
}

func TestRemove_ExplicitInvalidTargetsDoNotWrite(t *testing.T) {
	for _, target := range []string{"missing.md", "../outside.md", "Remove me"} {
		t.Run(target, func(t *testing.T) {
			root := buildRemoveFixture(t)
			if err := os.WriteFile(filepath.Join(root, "Remove me.md"), []byte("duplicate"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := runCLI(t, "", "remove", target, "--yes"); err == nil {
				t.Fatal("invalid or ambiguous target accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "Projects/Nested/Remove me.md")); err != nil {
				t.Fatal(err)
			}
			if len(loadJournalEntries(t, root)) != 0 {
				t.Fatal("invalid target created a journal entry")
			}
		})
	}
}

// pretendTerminal makes stdin count as a terminal, so a test can answer the
// confirmation prompt from a buffer.
func pretendTerminal(t *testing.T) {
	t.Helper()
	prev := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsTerminal = prev })
}

// Without a terminal remove refuses rather than reading an answer from stdin,
// the same rule `restore --wipe` follows; --yes is the scripted spelling.
func TestRemove_WithoutTerminalRefusesUnlessYes(t *testing.T) {
	root := buildRemoveFixture(t)
	source := filepath.Join(root, "Projects", "Nested", "Remove me.md")

	out, err := runCLI(t, "yes\n", "remove", "Remove me")
	if err == nil || IsNegativeOutcome(err) || !strings.Contains(err.Error(), "remove: no terminal to confirm on; pass --yes") {
		t.Fatalf("err = %v, want the no-terminal refusal (exit 2)\n%s", err, out)
	}
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatalf("refused remove moved the note: %v", statErr)
	}

	if out, err := runCLI(t, "", "remove", "-y", "Remove me"); err != nil {
		t.Fatalf("remove -y: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(source); !os.IsNotExist(statErr) {
		t.Fatalf("remove -y left the note in place: %v", statErr)
	}
}

// --dry-run prints the shared preview format and neither asks nor writes.
func TestRemove_DryRunPreviewsTheMove(t *testing.T) {
	root := buildRemoveFixture(t)
	source := filepath.Join(root, "Projects", "Nested", "Remove me.md")

	out, err := runCLI(t, "", "remove", "--dry-run", "Remove me")
	if err != nil {
		t.Fatalf("remove --dry-run: %v\n%s", err, out)
	}
	want := "--- Projects/Nested/Remove me.md\npath: Projects/Nested/Remove me.md -> Trash/Projects/Nested/Remove me.md\n"
	if out != want {
		t.Fatalf("dry-run output = %q, want %q", out, want)
	}
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatalf("dry run moved the note: %v", statErr)
	}
	if entries := loadJournalEntries(t, root); len(entries) != 0 {
		t.Fatalf("dry run journaled %d entries", len(entries))
	}
}
