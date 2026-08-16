package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildSetFixture writes a small vault and marks it as a vault root so the
// `set`/`unset` commands can pick it up.
func buildSetFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"note.md":  "---\ntitle: Existing\n---\nBody.\n",
		"a.md":     "---\ntitle: A\n---\nA body.\n",
		"b.md":     "---\ntitle: B\n---\nB body.\n",
		"empty.md": "Body only.\n",
		"tags.md":  "---\ntags:\n  - foo\n  - bar\ntitle: Tags\n---\nBody.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	useVaultRoot(t, root, testPropertyDefinitions)
	return root
}

// runCLI executes the root command with args and returns (stdout, err). It
// optionally writes stdin from the supplied string.
func runCLI(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}
	err := cmd.Execute()
	return out.String(), err
}

func readNote(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
