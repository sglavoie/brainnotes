package config

import (
	"strings"
	"testing"
)

const viewPatchSample = `# Brain Notes configuration.
ignore = ["Attachments"]

[frontmatter.properties.status]
type = "string"

[[views]]
# the view I open every morning
name = "Active work"
query = "status:wip"
limit = 20

[[views]]
name = "Reading"
query = "type:book"

[serve]
addr = "127.0.0.1:8080"
`

func runViewPatch(t *testing.T, source string, patches ...ViewPatch) PatchResult {
	t.Helper()
	result, err := PatchViews([]byte(source), patches)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	return result
}

// decodeViews round-trips a patched body through the real decoder, so a patch
// that produces TOML the loader would reject fails here rather than in a vault.
func decodeViews(t *testing.T, body string) []SavedView {
	t.Helper()
	cfg, err := loadTOML(t, body)
	if err != nil {
		t.Fatalf("patched config does not load: %v\n%s", err, body)
	}
	return cfg.Views
}

func TestPatchViewsUpsertCreatesBlockAndPreservesEverythingElse(t *testing.T) {
	result := runViewPatch(t, viewPatchSample, ViewPatch{
		Op:   PatchUpsert,
		Name: "Orphans",
		View: SavedView{
			Query:       "orphan:true",
			Sort:        "-modified",
			Description: `notes nothing links to, "yet"`,
			Limit:       50,
		},
	})
	if !result.Changed {
		t.Fatal("creating a view should change the file")
	}
	patched := string(result.Patched)

	want := "[[views]]\nname = \"Orphans\"\nquery = \"orphan:true\"\n" +
		"description = \"notes nothing links to, \\\"yet\\\"\"\nsort = \"-modified\"\nlimit = 50\n"
	if !strings.Contains(patched, want) {
		t.Fatalf("new block missing:\n%s", patched)
	}
	for _, unrelated := range []string{
		"# Brain Notes configuration.",
		"ignore = [\"Attachments\"]",
		"[frontmatter.properties.status]\ntype = \"string\"",
		"# the view I open every morning",
		"[serve]\naddr = \"127.0.0.1:8080\"",
	} {
		if !strings.Contains(patched, unrelated) {
			t.Fatalf("unrelated content lost: %q\n%s", unrelated, patched)
		}
	}

	views := decodeViews(t, patched)
	if len(views) != 3 {
		t.Fatalf("got %d views, want 3", len(views))
	}
	// A new view is appended after the last existing one, keeping the section
	// contiguous and before [serve].
	if views[2].Name != "Orphans" || views[2].Limit != 50 || views[2].Sort != "-modified" {
		t.Fatalf("appended view: %+v", views[2])
	}
	if views[2].Description != `notes nothing links to, "yet"` {
		t.Fatalf("description not round-tripped: %q", views[2].Description)
	}
}

func TestPatchViewsUpsertRewritesOnlyTargetBlock(t *testing.T) {
	result := runViewPatch(t, viewPatchSample, ViewPatch{
		Op:   PatchUpsert,
		Name: "Reading",
		View: SavedView{Query: "type:book and status:wip"},
	})
	patched := string(result.Patched)

	if !strings.Contains(patched, "# the view I open every morning") {
		t.Fatalf("a comment in an untouched block was lost:\n%s", patched)
	}
	if !strings.Contains(patched, "[serve]\naddr = \"127.0.0.1:8080\"") {
		t.Fatalf("the section after the views was disturbed:\n%s", patched)
	}
	views := decodeViews(t, patched)
	if len(views) != 2 {
		t.Fatalf("got %d views, want 2", len(views))
	}
	if views[0].Name != "Active work" || views[0].Limit != 20 {
		t.Fatalf("untargeted view changed: %+v", views[0])
	}
	if views[1].Query != "type:book and status:wip" {
		t.Fatalf("target query not rewritten: %+v", views[1])
	}
}

