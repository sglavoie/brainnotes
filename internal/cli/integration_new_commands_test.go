package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestEndToEndScenario_NewCommands chains the three commands introduced in
// the "next features" plan — `bn tags`, `bn find`, `bn set`, `bn backlinks`,
// `bn set` — against a fixture that contains a typo'd tag. The scenario
// surfaces the typo via `bn tags`, rewrites it with a stdin-piped `bn set`,
// then marks every backlink of a target note via another stdin-piped `bn set`.
func TestEndToEndScenario_NewCommands(t *testing.T) {
	root := buildIntegrationFixture(t)

	typoPath := filepath.Join(root, "Books", "Sci-Fi.md")
	writeTypoTagNote(t, typoPath)

	tagsOut, err := runCLI(t, "", "tags")
	if err != nil {
		t.Fatalf("tags: %v\n%s", err, tagsOut)
	}
	if !strings.Contains(tagsOut, "sciific") {
		t.Fatalf("expected typo in tags output, got:\n%s", tagsOut)
	}

	rewriteTypoTag(t, typoPath)
	assertTypoTagGoneFromTags(t)

	referrers := duneBacklinkReferrers(t, root)
	markReferrersReferenced(t, referrers)

	goals := readNote(t, filepath.Join(root, "Personal", "Goals.md"))
	if strings.Contains(goals, "status: referenced") {
		t.Fatalf("non-referrer Goals.md was modified:\n%s", goals)
	}
}

func writeTypoTagNote(t *testing.T, typoPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(typoPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\n" +
		"tags:\n  - sciific\n" +
		"---\n" +
		"Body of [[Dune]].\n"
	if err := os.WriteFile(typoPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write typo: %v", err)
	}
}

// rewriteTypoTag finds the typo'd note by tag and pipes it into `bn set`.
func rewriteTypoTag(t *testing.T, typoPath string) {
	t.Helper()
	findOut, err := runCLI(t, "", "find", "--tag", "sciific")
	if err != nil {
		t.Fatalf("find: %v\n%s", err, findOut)
	}
	gotPaths := splitAndSort(findOut)
	wantPaths := []string{typoPath}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("find --tag sciific:\n got=%v\nwant=%v", gotPaths, wantPaths)
	}

	stdin := strings.Join(gotPaths, "\n") + "\n"
	if _, err := runCLI(t, stdin, "set", "-", "tags=sci-fi"); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	rewritten := readNote(t, typoPath)
	if !strings.Contains(rewritten, "- sci-fi") {
		t.Fatalf("expected tags rewritten to sci-fi, got:\n%s", rewritten)
	}
	if strings.Contains(rewritten, "sciific") {
		t.Fatalf("expected sciific gone from %s, got:\n%s", typoPath, rewritten)
	}
	assertAlphabeticalKeys(t, rewritten)
}

func assertTypoTagGoneFromTags(t *testing.T) {
	t.Helper()
	tagsOut, err := runCLI(t, "", "tags")
	if err != nil {
		t.Fatalf("tags (post-rewrite): %v\n%s", err, tagsOut)
	}
	if strings.Contains(tagsOut, "sciific") {
		t.Fatalf("typo still present in tags output:\n%s", tagsOut)
	}
	if !strings.Contains(tagsOut, "sci-fi") {
		t.Fatalf("expected sci-fi in tags output, got:\n%s", tagsOut)
	}
}

func duneBacklinkReferrers(t *testing.T, root string) []string {
	t.Helper()
	blOut, err := runCLI(t, "", "backlinks", "Dune")
	if err != nil {
		t.Fatalf("backlinks: %v\n%s", err, blOut)
	}
	referrers := splitAndSort(blOut)
	if len(referrers) == 0 {
		t.Fatalf("expected backlinks for Dune, got none")
	}
	wantReferrer := filepath.Join(root, "References", "Dune Review.md")
	if !slices.Contains(referrers, wantReferrer) {
		t.Fatalf("expected %s in backlinks, got %v", wantReferrer, referrers)
	}
	return referrers
}

func markReferrersReferenced(t *testing.T, referrers []string) {
	t.Helper()
	stdin := strings.Join(referrers, "\n") + "\n"
	if _, err := runCLI(t, stdin, "set", "-", "status=referenced"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	for _, path := range referrers {
		got := readNote(t, path)
		if !strings.Contains(got, "status: referenced") {
			t.Fatalf("expected status: referenced in %s, got:\n%s", path, got)
		}
		assertAlphabeticalKeys(t, got)
	}
}
