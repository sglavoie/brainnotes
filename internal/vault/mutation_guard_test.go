package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardTempDir returns a temporary directory with symlinks already resolved.
// The guard judges resolved paths, and macOS hands out temp directories under
// /var, which is a symlink to /private/var — so a fake policy written against
// the unresolved name would never match what the guard actually asks about.
func guardTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

// withPolicy installs a location policy for the duration of one test. It is what
// lets the shared guard logic — path resolution, deduplication, error shape,
// fail-closed behavior — be tested on every platform, including the ones where
// the real policy is nil.
func withPolicy(t *testing.T, policy mutationLocationPolicyFunc) {
	t.Helper()
	previous := mutationLocationPolicy
	mutationLocationPolicy = policy
	t.Cleanup(func() { mutationLocationPolicy = previous })
}

// refuseUnder returns a policy that refuses any path at or below protected.
func refuseUnder(protected string) mutationLocationPolicyFunc {
	return func(path string) (string, string, error) {
		for current := filepath.Clean(path); ; {
			if current == filepath.Clean(protected) {
				return protected, "a protected location", nil
			}
			parent := filepath.Dir(current)
			if parent == current {
				return "", "", nil
			}
			current = parent
		}
	}
}

func TestGuardMutation_AllowsUnprotectedLocation(t *testing.T) {
	root := guardTempDir(t)
	withPolicy(t, refuseUnder(filepath.Join(root, "elsewhere")))

	if err := GuardMutation(root, filepath.Join(root, "note.md")); err != nil {
		t.Fatalf("GuardMutation() = %v, want nil", err)
	}
}

func TestGuardMutation_NoPolicyAllowsEverything(t *testing.T) {
	root := guardTempDir(t)
	withPolicy(t, nil)

	if err := GuardMutation(root, filepath.Join(root, "note.md")); err != nil {
		t.Fatalf("GuardMutation() = %v, want nil", err)
	}
}

func TestGuardMutation_RefusesProtectedRoot(t *testing.T) {
	root := guardTempDir(t)
	withPolicy(t, refuseUnder(root))

	err := GuardMutation(root)
	if !IsForbiddenMutation(err) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
	var forbidden *ForbiddenMutationError
	if !errors.As(err, &forbidden) {
		t.Fatalf("errors.As: %v is not a *ForbiddenMutationError", err)
	}
	if forbidden.Ancestor != root {
		t.Fatalf("Ancestor = %q, want %q", forbidden.Ancestor, root)
	}
	if !strings.Contains(err.Error(), "there is no override") {
		t.Fatalf("Error() = %q, want it to say there is no override", err.Error())
	}
}

func TestGuardMutation_RefusesProtectedTargetUnderAllowedRoot(t *testing.T) {
	root := guardTempDir(t)
	protected := filepath.Join(root, "synced")
	if err := os.Mkdir(protected, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	withPolicy(t, refuseUnder(protected))

	target := filepath.Join(protected, "note.md")
	err := GuardMutation(root, target)
	var forbidden *ForbiddenMutationError
	if !errors.As(err, &forbidden) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
	if forbidden.Target != target {
		t.Fatalf("Target = %q, want %q", forbidden.Target, target)
	}
	if forbidden.Ancestor != protected {
		t.Fatalf("Ancestor = %q, want %q", forbidden.Ancestor, protected)
	}
}

// The guard runs before a mutation, so most targets do not exist yet. What is
// judged is the nearest directory that does — otherwise every create would be
// undecidable and the fail-closed rule would refuse all of them.
func TestGuardMutation_JudgesNearestExistingAncestorOfMissingTarget(t *testing.T) {
	root := guardTempDir(t)
	withPolicy(t, refuseUnder(root))

	err := GuardMutation("", filepath.Join(root, "a", "b", "c", "note.md"))
	var forbidden *ForbiddenMutationError
	if !errors.As(err, &forbidden) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
	if forbidden.Ancestor != root {
		t.Fatalf("Ancestor = %q, want %q", forbidden.Ancestor, root)
	}
}

// A symlinked vault must be judged by where it actually lands. Checking the link
// itself would let a link outside a protected tree authorize writes inside one.
func TestGuardMutation_ResolvesSymlinkedTarget(t *testing.T) {
	base := guardTempDir(t)
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	resolvedReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	withPolicy(t, refuseUnder(resolvedReal))

	if err := GuardMutation("", filepath.Join(link, "note.md")); !IsForbiddenMutation(err) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation through the symlink", err)
	}
}

