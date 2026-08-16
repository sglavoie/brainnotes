package noteops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestBulkPreviewTrashNotesReadOnlyReportsDestinationAndFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "A.md", "---\ntitle: A\n---\nBody A.\n")
	writeFrontmatterTestFile(t, root, "Trash/A.md", "existing trash\n")

	result, err := BulkPreviewTrashNotes(
		root,
		[]BulkSource{
			{Key: "a", RelPath: "A.md"},
			{Key: "missing", RelPath: "Missing.md"},
		},
		WithBulkTrashClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithBulkTrashShortIDGenerator(func() (string, error) { return "abc123ef", nil }),
	)
	if err != nil {
		t.Fatalf("BulkPreviewTrashNotes: %v", err)
	}
	if result.Changed != 1 || result.Unchanged != 0 || len(result.Failures) != 1 {
		t.Fatalf("result counts = changed %d unchanged %d failures %#v", result.Changed, result.Unchanged, result.Failures)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results = %#v, want one planned result", result.Results)
	}
	item := result.Results[0]
	wantDest := "Trash/A.deleted-20260102T030405Z-abc123ef.md"
	if item.Destination.RelPath != wantDest {
		t.Fatalf("destination rel = %q, want %q", item.Destination.RelPath, wantDest)
	}
	if item.Trash == nil || item.Trash.SourceRelPath != "A.md" || item.Trash.DestinationRelPath != wantDest {
		t.Fatalf("trash plan = %#v, want source and destination", item.Trash)
	}
	if len(item.Trash.CollisionIDs) != 1 || item.Trash.CollisionIDs[0] != "abc123ef" {
		t.Fatalf("collision ids = %#v, want abc123ef", item.Trash.CollisionIDs)
	}
	if got := readFrontmatterTestFile(t, root, "A.md"); !strings.Contains(got, "Body A.") {
		t.Fatalf("source changed after preview:\n%s", got)
	}
	if got := readFrontmatterTestFile(t, root, "Trash/A.md"); got != "existing trash\n" {
		t.Fatalf("existing trash changed after preview:\n%s", got)
	}
	assertPathMissing(t, filepath.Join(root, filepath.FromSlash(wantDest)))
	assertPathMissing(t, filepath.Join(root, ".bnotes", "journal"))
}

func TestBulkTrashNotesReusesPreviewTrashDestinationPlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "A.md", "body\n")
	writeFrontmatterTestFile(t, root, "Trash/A.md", "existing trash\n")

	preview, err := BulkPreviewTrashNotes(
		root,
		[]BulkSource{{Key: "a", RelPath: "A.md"}},
		WithBulkTrashClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithBulkTrashShortIDGenerator(func() (string, error) { return "abc123ef", nil }),
	)
	if err != nil {
		t.Fatalf("BulkPreviewTrashNotes: %v", err)
	}
	if len(preview.Results) != 1 || preview.Results[0].Trash == nil {
		t.Fatalf("preview results = %#v, want one trash plan", preview.Results)
	}
	if err := os.Remove(filepath.Join(root, "Trash", "A.md")); err != nil {
		t.Fatalf("remove original trash collision: %v", err)
	}

	applied, err := BulkTrashNotes(
		root,
		[]BulkSource{{Key: "a", RelPath: "A.md"}},
		WithBulkTrashDestinationPlans([]TrashDestinationPlan{*preview.Results[0].Trash}),
	)
	if err != nil {
		t.Fatalf("BulkTrashNotes with preview plan: %v", err)
	}
	wantDest := "Trash/A.deleted-20260102T030405Z-abc123ef.md"
	if applied.Results[0].Destination.RelPath != wantDest {
		t.Fatalf("applied destination = %q, want previewed %q", applied.Results[0].Destination.RelPath, wantDest)
	}
	if got := readFrontmatterTestFile(t, root, wantDest); got != "body\n" {
		t.Fatalf("planned destination content = %q, want body", got)
	}
}

func TestBulkPreviewSetFrontmatterReadOnlyReportsChangedUnchangedAndFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "A.md", "---\nstatus: draft\ntitle: A\n---\nBody A.\n")
	writeFrontmatterTestFile(t, root, "B.md", "---\nstatus: done\ntitle: B\n---\nBody B.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	mutations, err := ParseFrontmatterMutations([]string{"status=done"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	result, err := BulkPreviewSetFrontmatter(
		root,
		[]BulkSource{
			{Key: "a", RelPath: "A.md"},
			{Key: "b", RelPath: "B.md"},
			{Key: "missing", RelPath: "Missing.md"},
		},
		mutations,
		&cfg.Frontmatter,
	)
	if err != nil {
		t.Fatalf("BulkPreviewSetFrontmatter: %v", err)
	}
	if result.Changed != 1 || result.Unchanged != 1 || len(result.Failures) != 1 {
		t.Fatalf("result counts = changed %d unchanged %d failures %#v", result.Changed, result.Unchanged, result.Failures)
	}
	changed := frontmatterPreviewForKey(t, result.Results[0], "status")
	if !changed.Before.Present || changed.Before.Value != "draft" || !changed.After.Present || changed.After.Value != "done" || !changed.Changed {
		t.Fatalf("changed status preview = %#v", changed)
	}
	unchanged := frontmatterPreviewForKey(t, result.Results[1], "status")
	if !unchanged.Before.Present || unchanged.Before.Value != "done" || !unchanged.After.Present || unchanged.After.Value != "done" || unchanged.Changed {
		t.Fatalf("unchanged status preview = %#v", unchanged)
	}
	if got := readFrontmatterTestFile(t, root, "A.md"); !strings.Contains(got, "status: draft") {
		t.Fatalf("A.md changed after preview:\n%s", got)
	}
	if got := readFrontmatterTestFile(t, root, "B.md"); !strings.Contains(got, "status: done") {
		t.Fatalf("B.md changed after preview:\n%s", got)
	}
	assertPathMissing(t, filepath.Join(root, ".bnotes", "journal"))
}

