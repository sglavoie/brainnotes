package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// assertMtimeUnchanged captures the mtime of every path before fn runs and
// fails the test if any of them differs afterward — used to verify that
// --dry-run never touches disk.
func assertMtimeUnchanged(t *testing.T, paths []string, fn func()) {
	t.Helper()
	before := make([]os.FileInfo, len(paths))
	for i, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		before[i] = fi
	}
	fn()
	for i, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if !fi.ModTime().Equal(before[i].ModTime()) {
			t.Fatalf("mtime of %s changed: before=%v after=%v", p, before[i].ModTime(), fi.ModTime())
		}
	}
}

func assertGolden(t *testing.T, goldenName, got string) {
	t.Helper()
	path := filepath.Join("testdata", goldenName)
	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if got != string(wantBytes) {
		t.Fatalf("output mismatch with %s:\n--- want ---\n%s--- got ---\n%s", path, string(wantBytes), got)
	}
}

func TestSetDryRun_NewKey(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	originalBody := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "set", "--dry-run", notePath, "status=reading")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "set_dryrun_newkey.golden", out)
	if got := readNote(t, notePath); got != originalBody {
		t.Fatalf("file body changed:\n--- before ---\n%s\n--- after ---\n%s", originalBody, got)
	}
}

func TestSetDryRun_ListAppend(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")
	originalBody := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "set", "--dry-run", notePath, "tags+=baz")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "set_dryrun_listappend.golden", out)
	if got := readNote(t, notePath); got != originalBody {
		t.Fatalf("file body changed")
	}
}

func TestSetDryRun_IdempotentAppendPrintsNothing(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "set", "--dry-run", notePath, "tags+=foo")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
}

func TestSetDryRun_MultiNoteStableOrder(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")
	stdin := bPath + "\n" + aPath + "\n"

	var out string
	assertMtimeUnchanged(t, []string{aPath, bPath}, func() {
		o, err := runCLI(t, stdin, "set", "--dry-run", "-", "status=done")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "set_dryrun_multinote.golden", out)
}

func TestUnsetDryRun_ExistingKey(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")
	originalBody := readNote(t, notePath)

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "unset", "--dry-run", notePath, "tags")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "unset_dryrun_existing.golden", out)
	if got := readNote(t, notePath); got != originalBody {
		t.Fatalf("file body changed")
	}
}

func TestUnsetDryRun_MissingKeyPrintsNothing(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "unset", "--dry-run", notePath, "missing")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
}
