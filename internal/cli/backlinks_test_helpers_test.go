package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// newBacklinkFixture writes a small vault used by the backlinks tests and
// returns its root. Layout:
//   - Target.md           — the queried note.
//   - Referrer1.md        — body has [[Target]].
//   - Referrer2.md        — body has [[Folder/Target|alias]].
//   - Referrer3.md        — frontmatter category: "[[Target]]".
//   - Unrelated.md        — no link to Target.
//   - Mentions.md         — `[[Target]]` inside a code span (must NOT match).
func newBacklinkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Target.md":    "---\ntitle: Target\n---\nI am the target.\n",
		"Referrer1.md": "---\ntitle: Referrer1\n---\nSee [[Target]] for more.\n",
		"Referrer2.md": "---\ntitle: Referrer2\n---\nAlso [[Folder/Target|alias]] here.\n",
		"Referrer3.md": "---\ntitle: Referrer3\ncategory: \"[[Target]]\"\n---\nBody.\n",
		"Unrelated.md": "---\ntitle: Unrelated\n---\nNothing to see.\n",
		"Mentions.md":  "---\ntitle: Mentions\n---\nInline `[[Target]]` should be ignored.\n",
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
	return root
}

// runBacklinks executes the root command with the given args and returns
// stdout, stderr, and the execute error.
func runBacklinks(t *testing.T, vaultRoot string, args ...string) (string, string, error) {
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

func relSlashLines(t *testing.T, vaultRoot, s string) []string {
	t.Helper()
	var rels []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(vaultRoot, line)
		if err != nil {
			t.Fatalf("rel %q: %v", line, err)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	sort.Strings(rels)
	return rels
}

// stripVaultLines splits raw output by newline, drops empty lines, and replaces
// the absolute vault-root prefix with the relative remainder (slash-separated)
// so verbose-mode output can be compared against goldens stored with relative
// paths. Order is preserved — verbose output is sorted by the command itself.
func stripVaultLines(t *testing.T, vaultRoot, s string) []string {
	t.Helper()
	var out []string
	prefix := vaultRoot + string(os.PathSeparator)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			line = filepath.ToSlash(line[len(prefix):])
		}
		out = append(out, line)
	}
	return out
}

func readGoldenLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}
