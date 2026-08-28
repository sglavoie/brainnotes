//go:build !darwin

package vault

import (
	"path/filepath"
	"testing"
)

// Off macOS there is no File Provider, so the location policy is absent and the
// guard permits every location. The session-2 safe-replacement contract is what
// still applies everywhere, and it is unaffected by this.
func TestMutationLocationPolicy_AbsentOffDarwin(t *testing.T) {
	if mutationLocationPolicy != nil {
		t.Fatal("mutationLocationPolicy is set off darwin, want nil")
	}
}

func TestGuardMutation_NoOpOffDarwin(t *testing.T) {
	root := guardTempDir(t)

	if err := GuardMutation(root, filepath.Join(root, "note.md")); err != nil {
		t.Fatalf("GuardMutation() = %v, want nil", err)
	}
	// A target with no existing ancestor cannot even be resolved, and off darwin
	// that is not a reason to refuse: there is no policy to ask.
	if err := GuardMutation("", filepath.Join(root, "missing", "deep", "note.md")); err != nil {
		t.Fatalf("GuardMutation(missing) = %v, want nil", err)
	}
}
