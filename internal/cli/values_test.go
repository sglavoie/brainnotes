package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildValuesFixture writes 7 notes used by the values/tags tests:
//   - Note1/2/3: tags: [project, meeting]
//   - Note4:     tags: [project, reading]
//   - Note5:     tags: [projetc]              (typo, surfaces in long tail)
//   - Note6:     category: "[[Project Alpha]]" (no tags)
//   - Note7:     no relevant frontmatter
func buildValuesFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Note1.md": "---\ntags:\n  - project\n  - meeting\n---\nBody.\n",
		"Note2.md": "---\ntags:\n  - project\n  - meeting\n---\nBody.\n",
		"Note3.md": "---\ntags:\n  - project\n  - meeting\n---\nBody.\n",
		"Note4.md": "---\ntags:\n  - project\n  - reading\n---\nBody.\n",
		"Note5.md": "---\ntags:\n  - projetc\n---\nBody.\n",
		"Note6.md": "---\ncategory: \"[[Project Alpha]]\"\n---\nBody.\n",
		"Note7.md": "---\ntitle: Note7\n---\nBody.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	markVaultRoot(t, root, "")
	return root
}

// runValuesCmd invokes the root command and returns stdout, stderr, and the
// execute error. Each test isolates env via isolateBnEnv.
func runValuesCmd(t *testing.T, vaultRoot string, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errBuf)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func readGoldenBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return data
}

