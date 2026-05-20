package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// buildIntegrationFixture writes a representative vault with all frontmatter
// property types (categories, tags, parent, status, rating, type, kind) and
// body/frontmatter wikilink cross-references. Used by the end-to-end scenario
// test below.
func buildIntegrationFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"References/Dune.md": "---\n" +
			"categories:\n  - \"[[Books]]\"\n" +
			"tags:\n  - sci-fi\n  - classic\n" +
			"status: To Read\n" +
			"rating: 6\n" +
			"author: Frank Herbert\n" +
			"---\n" +
			"Notes about [[Dune]] and its sequels.\n",
		"References/Dune Review.md": "---\n" +
			"categories:\n  - \"[[Books]]\"\n" +
			"tags:\n  - review\n" +
			"parent: \"[[Dune]]\"\n" +
			"---\n" +
			"Reviewing [[Dune]] in depth.\n" +
			"Also see [[Dune|the novel]] and [[Dune#Sequels]].\n" +
			"Folder-prefixed: [[References/Dune]].\n",
		"People/Frank Herbert.md": "---\n" +
			"categories:\n  - \"[[People]]\"\n" +
			"tags:\n  - author\n" +
			"type: Person\n" +
			"---\n" +
			"Wrote [[Dune]].\n",
		"Projects/Reading List.md": "---\n" +
			"categories:\n  - \"[[Projects]]\"\n" +
			"kind: Project\n" +
			"status: WIP\n" +
			"---\n" +
			"Currently reading [[Dune]].\n",
		"Daily/2026-05-18.md": "---\n" +
			"type: Daily\n" +
			"---\n" +
			"Read more of [[Dune]] today.\n",
		"Personal/Goals.md": "---\n" +
			"categories:\n  - \"[[Personal]]\"\n" +
			"type: Goal\n" +
			"---\n" +
			"Read 24 books this year.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")
	return root
}

