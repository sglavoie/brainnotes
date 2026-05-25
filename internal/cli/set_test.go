package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetSimpleScalar(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "status=reading")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "status: reading") {
		t.Fatalf("expected status: reading, got:\n%s", got)
	}
}

func TestSetForcedDate(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "created=2026-01-01", "--type=date")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "created: 2026-01-01") {
		t.Fatalf("expected created: 2026-01-01, got:\n%s", got)
	}
}

func TestSetAutoDateAndInt(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "created=2026-01-01", "rating=7"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "created: 2026-01-01") {
		t.Fatalf("expected auto-date, got:\n%s", got)
	}
	if !strings.Contains(got, "rating: 7") {
		t.Fatalf("expected auto-int, got:\n%s", got)
	}
}

func TestSetSortedKeyOutput(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "empty.md")

	if _, err := runCLI(t, "", "set", notePath, "zeta=1", "alpha=2", "mu=3"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	idxA := strings.Index(got, "alpha:")
	idxM := strings.Index(got, "mu:")
	idxZ := strings.Index(got, "zeta:")
	if idxA < 0 || idxM < 0 || idxZ < 0 {
		t.Fatalf("missing keys, got:\n%s", got)
	}
	if !(idxA < idxM && idxM < idxZ) {
		t.Fatalf("keys not alphabetical: alpha=%d mu=%d zeta=%d\n%s", idxA, idxM, idxZ, got)
	}
}

func TestSetMultipleNotesViaStdin(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")
	stdin := aPath + "\n" + bPath + "\n"

	out, err := runCLI(t, stdin, "set", "-", "status=done")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, aPath+": updated") || !strings.Contains(out, bPath+": updated") {
		t.Fatalf("expected both paths updated, got %q", out)
	}
	for _, p := range []string{aPath, bPath} {
		if got := readNote(t, p); !strings.Contains(got, "status: done") {
			t.Fatalf("%s missing status: done\n%s", p, got)
		}
	}
}

func TestSetForcedRawSkipsCoercion(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "flag=true", "--type=raw"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if strings.Contains(got, "flag: true\n") {
		t.Fatalf("expected raw string, got bool:\n%s", got)
	}
	if !strings.Contains(got, "flag: \"true\"") && !strings.Contains(got, "flag: 'true'") {
		t.Fatalf("expected quoted string flag, got:\n%s", got)
	}
}

func TestSetRejectsOutOfVault(t *testing.T) {
	buildSetFixture(t)
	outside := filepath.Join(t.TempDir(), "outsider.md")
	if err := os.WriteFile(outside, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runCLI(t, "", "set", outside, "status=x")
	if err == nil {
		t.Fatal("expected error for out-of-vault path")
	}
	if !strings.Contains(err.Error(), "outside the vault") {
		t.Fatalf("expected out-of-vault error, got %v", err)
	}
}

func TestSetUnchangedValueSkipsWrite(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "status=reading"); err != nil {
		t.Fatalf("first set: %v", err)
	}
	beforeBytes := readNote(t, notePath)

	past := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(notePath, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	out, err := runCLI(t, "", "set", notePath, "status=reading")
	if err != nil {
		t.Fatalf("second set: %v\n%s", err, out)
	}
	if strings.Contains(out, "updated") {
		t.Errorf("expected no `updated` report for no-op, got %q", out)
	}
	after, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(past) {
		t.Errorf("mtime changed on no-op set: before=%v after=%v", past, after.ModTime())
	}
	if readNote(t, notePath) != beforeBytes {
		t.Errorf("bytes changed on no-op set")
	}
}

func TestSet_FzfPickNotes(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{aPath, bPath}, nil
	}

	out, err := runCLI(t, "", "set", "status=reading")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, p := range []string{aPath, bPath} {
		got := readNote(t, p)
		if !strings.Contains(got, "status: reading") {
			t.Fatalf("%s missing status: reading\n%s", p, got)
		}
	}
}

func TestSet_FzfAbort(t *testing.T) {
	root := buildSetFixture(t)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return nil, nil
	}

	out, err := runCLI(t, "", "set", "status=reading")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	for _, name := range []string{"a.md", "b.md", "note.md"} {
		got := readNote(t, filepath.Join(root, name))
		if strings.Contains(got, "status:") {
			t.Fatalf("%s should not have status, got:\n%s", name, got)
		}
	}
}

func TestSet_FzfNoPairs(t *testing.T) {
	buildSetFixture(t)

	_, err := runCLI(t, "", "set", "justanote.md")
	if err == nil {
		t.Fatal("expected error for no key=value pairs")
	}
	if !strings.Contains(err.Error(), "at least one key=value pair") {
		t.Fatalf("expected key=value pair error, got %v", err)
	}
}
