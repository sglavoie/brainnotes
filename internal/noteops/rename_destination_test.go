package noteops

import (
	"os"
	"path/filepath"
	"testing"
)

// Only an absent destination is free. Any other failure to inspect it — here a
// parent that is a regular file — is a conflict rather than a green light, for
// both rename and move.
func TestDestinationChecksTreatOnlyNotExistAsFree(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Source.md")
	blocker := filepath.Join(dir, "Blocker.md")
	for _, path := range []string{target, blocker} {
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	checks := map[string]func(string, string, bool) error{
		"rename": CheckRenameDestination,
		"move":   CheckMoveDestination,
	}
	for name, check := range checks {
		if err := check(target, filepath.Join(dir, "Missing.md"), false); err != nil {
			t.Fatalf("%s to an absent destination = %v, want nil", name, err)
		}
		err := check(target, filepath.Join(blocker, "Under.md"), false)
		if err == nil || CodeOf(err) != CodeConflict {
			t.Fatalf("%s beneath a regular file = %v, want a conflict", name, err)
		}
	}
}