// A policy that cannot decide is not a policy that permits. Anything other than
// a clean "not protected" refuses the write.
func TestGuardMutation_FailsClosedOnPolicyError(t *testing.T) {
	root := guardTempDir(t)
	withPolicy(t, func(string) (string, string, error) {
		return "", "", errors.New("boom")
	})

	err := GuardMutation(root)
	if !IsForbiddenMutation(err) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Error() = %q, want it to carry the underlying failure", err.Error())
	}
}

// A path with no existing ancestor at all cannot be judged, so it is refused
// rather than waved through.
func TestGuardMutation_FailsClosedWhenAncestorUnresolvable(t *testing.T) {
	root := guardTempDir(t)
	unreadable := filepath.Join(root, "unreadable")
	if err := os.Mkdir(unreadable, 0o000); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	withPolicy(t, refuseUnder(filepath.Join(root, "nothing-here")))

	// Lstat of a child of a mode-0000 directory fails with EACCES rather than
	// ENOENT, so the ancestor walk cannot conclude anything.
	if err := GuardMutation("", filepath.Join(unreadable, "child", "note.md")); !IsForbiddenMutation(err) {
		t.Fatalf("GuardMutation() = %v, want a forbidden mutation", err)
	}
}

// The guard is called with a root plus every planned path, and a transaction can
// plan hundreds of paths in one directory. Each distinct location is decided
// once.
func TestGuardMutation_ConsultsPolicyOncePerDistinctLocation(t *testing.T) {
	root := guardTempDir(t)
	calls := 0
	withPolicy(t, func(string) (string, string, error) {
		calls++
		return "", "", nil
	})

	targets := []string{
		filepath.Join(root, "one.md"),
		filepath.Join(root, "two.md"),
		filepath.Join(root, "three.md"),
	}
	if err := GuardMutation(root, targets...); err != nil {
		t.Fatalf("GuardMutation() = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("policy calls = %d, want 1 (all four paths resolve to %q)", calls, root)
	}
}

// A location is a property of a directory, so an existing file is judged by the
// directory holding it. Judging the file itself would let an unrelated fact
// about that one file — an unreadable mode, an exotic filesystem flag — read as
// an undecidable location and refuse a write that is entirely safe. `bn` writes
// mode-0000 notes on purpose (Save preserves a restricted mode), so this is a
// case the vault actually reaches.
func TestGuardMutation_JudgesDirectoryOfAnExistingFile(t *testing.T) {
	root := guardTempDir(t)
	notePath := filepath.Join(root, "restricted.md")
	if err := os.WriteFile(notePath, []byte("body\n"), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(notePath, 0o644) })

	var judged []string
	withPolicy(t, func(path string) (string, string, error) {
		judged = append(judged, path)
		return "", "", nil
	})

	if err := GuardMutation(root, notePath); err != nil {
		t.Fatalf("GuardMutation() = %v, want nil", err)
	}
	if len(judged) != 1 || judged[0] != root {
		t.Fatalf("policy saw %v, want just the containing directory %q", judged, root)
	}
}

func TestGuardMutation_SkipsEmptyPaths(t *testing.T) {
	calls := 0
	withPolicy(t, func(string) (string, string, error) {
		calls++
		return "", "", nil
	})

	if err := GuardMutation("", "", "  "); err != nil {
		t.Fatalf("GuardMutation() = %v, want nil", err)
	}
	if calls != 0 {
		t.Fatalf("policy calls = %d, want 0", calls)
	}
}

// The message is what a person acts on, and it must not turn into a disclosure
// channel for the content the refused write carried.
func TestForbiddenMutationError_MessageNamesLocationsOnly(t *testing.T) {
	err := &ForbiddenMutationError{
		Target:   "/vault/notes/secret.md",
		Ancestor: "/vault",
		Reason:   "a protected location",
	}
	message := err.Error()
	for _, want := range []string{"/vault/notes/secret.md", "/vault", "a protected location", "there is no override"} {
		if !strings.Contains(message, want) {
			t.Fatalf("Error() = %q, want it to contain %q", message, want)
		}
	}
}

func TestIsForbiddenMutation_MatchesThroughWrapping(t *testing.T) {
	base := &ForbiddenMutationError{Target: "/vault", Reason: "a protected location"}
	wrapped := fmt.Errorf("save note: %w", base)
	if !IsForbiddenMutation(wrapped) {
		t.Fatal("IsForbiddenMutation(wrapped) = false, want true")
	}
	if IsForbiddenMutation(errors.New("unrelated")) {
		t.Fatal("IsForbiddenMutation(unrelated) = true, want false")
	}
	if IsForbiddenMutation(nil) {
		t.Fatal("IsForbiddenMutation(nil) = true, want false")
	}
}
