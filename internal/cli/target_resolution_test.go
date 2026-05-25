package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestResolveRenameTarget_PathArgRelativeToVaultRoot(t *testing.T) {
	root := buildRenameFixture(t)
	v := openTestVault(t, root)

	wd := mustGetwd(t)
	outside := t.TempDir()
	if err := os.Chdir(outside); err != nil {
		t.Fatalf("chdir(%q): %v", outside, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	target, err := resolveRenameTarget(v, "Dune.md")
	if err != nil {
		t.Fatalf("resolveRenameTarget: %v", err)
	}
	if want := filepath.Join(root, "Dune.md"); target.Path != want {
		t.Fatalf("target.Path = %q, want %q", target.Path, want)
	}
}

func TestResolveMoveTarget_PathArgRelativeToVaultRoot(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("---\ntitle: Dune\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	v := openTestVault(t, root)

	wd := mustGetwd(t)
	outside := t.TempDir()
	if err := os.Chdir(outside); err != nil {
		t.Fatalf("chdir(%q): %v", outside, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	target, err := resolveMoveTarget(v, "Books/Dune.md")
	if err != nil {
		t.Fatalf("resolveMoveTarget: %v", err)
	}
	if target.Path != targetPath {
		t.Fatalf("target.Path = %q, want %q", target.Path, targetPath)
	}
}

func TestEnsureUniqueRenameDestinationTitleRejectsExistingTitle(t *testing.T) {
	root := buildRenameFixture(t)
	dup := filepath.Join(root, "Reviews", "Sand Worms.md")
	if err := os.WriteFile(dup, []byte("---\ntitle: Sand Worms\n---\nExisting note.\n"), 0o644); err != nil {
		t.Fatalf("write duplicate: %v", err)
	}
	v := openTestVault(t, root)

	err := ensureUniqueRenameDestinationTitle(v, filepath.Join(root, "Dune.md"), filepath.Join(root, "Sand Worms.md"), "Sand Worms", false)
	if err == nil {
		t.Fatal("expected duplicate destination-title refusal")
	}
	if !strings.Contains(err.Error(), "already exists elsewhere in the vault") {
		t.Fatalf("expected duplicate destination-title error, got %v", err)
	}
}

func TestEnsureUniqueRenameDestinationTitleAllowsForcedOverwriteAtDestination(t *testing.T) {
	root := buildRenameFixture(t)
	dest := filepath.Join(root, "Sand Worms.md")
	if err := os.WriteFile(dest, []byte("---\ntitle: Sand Worms\n---\nExisting note.\n"), 0o644); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	v := openTestVault(t, root)

	if err := ensureUniqueRenameDestinationTitle(v, filepath.Join(root, "Dune.md"), dest, "Sand Worms", true); err != nil {
		t.Fatalf("expected forced overwrite at destination to be allowed, got %v", err)
	}
}

func openTestVault(t *testing.T, root string) *vault.Vault {
	t.Helper()
	v, err := vault.Open(&config.Config{Vault: root})
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	return v
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}
