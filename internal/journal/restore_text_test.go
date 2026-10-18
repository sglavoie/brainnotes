package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRestoreText(t *testing.T) {
	root := t.TempDir()
	journalRoot := filepath.Join(root, ".bnotes", "journal")
	entry := recordTwoFileEntry(t, root, journalRoot)
	writeFile(t, filepath.Join(root, "First.md"), "newer work\n")
	result, err := ReadRestoreText(root, journalRoot, entry.ID, "First.md", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != "newer work\n" || result.Restored != "first-before\n" || !strings.HasPrefix(result.CurrentDigest, "sha256:") {
		t.Fatalf("comparison = %+v", result)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "First.md")); string(got) != "newer work\n" {
		t.Fatal("preview wrote the note")
	}
	if _, err := ReadRestoreText(root, journalRoot, entry.ID, "../outside", 1024); err == nil {
		t.Fatal("unrecorded path accepted")
	}
}

func TestReadRestoreTextRefusals(t *testing.T) {
	cases := []struct {
		name, content string
		limit         int64
	}{
		{"too large", "longer than the limit", 4},
		{"binary", "before\x00after", 1024},
		{"invalid utf8", "\xff", 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			jr := filepath.Join(root, ".bnotes", "journal")
			entry := recordTwoFileEntry(t, root, jr)
			writeFile(t, filepath.Join(root, "First.md"), tc.content)
			if _, err := ReadRestoreText(root, jr, entry.ID, "First.md", tc.limit); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestReadRestoreTextMissingAndTamperedSnapshot(t *testing.T) {
	root := t.TempDir()
	jr := filepath.Join(root, ".bnotes", "journal")
	entry := recordTwoFileEntry(t, root, jr)
	if err := os.Remove(filepath.Join(root, "First.md")); err != nil {
		t.Fatal(err)
	}
	result, err := ReadRestoreText(root, jr, entry.ID, "First.md", 1024)
	if err != nil || result.Current != "" || result.CurrentDigest != "" {
		t.Fatalf("missing current: %+v, %v", result, err)
	}
	for _, file := range entry.Files {
		if file.Path == "First.md" {
			writeFile(t, filepath.Join(jr, entry.ID, file.Before.Snapshot), "tampered\n")
		}
	}
	if _, err := ReadRestoreText(root, jr, entry.ID, "First.md", 1024); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
}

func TestReadRestoreTextRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	jr := filepath.Join(root, ".bnotes", "journal")
	entry := recordTwoFileEntry(t, root, jr)
	path := filepath.Join(root, "First.md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Second.md"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRestoreText(root, jr, entry.ID, "First.md", 1024); err == nil {
		t.Fatal("symlink accepted")
	}
}
