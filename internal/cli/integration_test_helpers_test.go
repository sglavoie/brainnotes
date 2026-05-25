package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// buildIntegrationFixture writes a representative vault with all frontmatter
// property types (categories, tags, parent, status, rating, type, kind) and
// body/frontmatter wikilink cross-references. Used by the end-to-end scenario
// test below.
func buildIntegrationFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"References/Dune.md": "---\n" +
			"categories:\n  - \"[[Books]]\"\n" +
			"tags:\n  - sci-fi\n  - classic\n" +
			"status: To Read\n" +
			"rating: 6\n" +
			"author: Frank Herbert\n" +
			"---\n" +
			"Notes about [[Dune]] and its sequels.\n",
		"References/Dune Review.md": "---\n" +
			"categories:\n  - \"[[Books]]\"\n" +
			"tags:\n  - review\n" +
			"parent: \"[[Dune]]\"\n" +
			"---\n" +
			"Reviewing [[Dune]] in depth.\n" +
			"Also see [[Dune|the novel]] and [[Dune#Sequels]].\n" +
			"Folder-prefixed: [[References/Dune]].\n",
		"People/Frank Herbert.md": "---\n" +
			"categories:\n  - \"[[People]]\"\n" +
			"tags:\n  - author\n" +
			"type: Person\n" +
			"---\n" +
			"Wrote [[Dune]].\n",
		"Projects/Reading List.md": "---\n" +
			"categories:\n  - \"[[Projects]]\"\n" +
			"kind: Project\n" +
			"status: WIP\n" +
			"---\n" +
			"Currently reading [[Dune]].\n",
		"Daily/2026-05-18.md": "---\n" +
			"type: Daily\n" +
			"---\n" +
			"Read more of [[Dune]] today.\n",
		"Personal/Goals.md": "---\n" +
			"categories:\n  - \"[[Personal]]\"\n" +
			"type: Goal\n" +
			"---\n" +
			"Read 24 books this year.\n",
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

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")
	return root
}

// splitAndSort splits cmd output into lines, trims, drops blanks, and sorts.
func splitAndSort(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

// assertAlphabeticalKeys checks that top-level YAML keys in the frontmatter
// of body are in alphabetical order.
func assertAlphabeticalKeys(t *testing.T, body string) {
	t.Helper()
	if !strings.HasPrefix(body, "---\n") {
		t.Fatalf("body missing frontmatter:\n%s", body)
	}
	rest := body[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		t.Fatalf("body missing closing frontmatter:\n%s", body)
	}
	fm := rest[:end]
	var keys []string
	for _, line := range strings.Split(fm, "\n") {
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "-") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		keys = append(keys, line[:colon])
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] < keys[i-1] {
			t.Fatalf("frontmatter keys not alphabetical: %v\n%s", keys, body)
		}
	}
}
