package vault

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// ResolveByTitle previously compared the user-supplied folder to the note's
// folder verbatim, so leading or trailing slashes caused a no-match. It now
// normalizes both like the Folder query predicate does.
func TestResolveByTitleNormalizesFolder(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "People")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "Alice.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	v, err := Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, folder := range []string{"People", "People/", "/People", "/People/", "People\\", "./People", "People/.", "./People/", "People//"} {
		n, err := v.ResolveByTitle("Alice", folder)
		if err != nil {
			t.Errorf("ResolveByTitle(Alice, %q): %v", folder, err)
			continue
		}
		if filepath.Base(n.Path) != "Alice.md" {
			t.Errorf("ResolveByTitle(Alice, %q) = %q", folder, n.Path)
		}
	}
}

// The ambiguity error must list candidates in a stable order and with
// forward-slash paths so callers can render and diff it predictably. Without
// sorting, two runs on the same vault can swap the order of candidates.
func TestResolveByTitleAmbiguityCandidatesSorted(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"Zeta/dup.md":   "# z\n",
		"alpha/dup.md":  "# a\n",
		"middle/dup.md": "# m\n",
	})
	_, err := v.ResolveByTitle("dup", "")
	if err == nil {
		t.Fatalf("expected ambiguity error")
	}
	msg := err.Error()
	// Each candidate must use forward slashes regardless of OS conventions.
	for _, want := range []string{"alpha/dup.md", "middle/dup.md", "Zeta/dup.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("candidate %q missing from error: %s", want, msg)
		}
		if strings.Contains(msg, filepath.FromSlash(want)) && filepath.Separator != '/' {
			t.Errorf("candidate %q rendered with OS separator in: %s", want, msg)
		}
	}
	// Ensure ASCII byte-order sort: uppercase 'Z' sorts before lowercase
	// letters because uppercase bytes are numerically smaller in ASCII.
	aIdx := strings.Index(msg, "alpha/dup.md")
	mIdx := strings.Index(msg, "middle/dup.md")
	zIdx := strings.Index(msg, "Zeta/dup.md")
	if !(zIdx < aIdx && aIdx < mIdx) {
		t.Errorf("candidates not in stable byte-order: %s", msg)
	}
}

// Walk previously emitted hidden files (e.g. `.DS_Store.md`, editor temp files)
// even though hidden directories were pruned. Both are now skipped consistently.
func TestWalkSkipsHiddenFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "visible.md"), []byte("# v\n"), 0o644); err != nil {
		t.Fatalf("write visible: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden.md"), []byte("# h\n"), 0o644); err != nil {
		t.Fatalf("write hidden: %v", err)
	}
	v, err := Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var paths []string
	err = v.Walk(WalkFilter{}, func(n *Note) error {
		paths = append(paths, filepath.Base(n.Path))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, p := range paths {
		if p == ".hidden.md" {
			t.Errorf("hidden file %q should be skipped, got %v", p, paths)
		}
	}
	found := false
	for _, p := range paths {
		if p == "visible.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("visible.md not found in %v", paths)
	}
}

// Save previously inherited 0o600 from os.CreateTemp via the temp file's
// rename, silently narrowing the permissions of any 0o644 note in the vault.
// Round-tripping a save should leave the destination's existing mode intact.
func TestSavePreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	notePath := filepath.Join(v.Root, "a.md")
	// Force a recognizable mode that differs from os.CreateTemp's 0o600.
	if err := os.Chmod(notePath, 0o664); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.SetBody("changed\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	if err := n.Save(v.Frontmatter()); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o664 {
		t.Errorf("mode preservation: got %o, want %o", got, 0o664)
	}
}

// atomicWrite must not widen mode 0o000 to 0o644 on an existing file. The
// zero-mode check previously used `wantMode == 0` to detect "file not found",
// which also matches a file with deliberately restrictive 0o000 permissions.
func TestSavePreservesZeroMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	dir := t.TempDir()
	notePath := filepath.Join(dir, "restricted.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: R\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	v, err := Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	n, err := v.ResolveByTitle("restricted", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Load and mutate while the file is still readable, then restrict.
	if err := n.SetBody("changed\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	if err := os.Chmod(notePath, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := n.Save(v.Frontmatter()); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o000 {
		t.Errorf("zero-mode preservation: got %o, want %o", got, 0o000)
	}

	// Clean up: restore permissions so t.TempDir removal succeeds.
	_ = os.Chmod(notePath, 0o644)
}

