//go:build darwin

package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// markFileProviderDomain puts the real attribute macOS uses on the real
// directory, so these tests exercise the same getxattr path a synced vault
// would. The value is arbitrary — the guard keys on presence.
func markFileProviderDomain(t *testing.T, dir string) {
	t.Helper()
	if err := unix.Setxattr(dir, fileProviderDomainAttr, []byte("fixture-domain"), 0); err != nil {
		t.Skipf("cannot set %s on %q: %v", fileProviderDomainAttr, dir, err)
	}
}

// withDomainReader swaps the attribute reader. The kernel will not fail a
// getxattr on request, so this is the only way to keep "an unexpected error
// refuses the write" covered by a test rather than by reading the code.
func withDomainReader(t *testing.T, read func(string) (bool, error)) {
	t.Helper()
	previous := readFileProviderDomain
	readFileProviderDomain = read
	t.Cleanup(func() { readFileProviderDomain = previous })
}

// An ordinary local APFS directory carries no domain anywhere up to the volume
// root, so it is writable. This is the case that must keep passing for every
// vault that is not synced — including the one this suite runs against.
func TestFileProviderPolicy_AllowsLocalDirectory(t *testing.T) {
	root := guardTempDir(t)

	ancestor, reason, err := fileProviderLocationPolicy(root)
	if err != nil {
		t.Fatalf("fileProviderLocationPolicy() error = %v", err)
	}
	if ancestor != "" {
		t.Fatalf("ancestor = %q (%s), want none", ancestor, reason)
	}
	if err := GuardMutation(root, filepath.Join(root, "note.md")); err != nil {
		t.Fatalf("GuardMutation() = %v, want nil", err)
	}
}

// The vault root is itself the File Provider directory.
func TestFileProviderPolicy_RefusesMarkedRoot(t *testing.T) {
	root := guardTempDir(t)
	markFileProviderDomain(t, root)

	err := GuardMutation(root)
	var forbidden *ForbiddenMutationError
	if !errors.As(err, &forbidden) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
	if forbidden.Ancestor != root {
		t.Fatalf("Ancestor = %q, want %q", forbidden.Ancestor, root)
	}
	if !strings.Contains(forbidden.Reason, "File Provider") {
		t.Fatalf("Reason = %q, want it to name the File Provider", forbidden.Reason)
	}
}

// The realistic shape: the marked directory is an ancestor several levels up —
// the vault that lost a note sat under a synced ~/Documents, not on it.
func TestFileProviderPolicy_RefusesMarkedAncestor(t *testing.T) {
	base := guardTempDir(t)
	synced := filepath.Join(base, "Documents")
	vaultRoot := filepath.Join(synced, "nested", "Vault")
	if err := os.MkdirAll(vaultRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	markFileProviderDomain(t, synced)

	err := GuardMutation(vaultRoot, filepath.Join(vaultRoot, "note.md"))
	var forbidden *ForbiddenMutationError
	if !errors.As(err, &forbidden) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
	if forbidden.Ancestor != synced {
		t.Fatalf("Ancestor = %q, want the marked ancestor %q", forbidden.Ancestor, synced)
	}
}

// A vault reached through a symlink is judged where it lands, not where the link
// sits. Otherwise ~/Vault -> ~/Library/Mobile Documents/... would be writable.
func TestFileProviderPolicy_RefusesThroughSymlink(t *testing.T) {
	base := guardTempDir(t)
	synced := filepath.Join(base, "synced")
	vaultRoot := filepath.Join(synced, "Vault")
	if err := os.MkdirAll(vaultRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	markFileProviderDomain(t, synced)

	link := filepath.Join(base, "Vault")
	if err := os.Symlink(vaultRoot, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := GuardMutation(link)
	var forbidden *ForbiddenMutationError
	if !errors.As(err, &forbidden) {
		t.Fatalf("GuardMutation(link) = %v, want a forbidden mutation", err)
	}
	if forbidden.Ancestor != synced {
		t.Fatalf("Ancestor = %q, want %q", forbidden.Ancestor, synced)
	}
}

// Every guarded mutation names paths that do not exist yet — a new note, a
// folder about to be created, a journal entry directory. The nearest existing
// ancestor is what decides them.
func TestFileProviderPolicy_RefusesNotYetCreatedTarget(t *testing.T) {
	base := guardTempDir(t)
	synced := filepath.Join(base, "synced")
	if err := os.MkdirAll(synced, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	markFileProviderDomain(t, synced)

	missing := filepath.Join(synced, "Vault", ".bnotes", "journal", "entry", "manifest.json")
	err := GuardMutation("", missing)
	var forbidden *ForbiddenMutationError
	if !errors.As(err, &forbidden) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
	if forbidden.Ancestor != synced {
		t.Fatalf("Ancestor = %q, want %q", forbidden.Ancestor, synced)
	}
	if forbidden.Target != missing {
		t.Fatalf("Target = %q, want %q", forbidden.Target, missing)
	}
}

// An xattr call that fails for a reason other than "no such attribute" leaves
// the location undecided, and an undecided location is refused.
func TestFileProviderPolicy_FailsClosedOnUnexpectedXattrError(t *testing.T) {
	root := guardTempDir(t)
	withDomainReader(t, func(string) (bool, error) {
		return false, unix.EIO
	})

	err := GuardMutation(root)
	if !IsForbiddenMutation(err) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
	if !strings.Contains(err.Error(), "could not be determined") {
		t.Fatalf("Error() = %q, want it to say the location could not be determined", err.Error())
	}
}

// The three errno values that mean "nothing here to read" continue the walk
// instead of refusing, so an ordinary vault is not blocked by a filesystem
// without xattr support or by a directory that vanished mid-walk.
func TestFileProviderDomainPresent_TreatsAbsenceAsUnmanaged(t *testing.T) {
	root := guardTempDir(t)

	managed, err := fileProviderDomainPresent(root)
	if err != nil {
		t.Fatalf("fileProviderDomainPresent(unmarked) error = %v", err)
	}
	if managed {
		t.Fatal("fileProviderDomainPresent(unmarked) = true, want false")
	}

	managed, err = fileProviderDomainPresent(filepath.Join(root, "gone"))
	if err != nil {
		t.Fatalf("fileProviderDomainPresent(missing) error = %v", err)
	}
	if managed {
		t.Fatal("fileProviderDomainPresent(missing) = true, want false")
	}

	marked := filepath.Join(root, "marked")
	if err := os.Mkdir(marked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	markFileProviderDomain(t, marked)
	managed, err = fileProviderDomainPresent(marked)
	if err != nil {
		t.Fatalf("fileProviderDomainPresent(marked) error = %v", err)
	}
	if !managed {
		t.Fatal("fileProviderDomainPresent(marked) = false, want true")
	}
}

// A directory bn is not allowed to read is a directory whose domain bn cannot
// establish, which is refused rather than assumed safe.
func TestFileProviderPolicy_FailsClosedOnUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := guardTempDir(t)
	unreadable := filepath.Join(root, "unreadable")
	if err := os.Mkdir(unreadable, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	inner := filepath.Join(unreadable, "Vault")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })

	if err := GuardMutation(inner); !IsForbiddenMutation(err) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
}

// The policy is installed on darwin. Nothing else in the package asserts that
// the wiring is live, and a nil policy here would silently disable the guard.
func TestFileProviderPolicy_IsInstalled(t *testing.T) {
	if mutationLocationPolicy == nil {
		t.Fatal("mutationLocationPolicy is nil on darwin, want the File Provider policy")
	}
}
