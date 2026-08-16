package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// buildRenameFixture writes a small vault for rename tests. The vault has:
//   - Dune.md  — target note, with a self-reference in the body
//   - Reviews/Dune Review.md — body refs: bare, aliased, headed, folder-prefixed
//   - categorized.md — categories: ["[[Dune]]"]
//   - parent.md — parent: "[[Dune]]"
//   - other.md — no references
//
// It also marks the vault root and points BNOTES_VAULT at it.
func buildRenameFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Dune.md":                "---\ntitle: Dune\n---\nThis is the original Dune note.\nSelf reference: [[Dune]] and [[Dune|alias]].\n",
		"Reviews/Dune Review.md": "---\ntitle: Dune Review\n---\nRead [[Dune]] last week.\nAlso see [[Dune|the book]].\nJump to [[Dune#Sequels]].\nFull path: [[Books/Dune]].\nUnrelated: [[Something Else]].\n",
		"categorized.md":         "---\ncategories:\n  - \"[[Dune]]\"\n  - \"[[Books]]\"\n---\nA categorized note.\n",
		"parent.md":              "---\nparent: \"[[Dune]]\"\n---\nA child note.\n",
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
