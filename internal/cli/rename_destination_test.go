package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameAmbiguousTitle(t *testing.T) {
	root := buildRenameFixture(t)
	dup := filepath.Join(root, "Reviews", "Dune.md")
	if err := os.WriteFile(dup, []byte("---\ntitle: Dune Dup\n---\nDup body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err == nil {
		t.Fatalf("expected ambiguous error, got output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

func TestRenameDestinationExistsRefused(t *testing.T) {
	root := buildRenameFixture(t)
	conflict := filepath.Join(root, "Sand Worms.md")
	if err := os.WriteFile(conflict, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err == nil {
		t.Fatal("expected destination-exists error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Dune.md")); err != nil {
		t.Fatalf("original should still exist: %v", err)
	}
}

func TestRenameDestinationExistsForce(t *testing.T) {
	root := buildRenameFixture(t)
	conflict := filepath.Join(root, "Sand Worms.md")
	if err := os.WriteFile(conflict, []byte("stomped\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms", "--force")
	if err != nil {
		t.Fatalf("execute --force: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, got %v", err)
	}
	got := readNote(t, filepath.Join(root, "Sand Worms.md"))
	if strings.Contains(got, "stomped") {
		t.Fatalf("force should have overwritten conflict, got:\n%s", got)
	}
}

func TestRenameRejectsExplicitPathWhenTitleDuplicatedElsewhere(t *testing.T) {
	root := buildRenameFixture(t)
	dup := filepath.Join(root, "Reviews", "Dune.md")
	if err := os.WriteFile(dup, []byte("---\ntitle: Dune Copy\n---\nDup body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotVault(t, root)

	_, err := runCLI(t, "", "rename", filepath.Join(root, "Dune.md"), "Sand Worms")
	if err == nil {
		t.Fatal("expected duplicate-title refusal")
	}
	if !strings.Contains(err.Error(), "duplicated elsewhere in the vault") {
		t.Fatalf("expected duplicate-title error, got %v", err)
	}

	after := snapshotVault(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("duplicate-title refusal modified files:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestRenameRejectsNewTitleDuplicatedElsewhere(t *testing.T) {
	root := buildRenameFixture(t)
	dup := filepath.Join(root, "Reviews", "Sand Worms.md")
	if err := os.WriteFile(dup, []byte("---\ntitle: Sand Worms\n---\nExisting note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotVault(t, root)

	_, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err == nil {
		t.Fatal("expected duplicate destination-title refusal")
	}
	if !strings.Contains(err.Error(), "already exists elsewhere in the vault") {
		t.Fatalf("expected duplicate destination-title error, got %v", err)
	}

	after := snapshotVault(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("duplicate destination-title refusal modified files:\nbefore=%v\nafter=%v", before, after)
	}
}

// TestRenameRejectsDirectoryDestination locks in that a rename whose destination
// path resolves to an existing directory is refused — even with --force. Before
// the fix, --force trusted the user and let os.Rename fail mid-apply (after
// every other note's wikilinks had already been rewritten), leaving the vault
// in a half-rewritten state with no clear error pointing at the cause.
func TestRenameRejectsDirectoryDestination(t *testing.T) {
	root := buildRenameFixture(t)
	dirAtDest := filepath.Join(root, "Sand Worms.md")
	if err := os.MkdirAll(dirAtDest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err == nil {
		t.Fatal("expected directory-destination error without --force")
	}
	if !strings.Contains(err.Error(), "existing directory") {
		t.Fatalf("expected 'existing directory' error, got %v", err)
	}

	_, err = runCLI(t, "", "rename", "Dune", "Sand Worms", "--force")
	if err == nil {
		t.Fatal("expected directory-destination error with --force")
	}
	if !strings.Contains(err.Error(), "existing directory") {
		t.Fatalf("expected 'existing directory' error with --force, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "Dune.md")); err != nil {
		t.Fatalf("original should still exist: %v", err)
	}
}
