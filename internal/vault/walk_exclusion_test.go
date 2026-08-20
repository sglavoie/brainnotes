package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func walkExclusionConfig() *config.Config {
	cfg := &config.Config{Ignore: []string{"Archive", "Refs/Vendor"}}
	cfg.Templates.Dir = "Templates"
	return cfg
}

func TestWalkExclusion_Allowed(t *testing.T) {
	cfg := walkExclusionConfig()
	for _, rel := range []string{"Note.md", "Refs/Note.md", "Refs/Deep/Note.md", "Archived/Note.md", "refs/vendorish/Note.md"} {
		if reason, excluded := WalkExclusion(cfg, rel); excluded {
			t.Fatalf("%q excluded: %s", rel, reason)
		}
	}
}

func TestWalkExclusion_Excluded(t *testing.T) {
	cfg := walkExclusionConfig()
	cases := map[string]string{
		"Archive/Note.md":         "ignored path Archive",
		"Archive":                 "ignored path Archive",
		"Refs/Vendor/Deep/x.md":   "ignored path Refs/Vendor",
		"Templates/Note.md":       "templates directory Templates",
		"templates/Note.md":       "templates directory templates",
		".bnotes/state.json":      "hidden path .bnotes",
		"Refs/.hidden/Note.md":    "hidden path Refs/.hidden",
		".hidden.md":              "hidden path .hidden.md",
		"../outside.md":           "outside the vault",
		"":                        "outside the vault",
		"   ":                     "outside the vault",
		"./":                      "outside the vault",
		"Refs/../../outside.md":   "outside the vault",
		"Archive/../Archive/x.md": "ignored path Archive",
	}
	for rel, wantReason := range cases {
		reason, excluded := WalkExclusion(cfg, rel)
		if !excluded {
			t.Fatalf("%q not excluded", rel)
		}
		if reason != wantReason {
			t.Fatalf("%q reason = %q, want %q", rel, reason, wantReason)
		}
	}
}

func TestWalkExclusion_NilConfig(t *testing.T) {
	if reason, excluded := WalkExclusion(nil, "Note.md"); excluded {
		t.Fatalf("excluded with nil config: %s", reason)
	}
	if _, excluded := WalkExclusion(nil, ".git/config"); !excluded {
		t.Fatal("hidden paths must be excluded even without a config")
	}
}

// TestWalkExclusion_MatchesWalk pins the predicate to the walk it describes: a
// path Walk never visits must be reported excluded, and vice versa.
func TestWalkExclusion_MatchesWalk(t *testing.T) {
	root := t.TempDir()
	cfg := walkExclusionConfig()
	cfg.Vault = root

	rels := []string{
		"Note.md",
		"Refs/Note.md",
		"Archive/Note.md",
		"Refs/Vendor/Note.md",
		"Templates/Note.md",
		".hidden/Note.md",
	}
	for _, rel := range rels {
		writeExclusionFile(t, root, rel, "body\n")
	}

	v, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	walked := make(map[string]bool)
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		rel, relErr := filepath.Rel(v.Root, n.Path)
		if relErr != nil {
			return relErr
		}
		walked[filepath.ToSlash(rel)] = true
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	for _, rel := range rels {
		reason, excluded := WalkExclusion(cfg, rel)
		if excluded == walked[rel] {
			t.Fatalf("%q: walked=%v excluded=%v (%s)", rel, walked[rel], excluded, reason)
		}
	}
}

func writeExclusionFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func TestVerifyState(t *testing.T) {
	root := t.TempDir()
	writeExclusionFile(t, root, "Note.md", "body\n")
	present := filepath.Join(root, "Note.md")
	absent := filepath.Join(root, "Missing.md")

	if err := VerifyState(present, ExpectAnyContent()); err != nil {
		t.Fatalf("existing file: %v", err)
	}
	if err := VerifyState(present, ExpectHash(HashBytes([]byte("body\n")))); err != nil {
		t.Fatalf("matching hash: %v", err)
	}
	if err := VerifyState(absent, ExpectMissing()); err != nil {
		t.Fatalf("missing file: %v", err)
	}

	err := VerifyState(present, ExpectHash(HashBytes([]byte("other"))))
	if !IsTxnConflict(err) {
		t.Fatalf("stale hash: err = %v, want a conflict", err)
	}
	if err := VerifyState(present, ExpectMissing()); !IsTxnConflict(err) {
		t.Fatalf("unexpected existing file: err = %v, want a conflict", err)
	}
	if err := VerifyState(absent, ExpectAnyContent()); !IsTxnConflict(err) {
		t.Fatalf("unexpected missing file: err = %v, want a conflict", err)
	}
}

// TestFileTxn_WriteCreatesMissingFolder covers the new-file mode probe, which
// needs the destination's parent to exist before it can sample the umask.
func TestFileTxn_WriteCreatesMissingFolder(t *testing.T) {
	root := t.TempDir()

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("NewFileTxn: %v", err)
	}
	dest := filepath.Join(root, "New", "Deeper", "Note.md")
	if err := txn.Write(dest, []byte("body\n"), 0, ExpectMissing()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := txn.Commit(nil); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "body\n" {
		t.Fatalf("content = %q", string(data))
	}
}
