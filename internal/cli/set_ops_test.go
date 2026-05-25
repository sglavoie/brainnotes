package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetAdd_AppendsNewValue(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "set", notePath, "tags+=baz")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	for _, want := range []string{"- foo", "- bar", "- baz"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestSetAdd_IdempotentSkipsSave(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	infoBefore, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	before := infoBefore.ModTime()

	out, err := runCLI(t, "", "set", notePath, "tags+=foo")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if strings.Contains(out, "updated") {
		t.Fatalf("expected no updated report on no-op, got %q", out)
	}
	infoAfter, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !infoAfter.ModTime().Equal(before) {
		t.Fatalf("expected mtime unchanged on idempotent +=, before=%v after=%v", before, infoAfter.ModTime())
	}
}

func TestSetAdd_CreatesListWhenKeyAbsent(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "tags+=alpha,beta")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	if !strings.Contains(got, "tags:") || !strings.Contains(got, "- alpha") || !strings.Contains(got, "- beta") {
		t.Fatalf("expected new tags list, got:\n%s", got)
	}
}

func TestSetAdd_MixOfNewAndExisting(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "set", notePath, "tags+=foo,baz")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	if c := strings.Count(got, "- foo"); c != 1 {
		t.Fatalf("expected exactly one '- foo' entry, got %d:\n%s", c, got)
	}
	if !strings.Contains(got, "- baz") {
		t.Fatalf("expected baz appended:\n%s", got)
	}
}

func TestSetRemove_DropsValue(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "set", notePath, "tags-=foo")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	if strings.Contains(got, "- foo") {
		t.Fatalf("foo should be removed:\n%s", got)
	}
	if !strings.Contains(got, "- bar") {
		t.Fatalf("bar should remain:\n%s", got)
	}
}

func TestSetRemove_MissingValueIsNoOp(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	infoBefore, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	before := infoBefore.ModTime()

	out, err := runCLI(t, "", "set", notePath, "tags-=nope")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if strings.Contains(out, "updated") {
		t.Fatalf("expected no updated report on no-op -=, got %q", out)
	}
	infoAfter, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !infoAfter.ModTime().Equal(before) {
		t.Fatalf("expected mtime unchanged on -= of missing value, before=%v after=%v", before, infoAfter.ModTime())
	}
}

func TestSetRemove_AbsentKeyIsNoOp(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	out, err := runCLI(t, "", "set", notePath, "tags-=anything")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if strings.Contains(out, "updated") {
		t.Fatalf("expected no updated report when key absent, got %q", out)
	}
}

func TestSetOps_CombinedAddAndRemove(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "set", notePath, "tags+=alpha", "tags-=foo")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	if strings.Contains(got, "- foo") {
		t.Fatalf("foo should be removed:\n%s", got)
	}
	if !strings.Contains(got, "- alpha") || !strings.Contains(got, "- bar") {
		t.Fatalf("expected alpha appended and bar retained:\n%s", got)
	}
}

func TestSetOps_MixedOperatorsAcrossKeys(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "set", notePath, "tags+=baz", "status=reading", "tags-=bar")
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
	if !strings.Contains(got, "- foo") || !strings.Contains(got, "- baz") {
		t.Fatalf("expected foo retained and baz appended:\n%s", got)
	}
	if strings.Contains(got, "- bar") {
		t.Fatalf("bar should be removed:\n%s", got)
	}
}

func TestSetOps_MultiNoteViaStdin(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")
	stdin := aPath + "\n" + bPath + "\n"

	out, err := runCLI(t, stdin, "set", "-", "tags+=shared")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, aPath+": updated") || !strings.Contains(out, bPath+": updated") {
		t.Fatalf("expected both paths updated, got %q", out)
	}
	for _, p := range []string{aPath, bPath} {
		got := readNote(t, p)
		if !strings.Contains(got, "- shared") {
			t.Fatalf("%s missing - shared\n%s", p, got)
		}
	}
}

func TestSetOps_GlobExpansion(t *testing.T) {
	root := buildSetFixture(t)

	out, err := runCLI(t, "", "set", "*.md", "tags+=glob")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, name := range []string{"a.md", "b.md", "note.md", "empty.md", "tags.md"} {
		got := readNote(t, filepath.Join(root, name))
		if !strings.Contains(got, "- glob") {
			t.Fatalf("%s missing - glob:\n%s", name, got)
		}
	}
	_ = out
}

func TestSetOps_OpposingAddRemoveIsNoOp(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	beforeBytes := readNote(t, notePath)
	beforeInfo, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	out, err := runCLI(t, "", "set", notePath, "tags+=baz", "tags-=baz")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if strings.Contains(out, "updated") {
		t.Errorf("expected no `updated` report for net-zero change, got %q", out)
	}
	afterBytes := readNote(t, notePath)
	if afterBytes != beforeBytes {
		t.Errorf("file bytes changed on net-zero set:\n--- before ---\n%s--- after ---\n%s", beforeBytes, afterBytes)
	}
	afterInfo, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Errorf("mtime changed on net-zero set: before=%v after=%v", beforeInfo.ModTime(), afterInfo.ModTime())
	}
}

func TestSetOps_OpposingAddRemoveDryRunIsNoOp(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "set", "--dry-run", notePath, "tags+=baz", "tags-=baz")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if out != "" {
		t.Errorf("expected empty dry-run output for net-zero change, got %q", out)
	}
}