// Walk previously matched the templates dir case-sensitively, so on macOS APFS
// (case-insensitive, case-preserving) a directory created as "Templates" but
// configured via cfg.Templates.Dir = "templates" leaked unrendered placeholder
// files into the walk, breaking the documented invariant that template files
// must never be walked.
func TestWalkTemplatesCaseInsensitive(t *testing.T) {
	cases := []struct {
		name      string
		dirOnDisk string
		cfgDir    string
	}{
		{"lower disk, capital cfg", "templates", "Templates"},
		{"capital disk, lower cfg", "Templates", "templates"},
		{"mixed disk, plain cfg", "TempLates", "templates"},
		{"nested dir matched insensitively", "Meta/Templates", "meta/templates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tmplDir := filepath.Join(dir, filepath.FromSlash(tc.dirOnDisk))
			if err := os.MkdirAll(tmplDir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmplDir, "tpl.md"), []byte("---\ncreated: {{date}}\n---\nbroken\n"), 0o644); err != nil {
				t.Fatalf("write tpl: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "good.md"), []byte("---\ntitle: G\n---\nbody\n"), 0o644); err != nil {
				t.Fatalf("write good: %v", err)
			}
			cfg := &config.Config{
				Vault:     dir,
				Templates: config.TemplatesConfig{Dir: tc.cfgDir},
			}
			v, err := Open(cfg)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			var paths []string
			if err := v.Walk(WalkFilter{}, func(n *Note) error {
				rel, _ := filepath.Rel(v.Root, n.Path)
				paths = append(paths, filepath.ToSlash(rel))
				return nil
			}); err != nil {
				t.Fatalf("walk: %v", err)
			}
			for _, p := range paths {
				if strings.Contains(strings.ToLower(p), "templates/") || strings.HasSuffix(strings.ToLower(p), "/tpl.md") || p == "tpl.md" {
					t.Errorf("template file leaked into walk: %q (paths=%v)", p, paths)
				}
			}
			foundGood := false
			for _, p := range paths {
				if p == "good.md" {
					foundGood = true
				}
			}
			if !foundGood {
				t.Errorf("good.md missing from walk: %v", paths)
			}
		})
	}
}

// Walk must still treat user-configured ignore entries case-sensitively because
// the doc promises vault-relative slash-separated matching against on-disk
// names. Adding case-insensitivity for templates must not silently widen this
// contract for arbitrary cfg.Ignore entries.
func TestWalkIgnoreRemainsCaseSensitive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "Archive")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "old.md"), []byte("# old\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := &config.Config{
		Vault:  dir,
		Ignore: []string{"archive"}, // lowercase, won't match capitalized dir
	}
	v, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var paths []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		rel, _ := filepath.Rel(v.Root, n.Path)
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	found := false
	for _, p := range paths {
		if p == "Archive/old.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("case-sensitive Ignore mismatch should leave Archive/old.md visible: %v", paths)
	}
}

// ResolveByTitle's folder scope was previously case-sensitive, which is
// inconsistent with the case-insensitive title match and surprising on
// case-insensitive filesystems (macOS APFS, Windows NTFS). A user with
// `People/Alice.md` on disk should be able to find it via folder "people"
// just like via title "alice".
func TestResolveByTitleFolderCaseInsensitive(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"People/Alice.md": "# a\n",
		"Refs/Book.md":    "# b\n",
	})
	cases := []struct {
		folder string
		want   string
	}{
		{"people", "Alice.md"},
		{"PEOPLE", "Alice.md"},
		{"PeOpLe", "Alice.md"},
		{"refs", "Book.md"},
	}
	for _, tc := range cases {
		t.Run(tc.folder, func(t *testing.T) {
			title := "Alice"
			if tc.want == "Book.md" {
				title = "Book"
			}
			n, err := v.ResolveByTitle(title, tc.folder)
			if err != nil {
				t.Fatalf("ResolveByTitle(%q, %q): %v", title, tc.folder, err)
			}
			if filepath.Base(n.Path) != tc.want {
				t.Errorf("got %s, want %s", filepath.Base(n.Path), tc.want)
			}
		})
	}
}

// Folder must reject an empty root with a clear error instead of falling
// through to filepath.Rel which would silently produce a confusing path
// (e.g. the full absolute path made relative to the cwd).
func TestNoteFolderEmptyRoot(t *testing.T) {
	n := &Note{Path: "/some/abs/path/note.md"}
	if _, err := n.Folder(""); err == nil {
		t.Fatal("Folder with empty root should return an error")
	}
}

// Walk must descend into a deeply nested templates dir's PARENT directories
// even when the parents have case mismatches with the configured templates
// path, only skipping the final templates segment.
func TestWalkTemplatesDeepNestedDescendsParents(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Meta", "Templates"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Meta", "manifest.md"), []byte("---\ntitle: M\n---\nx\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Meta", "Templates", "tpl.md"), []byte("---\nfoo: {{bar}}\n---\nx\n"), 0o644); err != nil {
		t.Fatalf("write tpl: %v", err)
	}
	cfg := &config.Config{
		Vault:     dir,
		Templates: config.TemplatesConfig{Dir: "meta/templates"}, // lowercased
	}
	v, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var paths []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		rel, _ := filepath.Rel(v.Root, n.Path)
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	foundManifest := false
	for _, p := range paths {
		if p == "Meta/manifest.md" {
			foundManifest = true
		}
		if p == "Meta/Templates/tpl.md" {
			t.Errorf("template file %q leaked: %v", p, paths)
		}
	}
	if !foundManifest {
		t.Errorf("Meta/manifest.md should be walked: %v", paths)
	}
}