// TestEndToEndScenario exercises the full find → set → rename → move → unset
// pipeline against a representative fixture vault, asserting the on-disk
// state after each step.
func TestEndToEndScenario(t *testing.T) {
	root := buildIntegrationFixture(t)

	// Step 1: find by tag — expect Dune (sci-fi).
	out, err := runCLI(t, "", "find", "--tag", "sci-fi")
	if err != nil {
		t.Fatalf("find: %v\n%s", err, out)
	}
	got := splitAndSort(out)
	want := []string{filepath.Join(root, "References", "Dune.md")}
	if !equalStrings(got, want) {
		t.Fatalf("find --tag sci-fi:\n got=%v\nwant=%v", got, want)
	}

	// Step 2: set status=Reading rating=7 on the found note.
	dunePath := filepath.Join(root, "References", "Dune.md")
	if _, err := runCLI(t, "", "set", dunePath, "status=Reading", "rating=7"); err != nil {
		t.Fatalf("set: %v", err)
	}
	body := readNote(t, dunePath)
	if !strings.Contains(body, "status: Reading") {
		t.Fatalf("expected status: Reading, got:\n%s", body)
	}
	if !strings.Contains(body, "rating: 7") {
		t.Fatalf("expected rating: 7, got:\n%s", body)
	}
	// keys must be alphabetically sorted in the frontmatter.
	assertAlphabeticalKeys(t, body)

	// Step 3: rename Dune → Sand Worms.
	if _, err := runCLI(t, "", "rename", dunePath, "Sand Worms"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	newPath := filepath.Join(root, "References", "Sand Worms.md")
	if _, err := os.Stat(dunePath); !os.IsNotExist(err) {
		t.Fatalf("expected %s gone, stat err=%v", dunePath, err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected %s present: %v", newPath, err)
	}

	// All wikilink references should be rewritten — body and frontmatter,
	// bare/aliased/headed/folder-prefixed forms.
	review := readNote(t, filepath.Join(root, "References", "Dune Review.md"))
	wantSubs := []string{
		"[[Sand Worms]]",
		"[[Sand Worms|the novel]]",
		"[[Sand Worms#Sequels]]",
		"[[References/Sand Worms]]",
		"parent: '[[Sand Worms]]'",
	}
	for _, w := range wantSubs {
		if !strings.Contains(review, w) {
			t.Errorf("review missing %q:\n%s", w, review)
		}
	}
	if strings.Contains(review, "[[Dune") {
		t.Errorf("review still has [[Dune... refs:\n%s", review)
	}

	person := readNote(t, filepath.Join(root, "People", "Frank Herbert.md"))
	if !strings.Contains(person, "[[Sand Worms]]") {
		t.Errorf("person body not rewritten:\n%s", person)
	}

	project := readNote(t, filepath.Join(root, "Projects", "Reading List.md"))
	if !strings.Contains(project, "[[Sand Worms]]") {
		t.Errorf("project body not rewritten:\n%s", project)
	}

	// Step 4: move Sand Worms from References/ to References (no change in
	// folder) is a no-op; move it to References/Books instead.
	if _, err := runCLI(t, "", "move", newPath, "References/Books"); err != nil {
		t.Fatalf("move: %v", err)
	}
	movedPath := filepath.Join(root, "References", "Books", "Sand Worms.md")
	if _, err := os.Stat(movedPath); err != nil {
		t.Fatalf("expected %s present: %v", movedPath, err)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}

	// Folder-prefixed wikilinks should now point to References/Books/Sand Worms;
	// bare references stay bare.
	review = readNote(t, filepath.Join(root, "References", "Dune Review.md"))
	if !strings.Contains(review, "[[References/Books/Sand Worms]]") {
		t.Errorf("expected folder-prefixed rewrite to References/Books/Sand Worms:\n%s", review)
	}
	if !strings.Contains(review, "[[Sand Worms]]") {
		t.Errorf("expected bare [[Sand Worms]] preserved:\n%s", review)
	}

	// Step 5: unset rating on the moved note.
	if _, err := runCLI(t, "", "unset", movedPath, "rating"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	body = readNote(t, movedPath)
	if strings.Contains(body, "rating:") {
		t.Fatalf("rating should be gone:\n%s", body)
	}
	if !strings.Contains(body, "status: Reading") {
		t.Fatalf("other properties should remain:\n%s", body)
	}
	assertAlphabeticalKeys(t, body)
}

// TestEndToEndScenario_NewCommands chains the three commands introduced in
// the "next features" plan — `bn tags`, `bn find`, `bn set`, `bn backlinks`,
// `bn set` — against a fixture that contains a typo'd tag. The scenario
// surfaces the typo via `bn tags`, rewrites it with a stdin-piped `bn set`,
// then marks every backlink of a target note via another stdin-piped `bn set`.
func TestEndToEndScenario_NewCommands(t *testing.T) {
	root := buildIntegrationFixture(t)

	// Inject a note with a typo'd tag value ("sciific" — meant "sci-fi").
	typoPath := filepath.Join(root, "Books", "Sci-Fi.md")
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

	// Step 1: `bn tags` surfaces the typo.
	tagsOut, err := runCLI(t, "", "tags")
	if err != nil {
		t.Fatalf("tags: %v\n%s", err, tagsOut)
	}
	if !strings.Contains(tagsOut, "sciific") {
		t.Fatalf("expected typo in tags output, got:\n%s", tagsOut)
	}

	// Step 2: `bn find --tag sciific` returns the offending note.
	findOut, err := runCLI(t, "", "find", "--tag", "sciific")
	if err != nil {
		t.Fatalf("find: %v\n%s", err, findOut)
	}
	gotPaths := splitAndSort(findOut)
	wantPaths := []string{typoPath}
	if !equalStrings(gotPaths, wantPaths) {
		t.Fatalf("find --tag sciific:\n got=%v\nwant=%v", gotPaths, wantPaths)
	}

	// Step 3: pipe the typo'd paths into `bn set tags=sci-fi` via stdin.
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

	// Step 4: `bn tags` no longer surfaces the typo.
	tagsOut, err = runCLI(t, "", "tags")
	if err != nil {
		t.Fatalf("tags (post-rewrite): %v\n%s", err, tagsOut)
	}
	if strings.Contains(tagsOut, "sciific") {
		t.Fatalf("typo still present in tags output:\n%s", tagsOut)
	}
	if !strings.Contains(tagsOut, "sci-fi") {
		t.Fatalf("expected sci-fi in tags output, got:\n%s", tagsOut)
	}

	// Step 5: `bn backlinks Dune` returns notes referencing Dune. Pipe those
	// into `bn set status=referenced` and assert each got the property.
	blOut, err := runCLI(t, "", "backlinks", "Dune")
	if err != nil {
		t.Fatalf("backlinks: %v\n%s", err, blOut)
	}
	referrers := splitAndSort(blOut)
	if len(referrers) == 0 {
		t.Fatalf("expected backlinks for Dune, got none")
	}
	// Sanity check: the fixture's Dune Review note must be among the referrers.
	wantReferrer := filepath.Join(root, "References", "Dune Review.md")
	found := false
	for _, p := range referrers {
		if p == wantReferrer {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s in backlinks, got %v", wantReferrer, referrers)
	}

	stdin = strings.Join(referrers, "\n") + "\n"
	if _, err := runCLI(t, stdin, "set", "-", "status=referenced"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	for _, p := range referrers {
		got := readNote(t, p)
		if !strings.Contains(got, "status: referenced") {
			t.Fatalf("expected status: referenced in %s, got:\n%s", p, got)
		}
		assertAlphabeticalKeys(t, got)
	}

	// The non-referrer note (Personal/Goals.md links to nothing about Dune)
	// must be untouched.
	goals := readNote(t, filepath.Join(root, "Personal", "Goals.md"))
	if strings.Contains(goals, "status: referenced") {
		t.Fatalf("non-referrer Goals.md was modified:\n%s", goals)
	}
}

// TestEndToEndScenario_RepairWorkflow exercises the intended lint → repair
// --dry-run → repair pipeline against a vault with schema violations, verifying
// that lint detects issues, dry-run previews without writing, and apply fixes
// the safe ones while skipping the unfixable.
func TestEndToEndScenario_RepairWorkflow(t *testing.T) {
	root := t.TempDir()

	files := map[string]string{
		"clean.md":     "---\ncreated: 2025-01-01\nstatus: WIP\ntags:\n  - go\n---\nClean note.\n",
		"bad_case.md":  "---\ncreated: 2025-01-01\nstatus: wip\ntags:\n  - go\n---\nCase drift.\n",
		"dup_tags.md":  "---\ncreated: 2025-01-01\nstatus: Done\ntags:\n  - go\n  - go\n---\nDup tags.\n",
		"unfixable.md": "---\ncreated: 2025-01-01\nstatus: bananas\ntags:\n  - go\n---\nBad value.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := `vault = "` + root + `"

[frontmatter.schema.status]
type     = "string"
required = true
allowed  = ["WIP", "Done", "Archived"]

[frontmatter.schema.tags]
type     = "list"
required = true

[frontmatter.schema.created]
type     = "date"
required = true
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	// Step 1: lint detects issues.
	_, lintErr := runCLI(t, "", "lint")
	if lintErr == nil {
		t.Fatal("expected lint to report violations (non-zero exit)")
	}

	// Step 2: repair --dry-run previews fixes without modifying files.
	dryOut, err := runCLI(t, "", "repair", "--dry-run")
	if err != nil {
		t.Fatalf("repair --dry-run: %v\n%s", err, dryOut)
	}
	if !strings.Contains(dryOut, "--- bad_case.md") {
		t.Errorf("expected bad_case.md in dry-run preview, got:\n%s", dryOut)
	}
	if !strings.Contains(dryOut, "--- dup_tags.md") {
		t.Errorf("expected dup_tags.md in dry-run preview, got:\n%s", dryOut)
	}
	if strings.Contains(dryOut, "--- clean.md") {
		t.Errorf("clean.md should not appear in dry-run preview, got:\n%s", dryOut)
	}
	badCaseAfterDry := readNote(t, filepath.Join(root, "bad_case.md"))
	if strings.Contains(badCaseAfterDry, "status: WIP") {
		t.Fatal("dry-run should not have modified bad_case.md")
	}

	// Step 3: repair (apply) fixes what it can, skips what it can't.
	applyOut, err := runCLI(t, "", "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, applyOut)
	}
	if !strings.Contains(applyOut, "repaired:") {
		t.Errorf("expected 'repaired:' lines, got:\n%s", applyOut)
	}
	if !strings.Contains(applyOut, "SKIP:") {
		t.Errorf("expected SKIP lines for unfixable issues, got:\n%s", applyOut)
	}
	if !strings.Contains(applyOut, "skipped") {
		t.Errorf("expected skip count in summary, got:\n%s", applyOut)
	}

	// Verify on-disk state.
	got := readNote(t, filepath.Join(root, "bad_case.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("bad_case.md should have status: WIP after repair, got:\n%s", got)
	}
	assertAlphabeticalKeys(t, got)

	got = readNote(t, filepath.Join(root, "dup_tags.md"))
	if strings.Count(got, "- go") != 1 {
		t.Errorf("dup_tags.md should have one 'go' tag after repair, got:\n%s", got)
	}
	assertAlphabeticalKeys(t, got)

	got = readNote(t, filepath.Join(root, "clean.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("clean.md should be unchanged, got:\n%s", got)
	}

	got = readNote(t, filepath.Join(root, "unfixable.md"))
	if !strings.Contains(got, "status: bananas") {
		t.Errorf("unfixable.md status should still be 'bananas', got:\n%s", got)
	}

	// Step 4: re-running lint should now report fewer issues (only the
	// unfixable note's status remains violated).
	_, lintErr = runCLI(t, "", "lint")
	if lintErr == nil {
		t.Fatal("expected lint to still report unfixable violations")
	}
}

// splitAndSort splits cmd output into lines, trims, drops blanks, and sorts.
func splitAndSort(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

// assertAlphabeticalKeys checks that top-level YAML keys in the frontmatter
// of body are in alphabetical order.
func assertAlphabeticalKeys(t *testing.T, body string) {
	t.Helper()
	if !strings.HasPrefix(body, "---\n") {
		t.Fatalf("body missing frontmatter:\n%s", body)
	}
	rest := body[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		t.Fatalf("body missing closing frontmatter:\n%s", body)
	}
	fm := rest[:end]
	var keys []string
	for _, line := range strings.Split(fm, "\n") {
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "-") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		keys = append(keys, line[:colon])
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] < keys[i-1] {
			t.Fatalf("frontmatter keys not alphabetical: %v\n%s", keys, body)
		}
	}
}
