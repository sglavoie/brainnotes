package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func buildMoveFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Books/Dune.md":          "---\ntitle: Dune\n---\nOriginal body.\nFolder self: [[Books/Dune]] and [[Books/Dune|alias]].\nBare self: [[Dune]].\n",
		"Reviews/Dune Review.md": "---\ntitle: Dune Review\n---\nRead [[Books/Dune]] last week.\nAlso see [[Books/Dune|the book]].\nJump to [[Books/Dune#Sequels]].\nBare: [[Dune]].\nUnrelated: [[Books/Other]].\n",
		"categorized.md":         "---\ncategories:\n  - \"[[Books/Dune]]\"\n  - \"[[Books/Other]]\"\n---\nA categorized note.\n",
		"parent.md":              "---\nparent: \"[[Books/Dune]]\"\n---\nA child note.\n",
		"other.md":               "---\ntitle: Other\n---\nNo references here.\n",
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

	useVaultRoot(t, root, "")
	return root
}
