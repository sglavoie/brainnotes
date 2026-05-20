package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildGraphFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Alpha.md": "---\ntags:\n  - foo\n---\n[[Beta]] and [[Gamma]].\n",
		"Beta.md":  "---\ntags:\n  - foo\n---\n[[Gamma]].\n",
		"Gamma.md": "---\ntags:\n  - bar\n---\nNo links.\n",
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

func runGraphRaw(t *testing.T, vaultRoot string, args ...string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_CONFIG", "")
	t.Setenv("BNOTES_VAULT", "")

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v\noutput: %s", args, err, out.String())
	}
	return out.String()
}

func runGraphErr(t *testing.T, vaultRoot string, args ...string) error {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_CONFIG", "")
	t.Setenv("BNOTES_VAULT", "")

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	return root.Execute()
}

func TestGraph_DotOutput(t *testing.T) {
	root := buildGraphFixture(t)
	got := runGraphRaw(t, root, "graph")
	assertGolden(t, "graph_dot_basic.golden", got)
}

func TestGraph_MermaidOutput(t *testing.T) {
	root := buildGraphFixture(t)
	got := runGraphRaw(t, root, "graph", "--format", "mermaid")
	assertGolden(t, "graph_mermaid_basic.golden", got)
}

func TestGraph_FilteredSubgraph(t *testing.T) {
	root := buildGraphFixture(t)
	got := runGraphRaw(t, root, "graph", "--tag", "foo")
	want := `digraph vault {
  rankdir=LR;
  "Alpha" -> "Beta";
}
`
	if got != want {
		t.Fatalf("graph --tag foo:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestGraph_EmptyFilter(t *testing.T) {
	root := buildGraphFixture(t)
	got := runGraphRaw(t, root, "graph", "--tag", "nonexistent")
	want := "digraph vault {\n  rankdir=LR;\n}\n"
	if got != want {
		t.Fatalf("graph --tag nonexistent:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestGraph_DefaultFormat(t *testing.T) {
	root := buildGraphFixture(t)
	got := runGraphRaw(t, root, "graph")
	if !strings.HasPrefix(got, "digraph vault {") {
		t.Fatalf("default format should be dot, got:\n%s", got)
	}
}

func TestGraph_InvalidFormat(t *testing.T) {
	root := buildGraphFixture(t)
	err := runGraphErr(t, root, "graph", "--format", "json")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("expected 'unknown format' error, got: %v", err)
	}
}
