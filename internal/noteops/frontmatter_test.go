package noteops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

func TestSetFrontmatterScalarPreservesBodyAndJournals(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\nstatus: draft\ntitle: Source\n---\nBody stays.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	mutations, err := ParseFrontmatterMutations([]string{"status=done"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	result, err := SetFrontmatter(
		root,
		"Source.md",
		mutations,
		&cfg.Frontmatter,
		WithFrontmatterJournalOptions(
			journal.WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-frontmatter-set", nil }),
		),
	)
	if err != nil {
		t.Fatalf("SetFrontmatter: %v", err)
	}
	if !result.Changed {
		t.Fatal("Changed = false, want true")
	}
	if result.Journal == nil || result.Journal.ID != "entry-frontmatter-set" {
		t.Fatalf("journal = %#v, want deterministic entry", result.Journal)
	}
	got := readFrontmatterTestFile(t, root, "Source.md")
	if got != "---\nstatus: done\ntitle: Source\n---\nBody stays.\n" {
		t.Fatalf("note bytes = %q", got)
	}
	rec := frontmatterRecordForPath(t, *result.Journal, "Source.md")
	if rec.Before.Kind != journal.KindFile || rec.After.Kind != journal.KindFile {
		t.Fatalf("journal record = %#v, want file -> file", rec)
	}
}

func TestSetFrontmatterListAddRemoveAndWikilinkWrapping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\ncategories:\n  - \"[[Books]]\"\ntags:\n  - foo\n---\nBody.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{
		ListProperties:     []string{"categories", "tags"},
		WikilinkProperties: []string{"categories"},
	}}
	mutations, err := ParseFrontmatterMutations([]string{"tags+=bar", "tags-=foo", "categories+=People"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	result, err := SetFrontmatter(root, "Source.md", mutations, &cfg.Frontmatter)
	if err != nil {
		t.Fatalf("SetFrontmatter: %v", err)
	}
	if !result.Changed {
		t.Fatal("Changed = false, want true")
	}
	got := readFrontmatterTestFile(t, root, "Source.md")
	for _, want := range []string{"[[Books]]", "[[People]]", "- bar"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in note:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- foo") {
		t.Fatalf("removed list value still present:\n%s", got)
	}
	if !strings.HasSuffix(got, "---\nBody.\n") {
		t.Fatalf("body was not preserved:\n%s", got)
	}
}

func TestUnsetFrontmatterRemovesKeyAndSkipsNoOpJournal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\nstatus: done\ntitle: Source\n---\nBody.\n")
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	result, err := UnsetFrontmatter(
		root,
		"Source.md",
		[]string{"status"},
		&config.FrontmatterConfig{},
		WithFrontmatterJournalOptions(
			journal.WithClock(func() time.Time { return fixed }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-frontmatter-unset", nil }),
		),
	)
	if err != nil {
		t.Fatalf("UnsetFrontmatter: %v", err)
	}
	if !result.Changed || result.Journal == nil {
		t.Fatalf("result = %#v, want changed with journal", result)
	}
	got := readFrontmatterTestFile(t, root, "Source.md")
	if strings.Contains(got, "status:") || !strings.Contains(got, "title: Source") || !strings.HasSuffix(got, "---\nBody.\n") {
		t.Fatalf("unexpected unset output:\n%s", got)
	}

	noOp, err := UnsetFrontmatter(
		root,
		"Source.md",
		[]string{"missing"},
		&config.FrontmatterConfig{},
		WithFrontmatterJournalOptions(
			journal.WithClock(func() time.Time { return fixed }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-frontmatter-noop", nil }),
		),
	)
	if err != nil {
		t.Fatalf("UnsetFrontmatter no-op: %v", err)
	}
	if noOp.Changed || noOp.Journal != nil {
		t.Fatalf("no-op result = %#v, want unchanged without journal", noOp)
	}
	if _, err := os.Stat(filepath.Join(JournalRoot(root), "entry-frontmatter-noop", "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("no-op journal manifest stat err = %v, want missing", err)
	}
}

func TestSetFrontmatterListMutationRejectsExistingScalar(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "Source.md", "---\ntags: lonely\n---\nBody.\n")
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{ListProperties: []string{"tags"}}}
	mutations, err := ParseFrontmatterMutations([]string{"tags+=foo"}, "", cfg)
	if err != nil {
		t.Fatalf("ParseFrontmatterMutations: %v", err)
	}

	_, err = SetFrontmatter(root, "Source.md", mutations, &cfg.Frontmatter)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("CodeOf(err) = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	if got := readFrontmatterTestFile(t, root, "Source.md"); !strings.Contains(got, "tags: lonely") {
		t.Fatalf("note changed after conflict:\n%s", got)
	}
}

func writeFrontmatterTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func readFrontmatterTestFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	return string(data)
}

func frontmatterRecordForPath(t *testing.T, entry journal.Entry, rel string) journal.FileRecord {
	t.Helper()
	for _, rec := range entry.Files {
		if rec.Path == rel {
			return rec
		}
	}
	t.Fatalf("entry missing record for %q: %#v", rel, entry.Files)
	return journal.FileRecord{}
}
