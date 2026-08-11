package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSearch_TagScope(t *testing.T) {
	isolateBnEnv(t)
	root := buildTaggedVault(t)

	stdout, err := runSearch(t, root, "--tag", "physics", "activation energy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Physics.md"}
	if !equalStrings(got, want) {
		t.Fatalf("tag-scoped search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_FolderScope(t *testing.T) {
	isolateBnEnv(t)
	root := buildFolderVault(t)

	stdout, err := runSearch(t, root, "--folder", "References", "full-text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"References/Paper1.md"}
	if !equalStrings(got, want) {
		t.Fatalf("folder-scoped search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_PositionalArgs(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	stdout, err := runSearch(t, root, "keyword", alphaPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Alpha.md"}
	if !equalStrings(got, want) {
		t.Fatalf("positional-arg search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_StdinPipe(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	gammaPath := filepath.Join(root, "Notes", "Gamma.md")
	stdin := strings.NewReader(alphaPath + "\n" + gammaPath + "\n")

	stdout, err := runSearchStdin(t, root, stdin, "keyword", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Alpha.md"}
	if !equalStrings(got, want) {
		t.Fatalf("stdin-pipe search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_NoteArgsAndFindFlags_Error(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	alphaPath := filepath.Join(root, "Notes", "Alpha.md")
	_, err := runSearch(t, root, "--tag", "physics", "keyword", alphaPath)
	if err == nil {
		t.Fatal("expected error when combining note args and find flags, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error should mention mutually exclusive, got: %v", err)
	}
}

func TestSearch_OrphanScope(t *testing.T) {
	isolateBnEnv(t)
	root := buildOrphanVault(t)

	stdout, err := runSearch(t, root, "--orphan", "orphan-test-term")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Orphan.md"}
	if !equalStrings(got, want) {
		t.Fatalf("orphan-scoped search:\n got=%v\nwant=%v", got, want)
	}
}
