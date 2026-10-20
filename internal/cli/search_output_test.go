package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSearch_NullSep(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "-0", "keyword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "\x00") {
		t.Fatalf("expected NUL separator in output, got: %q", stdout)
	}
	parts := strings.Split(stdout, "\x00")
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) != 2 {
		t.Fatalf("expected 2 NUL-separated paths, got %d: %v", len(nonEmpty), nonEmpty)
	}
}

func TestSearch_NullStdin(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	betaPath := filepath.Join(root, "Notes", "Beta.md")
	stdin := strings.NewReader(alphaPath + "\x00" + betaPath + "\x00")

	stdout, err := runSearchStdin(t, root, stdin, "-0", "keyword", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := strings.Split(stdout, "\x00")
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) != 2 {
		t.Fatalf("expected 2 NUL-separated paths, got %d: %v", len(nonEmpty), nonEmpty)
	}
	sort.Strings(nonEmpty)
	got := make([]string, len(nonEmpty))
	for i, p := range nonEmpty {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		got[i] = filepath.ToSlash(rel)
	}
	want := []string{"Notes/Alpha.md", "Notes/Beta.md"}
	if !equalStrings(got, want) {
		t.Fatalf("null-stdin search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_Context_Basic(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Physics.md": "---\ntitle: Physics\n---\ncontext line above\nactivation energy in physics\ncontext line below\n",
	})
	stdout, err := runSearch(t, root, "--context", "1", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := contextLines(stdout)
	want := readGoldenLines(t, "search-context-basic.golden")
	if !equalStrings(got, want) {
		t.Fatalf("context basic:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_Context_Overlap(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nenergy line one\nsome middle line\nenergy line two\n",
	})
	// Two matches 2 lines apart with N=2: windows should merge into one block — no "--".
	stdout, err := runSearch(t, root, "--context", "2", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout, "--") {
		t.Fatalf("overlapping windows should merge into one block, got separator:\n%s", stdout)
	}
	lines := contextLines(stdout)
	if len(lines) == 0 {
		t.Fatal("expected output lines, got none")
	}
}

func TestSearch_Context_NoOverlap(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nenergy line one\nfar apart A\nfar apart B\nfar apart C\nenergy line two\n",
	})
	// Two matches far apart with N=1: windows should not overlap — "--" appears once.
	stdout, err := runSearch(t, root, "--context", "1", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := contextLines(stdout)
	var sepCount int
	for _, l := range lines {
		if l == "--" {
			sepCount++
		}
	}
	if sepCount != 1 {
		t.Fatalf("expected exactly 1 '--' separator for non-overlapping windows, got %d:\n%s", sepCount, stdout)
	}
}

func TestSearch_Context_MultiNote(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/A.md": "---\ntitle: A\n---\ncontext before\nenergy in A\ncontext after\n",
		"Notes/B.md": "---\ntitle: B\n---\ncontext before\nenergy in B\ncontext after\n",
	})
	stdout, err := runSearch(t, root, "--context", "1", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := contextLines(stdout)
	want := readGoldenLines(t, "search-context-multinote.golden")
	if !equalStrings(got, want) {
		t.Fatalf("context multi-note:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_Context_FirstLine(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nenergy is the first body line\nsome context after\n",
	})
	// Match on first body line with N=2: no frontmatter lines should appear.
	stdout, err := runSearch(t, root, "--context", "2", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := contextLines(stdout)
	for _, l := range lines {
		if strings.Contains(l, "title:") || strings.Contains(l, "---") {
			t.Fatalf("frontmatter line leaked into context output: %q", l)
		}
	}
	if len(lines) == 0 {
		t.Fatal("expected output lines, got none")
	}
}

func TestSearch_Context_LastLine(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "---\ntitle: Test\n---\nsome context before\nenergy is the last line\n",
	})
	// Match on last body line with N=2: output should not go past file end.
	stdout, err := runSearch(t, root, "--context", "2", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := contextLines(stdout)
	// Exactly 2 lines: context line before + match line.
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d:\n%s", len(lines), stdout)
	}
	if !strings.Contains(lines[1], ":") || !strings.Contains(lines[1], "energy is the last line") {
		t.Fatalf("expected match line as last output line, got: %q", lines[1])
	}
}

func TestSearch_Context_NoFrontmatter(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/Test.md": "energy first\ncontext after\n",
	})
	// Note without frontmatter: bodyStartIdx=0, match at line 1.
	stdout, err := runSearch(t, root, "--context", "1", "energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := contextLines(stdout)
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d:\n%s", len(lines), stdout)
	}
	// First line should be the match at line 1 (1-based).
	if !strings.Contains(lines[0], ":1:") {
		t.Fatalf("expected match at line 1, got: %q", lines[0])
	}
}

// readBodyLines must start the body exactly where frontmatter.Parse does, so a
// BOM-prefixed block is still skipped and an indented "---" is body text.
func TestReadBodyLinesAgreesWithFrontmatterParse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{name: "plain_frontmatter", content: "---\ntitle: a\n---\nbody\n", want: 3},
		{name: "bom_frontmatter", content: "\ufeff---\ntitle: a\n---\nbody\n", want: 3},
		{name: "crlf_frontmatter", content: "---\r\ntitle: a\r\n---\r\nbody\r\n", want: 3},
		{name: "closer_at_eof", content: "---\ntitle: a\n---", want: 3},
		{name: "indented_opener_is_body", content: "  ---\nbody\n  ---\nmore\n", want: 0},
		{name: "unterminated_block_is_body", content: "---\nbody\n", want: 0},
		{name: "no_frontmatter", content: "body\n", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "note.md")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, got, err := readBodyLines(path)
			if err != nil {
				t.Fatalf("readBodyLines: %v", err)
			}
			if got != tt.want {
				t.Fatalf("bodyStart = %d, want %d", got, tt.want)
			}
		})
	}
}
