package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ForbiddenMutationError reports a write refused because of *where* it would
// land rather than because of what it would contain. It names the target and,
// when one was identified, the protected ancestor that decided the verdict. It
// deliberately carries no file contents: the guard runs before anything is read
// or staged, and the only actionable fact is the location.
//
// There is no override. A flag, environment variable, or config switch that
// re-enabled the write would exist precisely for the case where a user is
// certain the storage is fine, which is the case that lost a note.
type ForbiddenMutationError struct {
	// Target is the intended mutation target as an absolute path.
	Target string
	// Ancestor is the protected directory Target resolves beneath. It is empty
	// when the location could not be decided at all, which is also refused.
	Ancestor string
	// Reason is the one-clause explanation of why the location is refused.
	Reason string
}

func (e *ForbiddenMutationError) Error() string {
	if e == nil {
		return "vault: mutation refused"
	}
	if e.Ancestor != "" {
		return fmt.Sprintf(
			"vault: refusing to write %s because %s is %s; move the vault outside that directory (there is no override)",
			e.Target, e.Ancestor, e.Reason)
	}
	return fmt.Sprintf(
		"vault: refusing to write %s because %s (there is no override)",
		e.Target, e.Reason)
}

// IsForbiddenMutation reports whether err is a write refused by the storage
// location policy. Nothing was locked, journaled, staged, or written.
func IsForbiddenMutation(err error) bool {
	var forbidden *ForbiddenMutationError
	return errors.As(err, &forbidden)
}

// mutationLocationPolicyFunc judges one existing, symlink-resolved path. It
// returns the protected ancestor and the reason it is protected, or an empty
// ancestor when the location is safe to mutate. Any error means the location
// could not be decided, which the guard treats as a refusal rather than a pass.
type mutationLocationPolicyFunc func(path string) (ancestor, reason string, err error)

// GuardMutation is the mandatory preflight every filesystem mutation in a vault
// runs before it creates anything at all — before a lock file, a journal entry
// directory, a temp file, a transaction manifest, a note, a rename, a trash
// move, a restore, a durable state file, or a configuration rewrite.
//
// It is an invariant rather than caller advice: the low-level paths in this
// package call it from lock acquisition and Note.Save, so a caller that reaches
// the filesystem through them is guarded whether it knows about the policy or
// not. Callers that create directories or journal entries *before* they take a
// lock call it explicitly, because the point of the guard is that no artifact
// exists when it refuses.
//
// root may be empty for a write that has no vault (writing a vault marker into
// a directory that is not a vault yet, or a state file addressed absolutely).
// Empty targets are ignored so callers can pass optional paths unconditionally.
//
// Reads are never guarded. A vault under protected storage stays fully readable
// and browsable; only mutation is refused.
func GuardMutation(root string, targets ...string) error {
	policy := mutationLocationPolicy
	if policy == nil {
		// No location policy on this platform. The safe-replacement capability
		// self-test still applies to every write; this guard is about storage
		// that misinterprets a path becoming vacant, which is a macOS File
		// Provider behavior.
		return nil
	}
	// One vault mutation names the root and several targets that usually share a
	// directory, so the same ancestor would otherwise be walked once per path.
	decided := make(map[string]struct{}, len(targets)+1)
	for i := 0; i <= len(targets); i++ {
		candidate := root
		if i > 0 {
			candidate = targets[i-1]
		}
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return &ForbiddenMutationError{
				Target: candidate,
				Reason: fmt.Sprintf("its location could not be resolved (%v)", err),
			}
		}
		existing, err := deepestExistingAncestor(abs)
		if err != nil {
			return &ForbiddenMutationError{
				Target: abs,
				Reason: fmt.Sprintf("its location could not be resolved (%v)", err),
			}
		}
		if _, done := decided[existing]; done {
			continue
		}
		decided[existing] = struct{}{}
		ancestor, reason, err := policy(existing)
		if err != nil {
			return &ForbiddenMutationError{
				Target: abs,
				Reason: fmt.Sprintf("its storage location could not be determined (%v)", err),
			}
		}
		if ancestor != "" {
			return &ForbiddenMutationError{Target: abs, Ancestor: ancestor, Reason: reason}
		}
	}
	return nil
}

// deepestExistingAncestor returns the real location abs occupies: the deepest
// existing *directory* on abs, with every symlink resolved.
//
// A target that does not exist yet — every create, every temp file, every
// journal entry directory — resolves to the directory it would be created in,
// which is exactly the location the policy has to judge. Resolving symlinks is
// what stops a link inside a safe vault from pointing the real write at
// protected storage; the guard judges where the bytes land, not where the name
// is.
//
// An existing file resolves to its parent directory rather than to itself,
// because a storage location is a property of a directory. Judging the file
// would make an unrelated fact about that one file — an unreadable mode, say —
// look like an undecidable location and refuse a write that is perfectly safe.
func deepestExistingAncestor(abs string) (string, error) {
	current := filepath.Clean(abs)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil {
				return "", fmt.Errorf("resolve symlinks on %q: %w", current, evalErr)
			}
			info, statErr := os.Stat(resolved)
			if statErr != nil {
				return "", fmt.Errorf("inspect %q: %w", resolved, statErr)
			}
			if !info.IsDir() {
				return filepath.Dir(resolved), nil
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %q", abs)
		}
		current = parent
	}
}