func TestBulkPreviewUnsetFrontmatterDistinguishesPresentNullFromAbsent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Null.md", "---\nstatus:\ntitle: Null\n---\nBody.\n")
	writeFrontmatterTestFile(t, root, "Absent.md", "---\ntitle: Absent\n---\nBody.\n")

	result, err := BulkPreviewUnsetFrontmatter(
		root,
		[]BulkSource{
			{Key: "null", RelPath: "Null.md"},
			{Key: "absent", RelPath: "Absent.md"},
		},
		[]string{"status"},
		&config.FrontmatterConfig{},
	)
	if err != nil {
		t.Fatalf("BulkPreviewUnsetFrontmatter: %v", err)
	}
	if result.Changed != 1 || result.Unchanged != 1 || len(result.Failures) != 0 {
		t.Fatalf("result counts = changed %d unchanged %d failures %#v", result.Changed, result.Unchanged, result.Failures)
	}
	changed := frontmatterPreviewForKey(t, result.Results[0], "status")
	if !changed.Before.Present || changed.Before.Value != nil || changed.After.Present || !changed.Changed {
		t.Fatalf("present-null unset preview = %#v", changed)
	}
	unchanged := frontmatterPreviewForKey(t, result.Results[1], "status")
	if unchanged.Before.Present || unchanged.After.Present || unchanged.Changed {
		t.Fatalf("absent unset preview = %#v", unchanged)
	}
	if got := readFrontmatterTestFile(t, root, "Null.md"); !strings.Contains(got, "status:") {
		t.Fatalf("Null.md changed after preview:\n%s", got)
	}
	if got := readFrontmatterTestFile(t, root, "Absent.md"); strings.Contains(got, "status:") {
		t.Fatalf("Absent.md unexpectedly changed after preview:\n%s", got)
	}
}

func TestBulkPreviewMoveNotesReadOnlyReportsDestinationAndRewriteCounts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Inbox/Source.md", "---\ntitle: Source\n---\nBody.\n")
	writeFrontmatterTestFile(t, root, "Ref.md", "---\ntitle: Ref\n---\nSee [[Inbox/Source]].\n")
	writeFrontmatterTestFile(t, root, "Meta.md", "---\nparent: \"[[Inbox/Source]]\"\n---\nBody.\n")
	cfg := &config.Config{
		Vault: root,
		Frontmatter: config.FrontmatterConfig{
			Properties: map[string]config.PropertyDefinition{
				"parent": {Type: config.KindWikilink},
			},
		},
	}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}

	result, err := BulkPreviewMoveNotes(
		v,
		cfg,
		[]BulkSource{{Key: "inbox/source", RelPath: "Inbox/Source.md"}},
		"Archive",
	)
	if err != nil {
		t.Fatalf("BulkPreviewMoveNotes: %v", err)
	}
	if result.Changed != 1 || result.Unchanged != 0 || len(result.Failures) != 0 {
		t.Fatalf("result counts = changed %d unchanged %d failures %#v", result.Changed, result.Unchanged, result.Failures)
	}
	item := result.Results[0]
	if item.Destination.RelPath != "Archive/Source.md" {
		t.Fatalf("destination rel = %q, want Archive/Source.md", item.Destination.RelPath)
	}
	if item.Move == nil || item.Move.RewriteCount != 2 || item.Move.RewriteFileCount != 2 || item.Move.BodyRewriteCount != 1 || item.Move.FrontmatterRewriteCount != 1 {
		t.Fatalf("move preview = %#v, want one body and one frontmatter rewrite", item.Move)
	}
	if got := readFrontmatterTestFile(t, root, "Inbox/Source.md"); !strings.Contains(got, "Body.") {
		t.Fatalf("source changed after preview:\n%s", got)
	}
	assertPathMissing(t, filepath.Join(root, "Archive"))
	if got := readFrontmatterTestFile(t, root, "Ref.md"); !strings.Contains(got, "[[Inbox/Source]]") {
		t.Fatalf("body reference changed after preview:\n%s", got)
	}
	if got := readFrontmatterTestFile(t, root, "Meta.md"); !strings.Contains(got, "[[Inbox/Source]]") {
		t.Fatalf("frontmatter reference changed after preview:\n%s", got)
	}
	assertPathMissing(t, filepath.Join(root, ".bnotes", "journal"))
}

func frontmatterPreviewForKey(t *testing.T, item BulkPreviewItem, key string) BulkFrontmatterPreview {
	t.Helper()
	for _, change := range item.Frontmatter {
		if change.Key == key {
			return change
		}
	}
	t.Fatalf("frontmatter preview missing key %q in %#v", key, item.Frontmatter)
	return BulkFrontmatterPreview{}
}

func assertPathMissing(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("stat %q = %v, want missing", p, err)
	}
}