func TestValues_TagsDefaultCountDesc(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, stderr, err := runValuesCmd(t, root, "tags")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	want := string(readGoldenBytes(t, "values_count_desc.golden"))
	if stdout != want {
		t.Fatalf("default sort:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_TagsSortName(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, _, err := runValuesCmd(t, root, "tags", "--sort=name")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := string(readGoldenBytes(t, "values_sort_name.golden"))
	if stdout != want {
		t.Fatalf("--sort=name:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_TagsValuesOnly(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, _, err := runValuesCmd(t, root, "tags", "--count=false")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "project\nmeeting\nprojetc\nreading\n"
	if stdout != want {
		t.Fatalf("--count=false:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_CategoryWikilink(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, _, err := runValuesCmd(t, root, "values", "category")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "1  [[Project Alpha]]\n"
	if stdout != want {
		t.Fatalf("values category:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_MissingPropertyExitsZero(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, stderr, err := runValuesCmd(t, root, "values", "nonexistent")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestValues_FzfPickProperty(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	orig := propertyPicker
	t.Cleanup(func() { propertyPicker = orig })
	propertyPicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{"tags"}, nil
	}

	fzfOut, _, err := runValuesCmd(t, root, "values")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	explicitOut, _, err := runValuesCmd(t, root, "values", "tags")
	if err != nil {
		t.Fatalf("execute explicit: %v", err)
	}
	if fzfOut != explicitOut {
		t.Fatalf("fzf output diverges from explicit:\n fzf=%q\nexplicit=%q", fzfOut, explicitOut)
	}
}

func TestValues_FzfAbort(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	orig := propertyPicker
	t.Cleanup(func() { propertyPicker = orig })
	propertyPicker = func(items []string, extraArgs []string) ([]string, error) {
		return nil, nil
	}

	stdout, stderr, err := runValuesCmd(t, root, "values")
	if !errors.Is(err, ErrNothingSelected) || err.Error() != "values: nothing selected" {
		t.Fatalf("err = %v, want values: nothing selected (exit 1)", err)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout on abort, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr on abort, got %q", stderr)
	}
}

func TestValues_FzfNoProperties(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	full := filepath.Join(root, "Empty.md")
	if err := os.WriteFile(full, []byte("No frontmatter here.\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	orig := propertyPicker
	t.Cleanup(func() { propertyPicker = orig })
	pickerCalled := false
	propertyPicker = func(items []string, extraArgs []string) ([]string, error) {
		pickerCalled = true
		return nil, nil
	}

	stdout, _, err := runValuesCmd(t, root, "values")
	if !errors.Is(err, ErrNothingSelected) || err.Error() != "values: no properties in vault" {
		t.Fatalf("err = %v, want values: no properties in vault (exit 1)", err)
	}
	if pickerCalled {
		t.Fatalf("picker should not be called when no properties exist")
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
}

func TestValues_InvalidSortFlag(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	_, _, err := runValuesCmd(t, root, "values", "--sort=bogus", "tags")
	if err == nil {
		t.Fatalf("expected error for --sort=bogus")
	}
	if !strings.Contains(err.Error(), "sort") {
		t.Fatalf("error should mention --sort; got %v", err)
	}
}

func TestValues_TagsAliasMatchesValuesTags(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	aliasOut, _, err := runValuesCmd(t, root, "tags")
	if err != nil {
		t.Fatalf("execute tags: %v", err)
	}
	longOut, _, err := runValuesCmd(t, root, "values", "tags")
	if err != nil {
		t.Fatalf("execute values tags: %v", err)
	}
	if aliasOut != longOut {
		t.Fatalf("alias output diverges from long form:\n tags=%q\nvalues=%q", aliasOut, longOut)
	}
}

func TestValues_TagsFiles(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, stderr, err := runValuesCmd(t, root, "tags", "--files")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	want := string(readGoldenBytes(t, "values_files_count_desc.golden"))
	if stdout != want {
		t.Fatalf("--files default sort:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_TagsFilesSortName(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, _, err := runValuesCmd(t, root, "tags", "--files", "--sort=name")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := string(readGoldenBytes(t, "values_files_sort_name.golden"))
	if stdout != want {
		t.Fatalf("--files --sort=name:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_TagsFilesNoCount(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, _, err := runValuesCmd(t, root, "tags", "--files", "--count=false")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := string(readGoldenBytes(t, "values_files_no_count.golden"))
	if stdout != want {
		t.Fatalf("--files --count=false:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_FilesAliasMatch(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	aliasOut, _, err := runValuesCmd(t, root, "tags", "--files")
	if err != nil {
		t.Fatalf("execute tags --files: %v", err)
	}
	longOut, _, err := runValuesCmd(t, root, "values", "tags", "--files")
	if err != nil {
		t.Fatalf("execute values tags --files: %v", err)
	}
	if aliasOut != longOut {
		t.Fatalf("alias output diverges from long form:\n tags=%q\nvalues=%q", aliasOut, longOut)
	}
}

func TestValues_CategoryFiles(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, _, err := runValuesCmd(t, root, "values", "category", "--files")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "1  [[Project Alpha]]:\n  Note6.md\n"
	if stdout != want {
		t.Fatalf("values category --files:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_FilesDedup(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	body := "---\ntags:\n  - dup\n  - dup\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(root, "Duped.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout, _, err := runValuesCmd(t, root, "tags", "--files")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "2  dup:\n  Duped.md\n"
	if stdout != want {
		t.Fatalf("dedup:\n got=%q\nwant=%q", stdout, want)
	}
}

func TestValues_FilesMissingProperty(t *testing.T) {
	isolateBnEnv(t)
	root := buildValuesFixture(t)

	stdout, stderr, err := runValuesCmd(t, root, "values", "nonexistent", "--files")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

// buildMapElementVault is a vault whose one note carries a map element inside
// its tags list, which `values` skips.
func buildMapElementVault(t *testing.T) string {
	t.Helper()
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	body := "---\ntags:\n  - real\n  - {nested: oops}\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(root, "Mixed.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write Mixed.md: %v", err)
	}
	return root
}

// TestValues_MapElementSkippedWithoutVerbose verifies that a map element nested
// inside a list value is silently skipped (no warning, count unaffected) when
// -v is not passed.
func TestValues_MapElementSkippedWithoutVerbose(t *testing.T) {
	root := buildMapElementVault(t)
	stdout, stderr, err := runValuesCmd(t, root, "tags")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr without -v, got %q", stderr)
	}
	if stdout != "1  real\n" {
		t.Fatalf("counts should ignore map element:\n got=%q\nwant=%q", stdout, "1  real\n")
	}
}

// TestValues_MapElementWarnsWithVerbose verifies that with -v exactly one
// warning per affected note hits stderr, and the counts are unchanged.
func TestValues_MapElementWarnsWithVerbose(t *testing.T) {
	root := buildMapElementVault(t)
	stdout, stderr, err := runValuesCmd(t, root, "tags", "-v")
	if err != nil {
		t.Fatalf("execute with -v: %v", err)
	}
	if stdout != "1  real\n" {
		t.Fatalf("counts should be unchanged by -v:\n got=%q\nwant=%q", stdout, "1  real\n")
	}
	if want := "tags: warning: skipped map-typed value in Mixed.md\n"; stderr != want {
		t.Fatalf("expected exactly one warning under the running command, vault-relative:\n got=%q\nwant=%q", stderr, want)
	}

	_, stderr, err = runValuesCmd(t, root, "values", "tags", "-v")
	if err != nil {
		t.Fatalf("execute values -v: %v", err)
	}
	if want := "values: warning: skipped map-typed value in Mixed.md\n"; stderr != want {
		t.Fatalf("values -v warning:\n got=%q\nwant=%q", stderr, want)
	}
}
