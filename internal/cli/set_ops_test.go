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
	// foo should still appear exactly once.
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

func TestSetOp_RejectsNonListKey(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	_, err := runCLI(t, "", "set", notePath, "status+=foo")
	if err == nil {
		t.Fatal("expected error for += on non-list key")
	}
	msg := err.Error()
	if !strings.Contains(msg, "status") || !strings.Contains(msg, "list_properties") {
		t.Fatalf("error should mention key and config knob, got: %v", err)
	}
}

func TestSetOp_RejectsEmptyValue(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	if _, err := runCLI(t, "", "set", notePath, "tags+="); err == nil {
		t.Fatal("expected error for += with empty value")
	}
	if _, err := runCLI(t, "", "set", notePath, "tags-="); err == nil {
		t.Fatal("expected error for -= with empty value")
	}
}

func TestSetOp_RejectsOnExistingScalar(t *testing.T) {
	// Force the case where a configured list_property already holds a scalar
	// value in the file: '+=' should report a 'not a list' error.
	root := t.TempDir()
	notePath := filepath.Join(root, "scalar.md")
	if err := os.WriteFile(notePath, []byte("---\ntags: lonely\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("vault = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	_, err := runCLI(t, "", "set", notePath, "tags+=foo")
	if err == nil {
		t.Fatal("expected error when += targets a scalar")
	}
	if !strings.Contains(err.Error(), "not a list") {
		t.Fatalf("expected 'not a list' error, got: %v", err)
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

	// And '+=' of the same value should be idempotent because the stored
	// entry already matches the wrapped form.
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
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("vault = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

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
	// All notes at the root should have been considered. At least a.md, b.md,
	// note.md, empty.md, tags.md are present.
	for _, name := range []string{"a.md", "b.md", "note.md", "empty.md", "tags.md"} {
		got := readNote(t, filepath.Join(root, name))
		if !strings.Contains(got, "- glob") {
			t.Fatalf("%s missing - glob:\n%s", name, got)
		}
	}
	_ = out
}

func TestSetOp_RejectsInvalidPair(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	_, err := runCLI(t, "", "set", notePath, "=foo")
	if err == nil {
		t.Fatal("expected error for pair with empty key")
	}
}

// TestSetOp_WikilinkScalarSkipsAutoCoerce verifies that wikilink-typed scalar
// properties skip auto-coercion. Without the fix, `bn set note.md parent=true`
// stores the Go bool `true` instead of the string `[[true]]`.
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

// TestSetOp_WikilinkListSkipsAutoCoerce verifies that wikilink-typed list
// properties skip auto-coercion. Before the fix, `bn set note.md
// categories=true,42` stored a bool and an int instead of wrapped strings.
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

// TestSetOp_WikilinkAddSkipsAutoCoerce verifies that += on a wikilink-typed
// list property skips auto-coercion, so values like "true" are stored as
// the string "[[true]]" rather than the Go bool true.
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

// TestSetOps_OpposingAddRemoveIsNoOp verifies that opposing operations in the
// same invocation (e.g. tags+=foo followed by tags-=foo) produce no file write
// when the net effect is zero. Before the fix, the per-operation `changed` flag
// was set to true by the first operation and the file was rewritten even though
// the final state matched the initial state.
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

// TestSetOps_OpposingAddRemoveDryRunIsNoOp verifies the dry-run path also
// correctly reports no change for opposing operations.
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
