package vault

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestOpenRejectsEmptyVaultPath(t *testing.T) {
	_, err := Open(&config.Config{Vault: ""})
	if err == nil {
		t.Fatal("expected error on empty vault path")
	}
	if !strings.Contains(err.Error(), "empty vault path") {
		t.Errorf("error should mention empty vault path: %v", err)
	}
}

func TestResolveByTitleDeepFolders(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"A/B/C/deep.md": "# deep\n",
		"deep.md":       "# shallow\n",
	})

	// Resolve in deep folder.
	n, err := v.ResolveByTitle("deep", "A/B/C")
	if err != nil {
		t.Fatalf("deep folder resolve: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(n.Path), "A/B/C/deep.md") {
		t.Errorf("got %s, want A/B/C/deep.md", n.Path)
	}

	// Root resolve should be ambiguous.
	_, err = v.ResolveByTitle("deep", "")
	if err == nil {
		t.Fatal("expected ambiguity error for deep.md")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention ambiguity: %v", err)
	}
}

// TestResolveByTitleSlashOnlyFolder verifies that a folder argument that
// normalizes to "" (e.g. "/" or "//") produces a vault-wide search and, on
// no-match, an error that does NOT claim the search was scoped to a folder.
// Before the fix, the not-found branch checked the raw folder argument,
// producing a misleading "no note titled X in /" message even though the
// actual search was vault-wide.
func TestResolveByTitleSlashOnlyFolder(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"People/Alice.md": "# alice\n",
	})
	// A slash-only folder normalizes to "" -> vault-wide search.
	n, err := v.ResolveByTitle("Alice", "/")
	if err != nil {
		t.Fatalf("expected vault-wide match for folder %q, got error: %v", "/", err)
	}
	if filepath.Base(n.Path) != "Alice.md" {
		t.Fatalf("got %s, want Alice.md", filepath.Base(n.Path))
	}

	// Not-found with slash-only folder: error must NOT include the raw "/" folder.
	_, err = v.ResolveByTitle("ZZZmissing", "/")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if strings.Contains(err.Error(), `in "/"`) || strings.Contains(err.Error(), "in `/`") {
		t.Errorf("error should not mention slash-only folder: %v", err)
	}
}

func TestResolveByTitleRejectsTraversalFolder(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"People/Alice.md": "# alice\n",
	})
	for _, folder := range []string{"..", "../People", "People/../../Secrets"} {
		t.Run(folder, func(t *testing.T) {
			_, err := v.ResolveByTitle("Alice", folder)
			if err == nil {
				t.Fatal("expected traversal folder to be rejected")
			}
			if !strings.Contains(err.Error(), "escapes vault") {
				t.Errorf("error should mention escaping vault: %v", err)
			}
		})
	}
}

// TestResolveByTitleMultiDotStem verifies that a note named "archive.tar.md"
// is resolvable by its full multi-dot stem.
func TestResolveByTitleMultiDotStem(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"archive.tar.md": "# a\n",
	})
	n, err := v.ResolveByTitle("archive.tar", "")
	if err != nil {
		t.Fatalf("ResolveByTitle: %v", err)
	}
	if filepath.Base(n.Path) != "archive.tar.md" {
		t.Errorf("got %s, want archive.tar.md", filepath.Base(n.Path))
	}
	// Resolving by just "archive" must not match a stem of "archive.tar".
	if _, err := v.ResolveByTitle("archive", ""); err == nil {
		t.Errorf("partial stem 'archive' should not match 'archive.tar.md'")
	}
}

// ResolveByTitle promises case-insensitive title matching. strings.ToLower is
// not enough for full Unicode simple folding: Greek sigma has two lowercase
// forms ("σ" and final "ς"), so a lowercase note stem could fail to match an
// uppercase query even though strings.EqualFold reports them equal.
func TestResolveByTitleUnicodeCaseFold(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"ος.md": "# sigma\n",
	})
	n, err := v.ResolveByTitle("ΟΣ", "")
	if err != nil {
		t.Fatalf("ResolveByTitle: %v", err)
	}
	if filepath.Base(n.Path) != "ος.md" {
		t.Errorf("got %s, want ος.md", filepath.Base(n.Path))
	}
}

// TestResolveByTitleSlashInTitle verifies that a title containing a path
// separator cannot match (filename stems never contain slashes), and the
// error message is the regular not-found form.
func TestResolveByTitleSlashInTitle(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"People/Alice.md": "# a\n",
	})
	if _, err := v.ResolveByTitle("People/Alice", ""); err == nil {
		t.Fatal("title with slash should not match a note's stem")
	}
}

// TestResolveByTitleEmptyVault verifies that ResolveByTitle returns a
// not-found error (not a nil/panic) on an empty vault.
func TestResolveByTitleEmptyVault(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{})
	_, err := v.ResolveByTitle("anything", "")
	if err == nil {
		t.Fatal("expected not-found error on empty vault")
	}
	if !strings.Contains(err.Error(), "no note titled") {
		t.Errorf("error should mention not-found: %v", err)
	}
}

// TestOpenTrimsWhitespaceVaultPath verifies that a vault path consisting of
// whitespace is rejected the same as the empty string. Without trimming, the
// caller would see a confusing "no such file" pointing at a literal-space
// directory under the process cwd (filepath.Abs(" ") => "<cwd>/ ").
func TestOpenTrimsWhitespaceVaultPath(t *testing.T) {
	for _, p := range []string{" ", "\t", "  \n"} {
		_, err := Open(&config.Config{Vault: p})
		if err == nil {
			t.Errorf("Open(%q) should error", p)
			continue
		}
		if !strings.Contains(err.Error(), "empty vault path") {
			t.Errorf("Open(%q) error should mention 'empty vault path': %v", p, err)
		}
	}
}
