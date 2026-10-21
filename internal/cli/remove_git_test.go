package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultfixture"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
)

func TestRemoveWithGitPreservesLatestBytesAndAllowsExternalDeletion(t *testing.T) {
	for _, state := range []string{"tracked", "modified", "untracked"} {
		t.Run(state, func(t *testing.T) {
			checkGitRemoval(t, state)
		})
	}
}

func checkGitRemoval(t *testing.T, state string) {
	t.Helper()
	v := vaultfixture.New(t, vaultfixture.Options{})
	useVaultRoot(t, v.Root, testPropertyDefinitions)
	path, body := prepareGitRemoval(t, v, state)
	// VS Code deletes another note before the CLI runs.
	if err := os.Remove(v.Path("Alpha.md")); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "", "remove", path, "--yes")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if got := v.Read(t, "Trash/"+path); got != body {
		t.Fatalf("Trash lost latest content: %q", got)
	}
	entry := loadJournalEntries(t, v.Root)[0]
	changed := v.Git(t, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")
	if strings.Contains(changed, "Alpha.md") {
		t.Fatal("CLI commit swept in an unrelated external deletion")
	}
	if out, err := runCLI(t, "", "undo", entry.ID); err != nil {
		t.Fatalf("undo: %v\n%s", err, out)
	}
	if got := v.Read(t, path); got != body {
		t.Fatalf("undo lost latest content: %q", got)
	}
	mgr, err := vaultgit.Open(context.Background(), v.Root, vaultgit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := mgr.CommitPending(context.Background())
	if err != nil || !result.Committed {
		t.Fatalf("record external deletion: %+v, %v", result, err)
	}
}

func prepareGitRemoval(t *testing.T, v *vaultfixture.Vault, state string) (string, string) {
	t.Helper()
	path := "Beta.md"
	body := v.Read(t, path)
	if state != "tracked" {
		body = "---\ntitle: Latest\n---\nUnsnapshotted bytes.\n"
		if state == "untracked" {
			path = "New.md"
		}
		v.Write(t, path, body)
	}
	return path, body
}
