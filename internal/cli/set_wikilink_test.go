package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetCategoriesWikilinkList(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "categories=Books,Personal")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "- '[[Books]]'") && !strings.Contains(got, "- \"[[Books]]\"") && !strings.Contains(got, "- [[Books]]") {
		t.Fatalf("expected wikilink-wrapped Books, got:\n%s", got)
	}
	if !strings.Contains(got, "Personal") {
		t.Fatalf("expected Personal entry, got:\n%s", got)
	}
}

func TestSetWikilinkPropertySkipsWriteWhenWrapped(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "parent=Foo"); err != nil {
		t.Fatalf("first set: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "parent: '[[Foo]]'") && !strings.Contains(got, "parent: \"[[Foo]]\"") && !strings.Contains(got, "parent: [[Foo]]") {
		t.Fatalf("expected wrapped parent, got:\n%s", got)
	}

	past := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(notePath, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	out, err := runCLI(t, "", "set", notePath, "parent=Foo")
	if err != nil {
		t.Fatalf("second set: %v\n%s", err, out)
	}
	if strings.Contains(out, "updated") {
		t.Errorf("expected no `updated` for wikilink no-op, got %q", out)
	}
	after, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(past) {
		t.Errorf("mtime changed on wikilink no-op: want=%v got=%v", past, after.ModTime())
	}
}

func TestSetAdd_WikilinkPropertyWraps(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "categories+=Books")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "[[Books]]") {
		t.Fatalf("expected wikilink-wrapped Books, got:\n%s", got)
	}

	infoBefore, err := os.Stat(notePath)
	if err != nil {
		t.Fatal(err)
	}
	before := infoBefore.ModTime()
	if _, err := runCLI(t, "", "set", notePath, "categories+=Books"); err != nil {
		t.Fatal(err)
	}
	infoAfter, err := os.Stat(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if !infoAfter.ModTime().Equal(before) {
		t.Fatalf("expected mtime unchanged on repeat wikilink +=, before=%v after=%v", before, infoAfter.ModTime())
	}
}

func TestSetRemove_WikilinkPropertyWraps(t *testing.T) {
	root := t.TempDir()
	notePath := filepath.Join(root, "wl.md")
	if err := os.WriteFile(notePath, []byte("---\ncategories:\n  - \"[[Books]]\"\n  - \"[[Personal]]\"\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	useVaultRoot(t, root, testPropertyDefinitions)

	if _, err := runCLI(t, "", "set", notePath, "categories-=Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if strings.Contains(got, "[[Books]]") {
		t.Fatalf("expected [[Books]] removed, got:\n%s", got)
	}
	if !strings.Contains(got, "[[Personal]]") {
		t.Fatalf("expected [[Personal]] to remain, got:\n%s", got)
	}
}

func TestSetOp_WikilinkScalarSkipsAutoCoerce(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "parent=true"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "[[true]]") {
		t.Fatalf("expected parent stored as [[true]] (string), got:\n%s", got)
	}
	if strings.Contains(got, "parent: true\n") {
		t.Fatalf("auto-coercion should be skipped for wikilink properties, got:\n%s", got)
	}
}

func TestSetOp_WikilinkListSkipsAutoCoerce(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "categories=true,42"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "[[true]]") {
		t.Fatalf("expected [[true]] in categories, got:\n%s", got)
	}
	if !strings.Contains(got, "[[42]]") {
		t.Fatalf("expected [[42]] in categories, got:\n%s", got)
	}
}

func TestSetOp_WikilinkAddSkipsAutoCoerce(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "categories+=true"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "[[true]]") {
		t.Fatalf("expected [[true]] in categories, got:\n%s", got)
	}
}
