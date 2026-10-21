//go:build darwin

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/sglavoie/brainnotes/internal/vault"
)

const fileProviderDomainAttr = "com.apple.file-provider-domain-id"

// markFileProvider makes dir look like a directory a File Provider extension
// owns. It is applied after the fixture is written, so the fixture itself is
// built normally and only the commands under test face the policy.
func markFileProvider(t *testing.T, dir string) {
	t.Helper()
	if err := unix.Setxattr(dir, fileProviderDomainAttr, []byte("fixture-domain"), 0); err != nil {
		t.Skipf("cannot set %s on %q: %v", fileProviderDomainAttr, dir, err)
	}
}

// guardedVault builds a small vault and then marks it as File Provider storage.
func guardedVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	root = resolved

	files := map[string]string{
		"note.md":              "---\ntags:\n  - foo\ntitle: Note\n---\nBody.\n",
		"other.md":             "---\ntitle: Other\n---\nSee [[Note]].\n",
		"Templates/default.md": "---\ntitle: {{ .Title }}\n---\n\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// The autosave floor is assigned rather than left to its default so that
	// `config unset` is a real mutation here. An unset of an absent key writes
	// nothing, and a case that writes nothing proves nothing about the guard.
	useVaultRoot(t, root, testPropertyDefinitions+"\n[autosave]\nmin_interval = \"1m\"\n")
	markFileProvider(t, root)
	return root
}

// assertNoArtifacts is the actual claim of this file: the refusal happens before
// anything exists, not after. A guard that ran late would still return an error,
// but it would leave a journal entry, a snapshot, a lock file, a staged temp
// file or an empty destination folder behind — which is the damage the policy is
// there to prevent.
func assertNoArtifacts(t *testing.T, root string, allowed map[string]bool) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir %q: %v", root, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if allowed[name] {
			continue
		}
		t.Fatalf("%q appeared under a File Provider vault root; the mutation left an artifact behind", name)
	}
}

func fixtureNames() map[string]bool {
	return map[string]bool{
		"note.md":         true,
		"other.md":        true,
		"Templates":       true,
		"brainnotes.toml": true,
	}
}

func assertForbidden(t *testing.T, out string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("command succeeded under a File Provider vault; output:\n%s", out)
	}
	if !vault.IsForbiddenMutation(err) {
		t.Fatalf("error = %v, want a forbidden mutation", err)
	}
	message := err.Error()
	for _, want := range []string{"File Provider", "there is no override"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error = %q, want it to contain %q", message, want)
		}
	}
}

// One case per representative mutation surface. Each one is a different route to
// disk — transaction, direct create, rename, folder move, trash, configuration
// rewrite — and every one of them has to reach the same preflight.
func TestMutationsRefusedUnderFileProviderRoot(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"set", []string{"set", "note.md", "status=done"}},
		{"unset", []string{"unset", "note.md", "tags"}},
		{"new", []string{"new", "default", "Fresh Note"}},
		{"rename", []string{"rename", "note.md", "Renamed Note"}},
		{"move", []string{"move", "note.md", "Archive"}},
		{"replace", []string{"replace", "tags", "foo", "bar"}},
		{"capture", []string{"capture", "a captured line"}},
		{"pin", []string{"pin", "note.md"}},
		// The vault's own configuration is vault storage like any other, so
		// rewriting it is refused on the same terms as rewriting a note.
		{"config-set", []string{"config", "set", "autosave.min_interval", "2m"}},
		{"config-unset", []string{"config", "unset", "autosave.min_interval"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := guardedVault(t)
			out, err := runCLI(t, "", tc.args...)
			assertForbidden(t, out, err)
			assertNoArtifacts(t, root, fixtureNames())
		})
	}
}

// `bn init` is the one mutation that runs before a vault exists, so it is
// guarded on its destination rather than on a vault root.
func TestInitRefusedUnderFileProviderRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	markFileProvider(t, root)

	out, err := runCLI(t, "", "init", root)
	assertForbidden(t, out, err)
	assertNoArtifacts(t, root, map[string]bool{})
}

// Undo is a mutation, so it is refused — but the history that describes it is a
// read, so it still lists. A vault that became synced after the fact stays fully
// inspectable; it just cannot be written to any more.
func TestUndoRefusedButHistoryStillReadableUnderFileProviderRoot(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	root = resolved
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("---\ntitle: Note\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	useVaultRoot(t, root, testPropertyDefinitions)

	// Journaled while the vault is still ordinary storage.
	if out, err := runCLI(t, "", "set", "note.md", "tags=foo"); err != nil {
		t.Fatalf("set failed: %v\n%s", err, out)
	}
	journalRoot := filepath.Join(root, ".bnotes", "journal")
	before, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatalf("readdir journal: %v", err)
	}

	markFileProvider(t, root)

	out, err := runCLI(t, "", "history")
	if err != nil {
		t.Fatalf("history failed under a File Provider vault: %v\n%s", err, out)
	}
	if !strings.Contains(out, "bn set") {
		t.Fatalf("history output = %q, want it to still list the recorded entry", out)
	}

	out, err = runCLI(t, "", "undo")
	assertForbidden(t, out, err)

	after, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatalf("readdir journal: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("journal entries = %d, want %d; the refused undo recorded one", len(after), len(before))
	}
}

// A backup writes an archive inside the vault, so it obeys the same policy and
// must not leave a backups/ directory behind when refused.
func TestBackupRefusedUnderFileProviderRoot(t *testing.T) {
	root := guardedVault(t)

	out, err := runCLI(t, "", "backup")
	assertForbidden(t, out, err)
	assertNoArtifacts(t, root, fixtureNames())
}

// Reading is unaffected. A synced vault is still fully usable for everything
// that does not write, which is what keeps the policy from being a lockout.
func TestReadOnlyCommandsWorkUnderFileProviderRoot(t *testing.T) {
	root := guardedVault(t)

	cases := []struct {
		name string
		args []string
	}{
		{"find", []string{"find"}},
		{"history", []string{"history"}},
		{"config", []string{"config"}},
		{"set-dry-run", []string{"set", "note.md", "status=done", "--dry-run"}},
		{"rename-dry-run", []string{"rename", "note.md", "Renamed Note", "--dry-run"}},
		{"config-get", []string{"config", "get", "autosave.min_interval"}},
		{"config-set-dry-run", []string{"config", "set", "autosave.min_interval", "2m", "--dry-run"}},
		{"config-unset-dry-run", []string{"config", "unset", "autosave.min_interval", "--dry-run"}},
		// 60s names the stored 1m, so this set changes nothing and never becomes
		// a mutation at all. That is what makes a converged script safe to re-run
		// against a vault that later moved onto synced storage.
		{"config-set-no-op", []string{"config", "set", "autosave.min_interval", "60s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCLI(t, "", tc.args...)
			if err != nil {
				t.Fatalf("%v failed under a File Provider vault: %v\n%s", tc.args, err, out)
			}
		})
	}

	// Not even the index cache is allowed to materialize: .bnotes/ is storage
	// like any other, and a read must not create it here.
	assertNoArtifacts(t, root, fixtureNames())
}