// TestPatchViewsUpsertDropsCommentsInsideTheTargetBlock pins the known cost of
// whole-block replacement, which is exactly why the diff is a mandatory
// preview rather than a courtesy.
func TestPatchViewsUpsertDropsCommentsInsideTheTargetBlock(t *testing.T) {
	result := runViewPatch(t, viewPatchSample, ViewPatch{
		Op:   PatchUpsert,
		Name: "Active work",
		View: SavedView{Query: "status:wip", Limit: 20},
	})
	patched := string(result.Patched)
	if strings.Contains(patched, "# the view I open every morning") {
		t.Fatal("rewriting a block is expected to drop the comments inside it")
	}
	removed := false
	for _, line := range result.Diff {
		if line.Kind == PatchLineRemoved && strings.Contains(line.Text, "# the view I open every morning") {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("the dropped comment must appear in the preview diff: %+v", result.Diff)
	}
}

func TestPatchViewsRenameKeepsBlockBody(t *testing.T) {
	result := runViewPatch(t, viewPatchSample, ViewPatch{
		Op:      PatchRename,
		Name:    "Active work",
		NewName: "Current work",
	})
	patched := string(result.Patched)
	if !strings.Contains(patched, "# the view I open every morning") {
		t.Fatalf("rename must preserve the block body:\n%s", patched)
	}
	views := decodeViews(t, patched)
	if views[0].Name != "Current work" || views[0].Query != "status:wip" || views[0].Limit != 20 {
		t.Fatalf("renamed view: %+v", views[0])
	}
}

func TestPatchViewsDeleteRemovesWholeBlock(t *testing.T) {
	result := runViewPatch(t, viewPatchSample, ViewPatch{Op: PatchDelete, Name: "Active work"})
	patched := string(result.Patched)
	if strings.Contains(patched, "Active work") || strings.Contains(patched, "# the view I open every morning") {
		t.Fatalf("delete left the block behind:\n%s", patched)
	}
	if !strings.Contains(patched, "[[views]]\nname = \"Reading\"") {
		t.Fatalf("delete disturbed the sibling view:\n%s", patched)
	}
	if !strings.Contains(patched, "[serve]\naddr = \"127.0.0.1:8080\"") {
		t.Fatalf("delete glued two unrelated sections together:\n%s", patched)
	}
	views := decodeViews(t, patched)
	if len(views) != 1 || views[0].Name != "Reading" {
		t.Fatalf("views after delete: %+v", views)
	}
}

// TestPatchViewsRoundTripsThroughEveryOp runs the three ops in sequence and
// requires the result to still load, which is the property that matters:
// brainnotes.toml is the vault-root marker.
func TestPatchViewsRoundTripsThroughEveryOp(t *testing.T) {
	result := runViewPatch(t, viewPatchSample,
		ViewPatch{Op: PatchUpsert, Name: "Orphans", View: SavedView{Query: "orphan:true"}},
		ViewPatch{Op: PatchRename, Name: "Orphans", NewName: "Unlinked"},
		ViewPatch{Op: PatchUpsert, Name: "Unlinked", View: SavedView{Query: "orphan:true", Limit: 5}},
		ViewPatch{Op: PatchDelete, Name: "Reading"},
	)
	views := decodeViews(t, string(result.Patched))
	if len(views) != 2 {
		t.Fatalf("got %d views, want 2: %+v", len(views), views)
	}
	if views[0].Name != "Active work" {
		t.Fatalf("first view: %+v", views[0])
	}
	if views[1].Name != "Unlinked" || views[1].Limit != 5 {
		t.Fatalf("second view: %+v", views[1])
	}
}

func TestPatchViewsOnFileWithNoViews(t *testing.T) {
	const source = `ignore = ["Attachments"]

[serve]
addr = "127.0.0.1:8080"
`
	result := runViewPatch(t, source, ViewPatch{
		Op:   PatchUpsert,
		Name: "Orphans",
		View: SavedView{Query: "orphan:true"},
	})
	patched := string(result.Patched)
	if !strings.HasSuffix(patched, "[[views]]\nname = \"Orphans\"\nquery = \"orphan:true\"\n") {
		t.Fatalf("first view should be appended at end of file:\n%s", patched)
	}
	views := decodeViews(t, patched)
	if len(views) != 1 || views[0].Name != "Orphans" {
		t.Fatalf("views: %+v", views)
	}
}

func TestPatchViewsIsUnchangedForANoOpRename(t *testing.T) {
	result := runViewPatch(t, viewPatchSample, ViewPatch{
		Op:      PatchRename,
		Name:    "Active work",
		NewName: "Active work",
	})
	if result.Changed {
		t.Fatal("renaming a view to its own name should not change the file")
	}
	if len(result.Diff) != 0 {
		t.Fatalf("no-op rename produced a diff: %+v", result.Diff)
	}
}

func TestPatchViewsErrors(t *testing.T) {
	cases := []struct {
		name    string
		patch   ViewPatch
		wantErr string
	}{
		{
			name:    "empty name",
			patch:   ViewPatch{Op: PatchUpsert, Name: "  "},
			wantErr: "requires a view name",
		},
		{
			name:    "delete missing",
			patch:   ViewPatch{Op: PatchDelete, Name: "Nope"},
			wantErr: `view "Nope" is not declared`,
		},
		{
			name:    "rename missing",
			patch:   ViewPatch{Op: PatchRename, Name: "Nope", NewName: "Other"},
			wantErr: `view "Nope" is not declared`,
		},
		{
			name:    "rename onto an existing view",
			patch:   ViewPatch{Op: PatchRename, Name: "Reading", NewName: "Active work"},
			wantErr: `view "Active work" is already declared`,
		},
		{
			name:    "rename to an invalid name",
			patch:   ViewPatch{Op: PatchRename, Name: "Reading", NewName: "bad/name"},
			wantErr: "must be letters, digits",
		},
		{
			name:    "upsert with an empty query",
			patch:   ViewPatch{Op: PatchUpsert, Name: "Orphans", View: SavedView{Query: "  "}},
			wantErr: "query must not be empty",
		},
		{
			name:    "upsert with a negative limit",
			patch:   ViewPatch{Op: PatchUpsert, Name: "Orphans", View: SavedView{Query: "orphan:true", Limit: -2}},
			wantErr: "must not be negative",
		},
		{
			name:    "unknown op",
			patch:   ViewPatch{Op: PatchOp("nope"), Name: "Reading"},
			wantErr: "unknown patch operation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PatchViews([]byte(viewPatchSample), []ViewPatch{tc.patch})
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q should contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestPatchViewsMatchesNameCaseInsensitively keeps the patcher and the loader
// agreeing about which view a name refers to; validateViews forbids two views
// that differ only by case, so a fold-insensitive patch cannot be ambiguous.
func TestPatchViewsMatchesNameCaseInsensitively(t *testing.T) {
	result := runViewPatch(t, viewPatchSample, ViewPatch{
		Op:   PatchUpsert,
		Name: "ACTIVE WORK",
		View: SavedView{Query: "status:wip"},
	})
	views := decodeViews(t, string(result.Patched))
	if len(views) != 2 {
		t.Fatalf("a case-variant upsert must replace, not append: %+v", views)
	}
	// The patch's own spelling wins, because it is what the caller previewed.
	if views[0].Name != "ACTIVE WORK" {
		t.Fatalf("first view: %+v", views[0])
	}
}

func TestParseTOMLTableHeader(t *testing.T) {
	cases := []struct {
		line     string
		path     string
		isArray  bool
		isHeader bool
	}{
		{line: "[[views]]", path: "views", isArray: true, isHeader: true},
		{line: "  [[ views ]]  # mine", path: "views", isArray: true, isHeader: true},
		{line: "[serve]", path: "serve", isHeader: true},
		{line: "[frontmatter.properties.tags]", path: "frontmatter.properties.tags", isHeader: true},
		{line: "[[other]]", path: "other", isArray: true, isHeader: true},
		// Trailing non-comment text means this is not a table header, matching
		// parseTableHeader; otherwise a malformed line could open a view block.
		{line: "[[views]] name = \"x\""},
		{line: "[serve] addr = \"x\""},
		{line: "[[views]"},
		{line: "name = \"x\""},
		{line: ""},
	}
	for _, tc := range cases {
		path, isArray, isHeader := parseTOMLTableHeader(tc.line)
		if path != tc.path || isArray != tc.isArray || isHeader != tc.isHeader {
			t.Errorf("parseTOMLTableHeader(%q) = (%q, %v, %v), want (%q, %v, %v)",
				tc.line, path, isArray, isHeader, tc.path, tc.isArray, tc.isHeader)
		}
	}
}

func TestParseViewNameAssignment(t *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{line: `name = "Active work"`, want: "Active work", ok: true},
		{line: `  name="Active"  `, want: "Active", ok: true},
		{line: `"name" = "Active"`, want: "Active", ok: true},
		{line: `name = 'Active'`, want: "Active", ok: true},
		{line: `name = "Active" # mine`, want: "Active", ok: true},
		{line: `name = "say \"hi\""`, want: `say "hi"`, ok: true},
		{line: `query = "status:wip"`},
		{line: `name = Active`},
		{line: `name = "unterminated`},
		{line: `# name = "Active"`},
	}
	for _, tc := range cases {
		got, ok := parseViewNameAssignment(tc.line)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseViewNameAssignment(%q) = (%q, %v), want (%q, %v)", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}
