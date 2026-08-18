package noteops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

const captureHeading = "## Captures"

func captureRequest(text string, minute int) CaptureRequest {
	return CaptureRequest{
		Heading: captureHeading,
		Text:    text,
		At:      time.Date(2026, 8, 17, 9, minute, 0, 0, time.UTC),
	}
}

func TestApplyCaptureBootstrapsInbox(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	result, err := ApplyCapture(root, "Inbox.md", captureRequest("buy milk", 30), nil)
	if err != nil {
		t.Fatalf("ApplyCapture: %v", err)
	}
	if !result.CreatedNote || !result.AddedHeading {
		t.Fatalf("created=%v addedHeading=%v, want both true", result.CreatedNote, result.AddedHeading)
	}
	if result.Line != "- 2026-08-17 09:30 buy milk" {
		t.Fatalf("line = %q", result.Line)
	}
	if result.LineNumber != 2 {
		t.Fatalf("line number = %d, want 2", result.LineNumber)
	}
	if result.Destination.RelPath != "Inbox.md" {
		t.Fatalf("destination = %q, want Inbox.md", result.Destination.RelPath)
	}
	assertCreateTestFile(t, root, "Inbox.md",
		"---\ntitle: Inbox\n---\n## Captures\n- 2026-08-17 09:30 buy milk\n")
}

func TestApplyCaptureAppendsUnderExistingHeading(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := ApplyCapture(root, "Inbox.md", captureRequest("first", 30), nil); err != nil {
		t.Fatalf("first ApplyCapture: %v", err)
	}
	result, err := ApplyCapture(root, "Inbox.md", captureRequest("second", 45), nil)
	if err != nil {
		t.Fatalf("second ApplyCapture: %v", err)
	}
	if result.CreatedNote || result.AddedHeading {
		t.Fatalf("created=%v addedHeading=%v, want both false", result.CreatedNote, result.AddedHeading)
	}
	if result.LineNumber != 3 {
		t.Fatalf("line number = %d, want 3", result.LineNumber)
	}
	assertCreateTestFile(t, root, "Inbox.md",
		"---\ntitle: Inbox\n---\n## Captures\n- 2026-08-17 09:30 first\n- 2026-08-17 09:45 second\n")
}

// TestApplyCaptureLeavesEverythingElseByteIdentical pins the whole point of the
// splice: unsorted, flow-style frontmatter that Marshal would rewrite survives
// untouched, and so does the prose after the capture section.
func TestApplyCaptureLeavesEverythingElseByteIdentical(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const original = "---\ntitle: Inbox\nalpha: [one, two]\n---\n" +
		"# Inbox\n\n## Captures\n- old capture\n\n## Later\n\nTrailing prose.\n"
	writeCreateTestFile(t, root, "Inbox.md", original)

	result, err := ApplyCapture(root, "Inbox.md", captureRequest("buy milk", 30), nil)
	if err != nil {
		t.Fatalf("ApplyCapture: %v", err)
	}
	if result.LineNumber != 5 {
		t.Fatalf("line number = %d, want 5", result.LineNumber)
	}
	assertCreateTestFile(t, root, "Inbox.md", "---\ntitle: Inbox\nalpha: [one, two]\n---\n"+
		"# Inbox\n\n## Captures\n- old capture\n- 2026-08-17 09:30 buy milk\n\n## Later\n\nTrailing prose.\n")
}

func TestPreviewCaptureWritesNothing(t *testing.T) {
	t.Parallel()

	t.Run("bootstrap", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		result, err := PreviewCapture(root, "Inbox.md", captureRequest("buy milk", 30))
		if err != nil {
			t.Fatalf("PreviewCapture: %v", err)
		}
		if !result.CreatedNote || result.Journal != nil {
			t.Fatalf("created=%v journal=%v, want true and nil", result.CreatedNote, result.Journal)
		}
		if _, err := os.Stat(filepath.Join(root, "Inbox.md")); !os.IsNotExist(err) {
			t.Fatalf("preview wrote a file: err=%v", err)
		}
		var out strings.Builder
		if err := RenderNotePreview(&out, result.Preview); err != nil {
			t.Fatalf("RenderNotePreview: %v", err)
		}
		want := "--- Inbox.md\npath:  -> Inbox.md\nbody:2:  -> - 2026-08-17 09:30 buy milk\n"
		if out.String() != want {
			t.Fatalf("preview =\n%q\nwant\n%q", out.String(), want)
		}
	})

	t.Run("existing note", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		const original = "---\ntitle: Inbox\n---\n## Captures\n- old capture\n"
		writeCreateTestFile(t, root, "Inbox.md", original)

		result, err := PreviewCapture(root, "Inbox.md", captureRequest("buy milk", 30))
		if err != nil {
			t.Fatalf("PreviewCapture: %v", err)
		}
		if result.CreatedNote {
			t.Fatal("created = true, want false")
		}
		assertCreateTestFile(t, root, "Inbox.md", original)
		var out strings.Builder
		if err := RenderNotePreview(&out, result.Preview); err != nil {
			t.Fatalf("RenderNotePreview: %v", err)
		}
		want := "--- Inbox.md\nbody:3:  -> - 2026-08-17 09:30 buy milk\n"
		if out.String() != want {
			t.Fatalf("preview =\n%q\nwant\n%q", out.String(), want)
		}
	})
}

func TestApplyCaptureJournalsBootstrapAsMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	result, err := ApplyCapture(root, "Inbox.md", captureRequest("buy milk", 30), nil,
		WithCaptureJournalOptions(
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-capture", nil }),
		))
	if err != nil {
		t.Fatalf("ApplyCapture: %v", err)
	}
	if result.Journal == nil {
		t.Fatal("journal entry = nil, want entry")
	}
	if result.Journal.ID != "entry-capture" {
		t.Fatalf("journal ID = %q, want entry-capture", result.Journal.ID)
	}
	rec := createRecordForPath(t, *result.Journal, "Inbox.md")
	if rec.Before.Kind != journal.KindMissing || rec.After.Kind != journal.KindFile {
		t.Fatalf("journal record = %#v, want missing -> file", rec)
	}
	if _, err := os.Stat(filepath.Join(JournalRoot(root), "entry-capture", "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}
}

func TestApplyCaptureJournalsAppendAsFileToFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCreateTestFile(t, root, "Inbox.md", "---\ntitle: Inbox\n---\n## Captures\n- old capture\n")

	result, err := ApplyCapture(root, "Inbox.md", captureRequest("buy milk", 30), nil,
		WithCaptureCommand("bn capture"),
		WithCaptureJournalOptions(
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-append", nil }),
		))
	if err != nil {
		t.Fatalf("ApplyCapture: %v", err)
	}
	if result.Journal == nil {
		t.Fatal("journal entry = nil, want entry")
	}
	if result.Journal.Command != "bn capture" {
		t.Fatalf("journal command = %q, want bn capture", result.Journal.Command)
	}
	rec := createRecordForPath(t, *result.Journal, "Inbox.md")
	if rec.Before.Kind != journal.KindFile || rec.After.Kind != journal.KindFile {
		t.Fatalf("journal record = %#v, want file -> file", rec)
	}
	if rec.Before.Snapshot == "" {
		t.Fatal("journal record has no before snapshot")
	}
}

func TestApplyCaptureRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		relPath string
		req     CaptureRequest
		want    ErrorCode
	}{
		{
			name:    "empty text",
			relPath: "Inbox.md",
			req:     CaptureRequest{Heading: captureHeading, Text: "   ", At: at},
			want:    CodeInvalidRequest,
		},
		{
			name:    "zero timestamp",
			relPath: "Inbox.md",
			req:     CaptureRequest{Heading: captureHeading, Text: "buy milk"},
			want:    CodeInvalidRequest,
		},
		{
			name:    "heading is not an ATX heading",
			relPath: "Inbox.md",
			req:     CaptureRequest{Heading: "Captures", Text: "buy milk", At: at},
			want:    CodeInvalidRequest,
		},
		{
			name:    "path escapes the vault",
			relPath: "../Inbox.md",
			req:     CaptureRequest{Heading: captureHeading, Text: "buy milk", At: at},
			want:    CodeForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if _, err := PreviewCapture(root, tc.relPath, tc.req); CodeOf(err) != tc.want {
				t.Fatalf("PreviewCapture code = %q, want %q (%v)", CodeOf(err), tc.want, err)
			}
			if _, err := ApplyCapture(root, tc.relPath, tc.req, nil); CodeOf(err) != tc.want {
				t.Fatalf("ApplyCapture code = %q, want %q (%v)", CodeOf(err), tc.want, err)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("vault not empty after rejected capture: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestApplyCaptureRefusesUnparseableNote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const broken = "---\ntitle: [unclosed\n---\n## Captures\n"
	writeCreateTestFile(t, root, "Inbox.md", broken)

	_, err := ApplyCapture(root, "Inbox.md", captureRequest("buy milk", 30), nil)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	assertCreateTestFile(t, root, "Inbox.md", broken)
}
