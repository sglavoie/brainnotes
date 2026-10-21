package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// buildUnparsableFixture is a vault where one note's frontmatter is not valid
// YAML. Every read-only command must still answer from the other notes.
func buildUnparsableFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Alpha.md": "---\ntags:\n  - go\n---\nAlpha links [[Beta]] and says hello.\n",
		"Beta.md":  "---\ntags:\n  - go\n---\nBeta says hello back.\n",
		"bad.md":   "---\ntags: [unclosed\n---\nBad links [[Alpha]] and says hello.\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	useVaultRoot(t, root, `[frontmatter.properties.tags]
type     = "list"
items    = "string"
required = true
`)
	return root
}

func runCLISplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestReadOnlyCommandsSkipUnparsableNote(t *testing.T) {
	buildUnparsableFixture(t)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"find", []string{"find"}, "Alpha.md"},
		{"search", []string{"search", "hello"}, "Beta.md"},
		{"links", []string{"links", "Alpha"}, "Beta"},
		{"backlinks", []string{"backlinks", "Beta"}, "Alpha"},
		{"values", []string{"values", "tags"}, "go"},
		{"graph", []string{"graph"}, "Alpha"},
		{"related", []string{"related", "Alpha"}, "Beta"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runCLISplit(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: unexpected error %v\nstderr: %s", tc.args, err, stderr)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Fatalf("%v: stdout missing %q:\n%s", tc.args, tc.want, stdout)
			}
			if strings.Contains(stdout, "bad.md") {
				t.Fatalf("%v: unparsable note leaked into stdout:\n%s", tc.args, stdout)
			}
			wantWarn := tc.args[0] + ": warning: skipped bad.md: frontmatter: "
			if strings.Count(stderr, wantWarn) != 1 {
				t.Fatalf("%v: stderr = %q, want one line starting %q", tc.args, stderr, wantWarn)
			}
		})
	}
}

func TestLintReportsUnparsableNoteAsViolation(t *testing.T) {
	buildUnparsableFixture(t)

	stdout, stderr, err := runCLISplit(t, "lint")
	var lintErr *LintError
	if err == nil || !errors.As(err, &lintErr) {
		t.Fatalf("lint error = %v, want *LintError (exit 1)\nstderr: %s", err, stderr)
	}
	if lintErr.Count != 1 {
		t.Fatalf("violation count = %d, want 1:\n%s", lintErr.Count, stdout)
	}
	// Named like `views check`, so the rendered line reads `bn: lint: ...`.
	if got, want := err.Error(), "lint: 1 violation(s) found"; got != want {
		t.Fatalf("lint error = %q, want %q", got, want)
	}
	if !strings.Contains(stdout, "frontmatter: unparsable in 1 note (") || !strings.Contains(stdout, "  bad.md\n") {
		t.Fatalf("lint did not report bad.md as unparsable:\n%s", stdout)
	}
}

func TestMutationStaysStrictOnUnparsableNote(t *testing.T) {
	buildUnparsableFixture(t)

	_, _, err := runCLISplit(t, "replace", "tags", "go", "rust", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "bad.md") {
		t.Fatalf("replace error = %v, want a parse failure naming bad.md", err)
	}
}

// TestMutationScanErrorsNameCommandAndRelativePath pins how a vault-wide
// mutation reports the note that stopped its walk: under the command's name,
// with the vault-relative path, never `vault: walk:` and an absolute path.
func TestMutationScanErrorsNameCommandAndRelativePath(t *testing.T) {
	root := buildUnparsableFixture(t)
	for _, args := range [][]string{
		{"replace", "tags", "go", "rust", "--dry-run"},
		{"rename-key", "tags", "labels", "--dry-run"},
		{"relink", "--dry-run"},
		{"repair", "--dry-run"},
		{"pin", "Alpha.md", "--dry-run"},
		{"rename", "Alpha.md", "Gamma", "--dry-run"},
	} {
		_, _, err := runCLISplit(t, args...)
		want := args[0] + `: scan vault: note: parse "bad.md": frontmatter:`
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("bn %v: err = %v, want prefix %q", args, err, want)
		}
		if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "vault: walk") {
			t.Fatalf("bn %v: err = %v, want no absolute path or walk prefix", args, err)
		}
		var parseErr *vault.ParseError
		if !errors.As(err, &parseErr) {
			t.Fatalf("bn %v: err = %v, want it to unwrap to *vault.ParseError", args, err)
		}
	}
}
