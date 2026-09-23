package cli

import (
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
			if err != nil {
				t.Fatalf("remove: %v\n%s", err, out)
			}
			if !strings.Contains(out, "canceled") {
				t.Fatalf("expected cancellation output:\n%s", out)
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
		if err != nil {
			t.Fatalf("remove: %v\n%s", err, out)
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
		if err != nil {
			t.Fatalf("remove: %v\n%s", err, out)
		}
		if out != "" {
			t.Fatalf("dismissed picker output = %q, want empty", out)
		}
		if _, err := os.Stat(filepath.Join(root, "Projects", "Nested", "Remove me.md")); err != nil {
			t.Fatalf("source should remain: %v", err)
		}
	})
}

func TestRemove_RejectsPositionalArguments(t *testing.T) {
	root := buildRemoveFixture(t)

	_, err := runCLI(t, "", "remove", filepath.Join(root, "Projects", "Nested", "Remove me.md"))
	if err == nil {
		t.Fatal("remove should reject positional arguments")
	}
	if !strings.Contains(err.Error(), "unknown command") && !strings.Contains(err.Error(), "accepts 0 arg(s)") {
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
