package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixCaptureClock pins the capture timestamp so rendered bullets and goldens
// are deterministic.
func fixCaptureClock(t *testing.T) {
	t.Helper()
	prev := clockForCapture
	clockForCapture = func() time.Time { return time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC) }
	t.Cleanup(func() { clockForCapture = prev })
}

// buildCaptureFixture writes a vault whose inbox note exists with the
// configured heading already present. Files are keyed vault-relative.
func buildCaptureFixture(t *testing.T, files map[string]string) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	useVaultRoot(t, root, testPropertyDefinitions)
	fixCaptureClock(t)
	return root
}

func TestCapture_PositionalText(t *testing.T) {
	root := buildCaptureFixture(t, map[string]string{
		"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n- 2026-08-16 08:00 older\n",
	})

	out, err := runCLI(t, "", "capture", "call", "the", "dentist")
	if err != nil {
		t.Fatalf("capture: %v\n%s", err, out)
	}

	got := readNote(t, filepath.Join(root, "Inbox.md"))
	want := "---\ntitle: Inbox\n---\n## Captures\n- 2026-08-16 08:00 older\n- 2026-08-17 09:30 call the dentist\n"
	if got != want {
		t.Fatalf("note =\n%q\nwant\n%q", got, want)
	}
	for _, sub := range []string{
		"captured to Inbox.md",
		"- 2026-08-17 09:30 call the dentist",
		"journal_id: ",
		"undo restores all of Inbox.md",
		"after later edits to it 'bn undo' refuses",
		"'bn undo --force' overwrites those edits",
	} {
		if !strings.Contains(out, sub) {
			t.Errorf("output missing %q:\n%s", sub, out)
		}
	}
}

func TestCapture_Stdin(t *testing.T) {
	root := buildCaptureFixture(t, map[string]string{
		"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n",
	})

	out, err := runCLI(t, "  pasted   thought \n", "capture")
	if err != nil {
		t.Fatalf("capture: %v\n%s", err, out)
	}

	got := readNote(t, filepath.Join(root, "Inbox.md"))
	want := "---\ntitle: Inbox\n---\n## Captures\n- 2026-08-17 09:30 pasted thought\n"
	if got != want {
		t.Fatalf("note =\n%q\nwant\n%q", got, want)
	}
}

func TestCapture_NoteOverride(t *testing.T) {
	root := buildCaptureFixture(t, map[string]string{
		"Inbox.md":         "---\ntitle: Inbox\n---\n## Captures\n",
		"Scratch/Later.md": "---\ntitle: Later\n---\n## Captures\n",
	})

	if out, err := runCLI(t, "", "capture", "--note", "Scratch/Later", "one-off idea"); err != nil {
		t.Fatalf("capture: %v\n%s", err, out)
	}

	if got := readNote(t, filepath.Join(root, "Scratch", "Later.md")); !strings.Contains(got, "- 2026-08-17 09:30 one-off idea") {
		t.Fatalf("override note not appended to:\n%s", got)
	}
	if got := readNote(t, filepath.Join(root, "Inbox.md")); strings.Contains(got, "one-off idea") {
		t.Fatalf("configured inbox was written:\n%s", got)
	}
}

func TestCapture_HelpStatesUndoConflictGuard(t *testing.T) {
	help := NewCaptureCmd().Long
	for _, sub := range []string{
		"Ordinary 'bn undo' is fail-fast",
		"if the note\nchanged after the capture, it refuses and writes nothing",
		"'bn undo --force'\nrestores the whole snapshot and overwrites those later edits",
	} {
		if !strings.Contains(help, sub) {
			t.Errorf("help missing %q:\n%s", sub, help)
		}
	}
}

func TestCapture_BootstrapsMissingNote(t *testing.T) {
	root := buildCaptureFixture(t, nil)

	out, err := runCLI(t, "", "capture", "first thought")
	if err != nil {
		t.Fatalf("capture: %v\n%s", err, out)
	}
	if !strings.Contains(out, "captured to Inbox.md (created)") {
		t.Errorf("output does not report the created note:\n%s", out)
	}

	got := readNote(t, filepath.Join(root, "Inbox.md"))
	for _, sub := range []string{"title: Inbox", "## Captures", "- 2026-08-17 09:30 first thought"} {
		if !strings.Contains(got, sub) {
			t.Errorf("bootstrapped note missing %q:\n%s", sub, got)
		}
	}
}

func TestCapture_EmptyTextRejected(t *testing.T) {
	root := buildCaptureFixture(t, map[string]string{
		"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n",
	})
	original := readNote(t, filepath.Join(root, "Inbox.md"))

	t.Run("blank args", func(t *testing.T) {
		if _, err := runCLI(t, "", "capture", "   "); err == nil {
			t.Fatal("expected an error for blank text")
		}
	})
	t.Run("blank stdin", func(t *testing.T) {
		if _, err := runCLI(t, "   \n", "capture"); err == nil {
			t.Fatal("expected an error for blank stdin")
		}
	})

	if got := readNote(t, filepath.Join(root, "Inbox.md")); got != original {
		t.Fatalf("note changed:\n%s", got)
	}
}

func TestCaptureDryRun_NewHeading(t *testing.T) {
	root := buildCaptureFixture(t, map[string]string{
		"Inbox.md": "---\ntitle: Inbox\n---\nExisting body.\n",
	})
	notePath := filepath.Join(root, "Inbox.md")
	original := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "capture", "--dry-run", "buy milk")
		if err != nil {
			t.Fatalf("capture --dry-run: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "capture_dryrun_new_heading.golden", out)
	if got := readNote(t, notePath); got != original {
		t.Fatalf("file changed:\n--- before ---\n%s--- after ---\n%s", original, got)
	}
}

func TestCaptureDryRun_ExistingHeading(t *testing.T) {
	root := buildCaptureFixture(t, map[string]string{
		"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n- 2026-08-16 08:00 older\n",
	})
	notePath := filepath.Join(root, "Inbox.md")
	original := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "capture", "--dry-run", "buy milk")
		if err != nil {
			t.Fatalf("capture --dry-run: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "capture_dryrun_existing_heading.golden", out)
	if got := readNote(t, notePath); got != original {
		t.Fatalf("file changed:\n--- before ---\n%s--- after ---\n%s", original, got)
	}
}
