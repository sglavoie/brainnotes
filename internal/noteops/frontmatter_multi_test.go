package noteops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
)

// withFrontmatterBeforeAtomicCommit is a test-only seam: it runs in the window
// between planning every note and staging the transaction, which is where a
// pinned content hash goes stale.
func withFrontmatterBeforeAtomicCommit(fn func() error) FrontmatterOption {
	return func(o *frontmatterOptions) {
		o.beforeAtomicCommit = fn
	}
}

func atomicPinWrites(t *testing.T, rels []string, ranks []string) []NoteFrontmatterWrite {
	t.Helper()
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{}}
	writes := make([]NoteFrontmatterWrite, 0, len(rels))
	for i, rel := range rels {
		mutations, err := ParseFrontmatterMutations([]string{"pinned=" + ranks[i]}, "float", cfg)
		if err != nil {
			t.Fatalf("ParseFrontmatterMutations %q: %v", ranks[i], err)
		}
		writes = append(writes, NoteFrontmatterWrite{RelPath: rel, Mutations: mutations})
	}
	return writes
}

// TestSetFrontmatterAtomicStaleHashRefusesTheWholeSet is the reason this
// function exists: an order-preserving renumber must not half-apply, so one note
// replaced after it was planned has to stop every other note from being written.
func TestSetFrontmatterAtomicStaleHashRefusesTheWholeSet(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rels := []string{"N1.md", "N2.md", "N3.md", "N4.md", "N5.md"}
	before := make(map[string]string, len(rels))
	for i, rel := range rels {
		body := fmt.Sprintf("---\npinned: %d.0\n---\nBody %s.\n", i+1, rel)
		writeFrontmatterTestFile(t, root, rel, body)
		before[rel] = body
	}

	concurrent := "---\npinned: 99.0\n---\nSomeone else wrote N3.\n"
	_, err := SetFrontmatterAtomic(
		root,
		atomicPinWrites(t, rels, []string{"1.5", "2.5", "3.5", "4.5", "5.5"}),
		&config.FrontmatterConfig{},
		withFrontmatterBeforeAtomicCommit(func() error {
			return os.WriteFile(filepath.Join(root, "N3.md"), []byte(concurrent), 0o644)
		}),
	)
	if err == nil {
		t.Fatal("stale hash on N3 must fail the whole call")
	}
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}

	for _, rel := range rels {
		want := before[rel]
		if rel == "N3.md" {
			want = concurrent
		}
		if got := readFrontmatterTestFile(t, root, rel); got != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(JournalRoot(root)); !os.IsNotExist(err) {
		t.Fatalf("journal root stat err = %v, want missing", err)
	}
}

// TestSetFrontmatterAtomicJournalsEveryChangedNoteInOneEntry proves the set is
// undone as a set, and that a note whose mutations net to no change is skipped
// rather than rewritten.
func TestSetFrontmatterAtomicJournalsEveryChangedNoteInOneEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "A.md", "---\npinned: 1.0\n---\nBody A.\n")
	writeFrontmatterTestFile(t, root, "sub/B.md", "---\npinned: 2.0\n---\nBody B.\n")
	writeFrontmatterTestFile(t, root, "C.md", "---\npinned: 3.0\n---\nBody C.\n")
	unchanged := "---\npinned: 4.5\n---\nBody D.\n"
	writeFrontmatterTestFile(t, root, "D.md", unchanged)

	result, err := SetFrontmatterAtomic(
		root,
		atomicPinWrites(t,
			[]string{"A.md", "sub/B.md", "C.md", "D.md"},
			[]string{"10.5", "20.5", "30.5", "4.5"}),
		&config.FrontmatterConfig{},
		WithFrontmatterJournalOptions(
			journal.WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
			journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-atomic-set", nil }),
		),
	)
	if err != nil {
		t.Fatalf("SetFrontmatterAtomic: %v", err)
	}
	if !result.Changed {
		t.Fatal("Changed = false, want true")
	}
	if len(result.Sources) != 4 {
		t.Fatalf("Sources = %#v, want one entry per write", result.Sources)
	}
	if result.Journal == nil || result.Journal.ID != "entry-atomic-set" {
		t.Fatalf("journal = %#v, want one deterministic entry", result.Journal)
	}
	if len(result.Journal.Files) != 3 {
		t.Fatalf("journal files = %#v, want exactly the 3 changed notes", result.Journal.Files)
	}
	for _, rel := range []string{"A.md", "sub/B.md", "C.md"} {
		rec := frontmatterRecordForPath(t, *result.Journal, rel)
		if rec.Before.Kind != journal.KindFile || rec.After.Kind != journal.KindFile {
			t.Fatalf("journal record for %s = %#v, want file -> file", rel, rec)
		}
	}
	for rel, want := range map[string]string{
		"A.md":     "pinned: 10.5",
		"sub/B.md": "pinned: 20.5",
		"C.md":     "pinned: 30.5",
	} {
		if got := readFrontmatterTestFile(t, root, rel); !strings.Contains(got, want) {
			t.Fatalf("%s missing %q:\n%s", rel, want, got)
		}
	}
	if got := readFrontmatterTestFile(t, root, "D.md"); got != unchanged {
		t.Fatalf("unchanged note was rewritten: %q", got)
	}
}

func TestSetFrontmatterAtomicWritesNothingWhenNoNoteChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	body := "---\npinned: 1.5\n---\nBody.\n"
	writeFrontmatterTestFile(t, root, "A.md", body)

	result, err := SetFrontmatterAtomic(root, atomicPinWrites(t, []string{"A.md"}, []string{"1.5"}), &config.FrontmatterConfig{})
	if err != nil {
		t.Fatalf("SetFrontmatterAtomic: %v", err)
	}
	if result.Changed || result.Journal != nil {
		t.Fatalf("result = %#v, want unchanged without journal", result)
	}
	if got := readFrontmatterTestFile(t, root, "A.md"); got != body {
		t.Fatalf("note bytes = %q, want %q", got, body)
	}
	if _, err := os.Stat(JournalRoot(root)); !os.IsNotExist(err) {
		t.Fatalf("journal root stat err = %v, want missing", err)
	}
}

func TestSetFrontmatterAtomicRejectsInvalidWriteSets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontmatterTestFile(t, root, "A.md", "---\npinned: 1.0\n---\nBody.\n")

	cases := map[string]struct {
		writes    []NoteFrontmatterWrite
		wantInMsg string
	}{
		"empty":     {writes: nil, wantInMsg: "no notes selected"},
		"duplicate": {writes: atomicPinWrites(t, []string{"A.md", "A.md"}, []string{"1.5", "2.5"}), wantInMsg: "twice"},
		"empty path": {writes: []NoteFrontmatterWrite{{RelPath: "  "}},
			wantInMsg: "empty note path"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := SetFrontmatterAtomic(root, tc.writes, &config.FrontmatterConfig{})
			if CodeOf(err) != CodeInvalidRequest {
				t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeInvalidRequest, err)
			}
			if !strings.Contains(MessageOf(err), tc.wantInMsg) {
				t.Fatalf("message = %q, want it to mention %q", MessageOf(err), tc.wantInMsg)
			}
		})
	}
}

// legacyAtomicFrontmatterCap is the fixed limit this call used to refuse at.
// Above it a whole-list pin renormalize rendered fine and could never commit.
const legacyAtomicFrontmatterCap = 256

// largeAtomicSet builds a set well past the legacy cap.
func largeAtomicSet(t *testing.T, root string) (rels []string, ranks []string) {
	t.Helper()
	return atomicSetOfSize(t, root, legacyAtomicFrontmatterCap+44)
}

func atomicSetOfSize(t *testing.T, root string, count int) (rels []string, ranks []string) {
	t.Helper()
	for i := 0; i < count; i++ {
		rel := fmt.Sprintf("N%05d.md", i)
		writeFrontmatterTestFile(t, root, rel, fmt.Sprintf("---\ntitle: N%05d\n---\nBody %d.\n", i, i))
		rels = append(rels, rel)
		ranks = append(ranks, fmt.Sprintf("%d.0", i+1))
	}
	return rels, ranks
}

// TestSetFrontmatterAtomicCommitsPastTheLegacyCap is the whole-list renormalize
// case: every pinned note is renumbered at once, so refusing the set at a fixed
// 256 left a long pinned list orderable in the browser and unorderable on disk.
func TestSetFrontmatterAtomicCommitsPastTheLegacyCap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rels, ranks := largeAtomicSet(t, root)

	result, err := SetFrontmatterAtomic(root, atomicPinWrites(t, rels, ranks), &config.FrontmatterConfig{},
		WithFrontmatterLockEscalationForTest(testLockEscalation))
	if err != nil {
		t.Fatalf("SetFrontmatterAtomic over %d notes: %v", len(rels), err)
	}
	if !result.Changed || result.Journal == nil {
		t.Fatalf("result = %#v, want a changed set with one journal entry", result)
	}
	if len(result.Journal.Files) != len(rels) {
		t.Fatalf("journal files = %d, want %d in the one entry", len(result.Journal.Files), len(rels))
	}
	entries, err := os.ReadDir(JournalRoot(root))
	if err != nil {
		t.Fatalf("read journal root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want 1 undoable action", len(entries))
	}
	for i, rel := range rels {
		want := "pinned: " + ranks[i] + "\n"
		if got := readFrontmatterTestFile(t, root, rel); !strings.Contains(got, want) {
			t.Fatalf("%s missing %q:\n%s", rel, want, got)
		}
	}
}

// testLockEscalation stands in for vault.MaxLockedPaths so the escalation
// boundary is crossed with dozens of notes rather than thousands of fsyncs.
const testLockEscalation = 32

// beyondLockedPathCeiling builds a set larger than the number of paths one
// transaction locks individually, so the commit has to escalate to the
// vault-wide lock. It is the size this call used to refuse outright, which left a
// pinned list past that boundary orderable in the browser and unorderable on
// disk. Callers pass WithFrontmatterLockEscalationForTest(testLockEscalation).
func beyondLockedPathCeiling(t *testing.T, root string) (rels []string, ranks []string) {
	t.Helper()
	return atomicSetOfSize(t, root, testLockEscalation+8)
}

// TestSetFrontmatterAtomicCommitsBeyondLockedPathCeiling is the size boundary
// itself: the whole set is renumbered in one transaction and lands as one
// undoable action even though it names more paths than the process holds lock
// descriptors for.
func TestSetFrontmatterAtomicCommitsBeyondLockedPathCeiling(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rels, ranks := beyondLockedPathCeiling(t, root)

	result, err := SetFrontmatterAtomic(root, atomicPinWrites(t, rels, ranks), &config.FrontmatterConfig{})
	if err != nil {
		t.Fatalf("SetFrontmatterAtomic over %d notes: %v", len(rels), err)
	}
	if !result.Changed || result.Journal == nil {
		t.Fatalf("result = %#v, want a changed set with one journal entry", result)
	}
	if len(result.Journal.Files) != len(rels) {
		t.Fatalf("journal files = %d, want %d in the one entry", len(result.Journal.Files), len(rels))
	}
	entries, err := os.ReadDir(JournalRoot(root))
	if err != nil {
		t.Fatalf("read journal root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want 1 undoable action", len(entries))
	}
	for i, rel := range rels {
		want := "pinned: " + ranks[i] + "\n"
		if got := readFrontmatterTestFile(t, root, rel); !strings.Contains(got, want) {
			t.Fatalf("%s missing %q:\n%s", rel, want, got)
		}
	}
}

// TestSetFrontmatterAtomicRefusesAStaleNoteBeyondLockedPathCeiling proves
// escalation bought size without giving up all-or-nothing: one note replaced
// after planning still leaves every other note in the oversized set untouched and
// journals nothing.
func TestSetFrontmatterAtomicRefusesAStaleNoteBeyondLockedPathCeiling(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rels, ranks := beyondLockedPathCeiling(t, root)
	before := make(map[string]string, len(rels))
	for _, rel := range rels {
		before[rel] = readFrontmatterTestFile(t, root, rel)
	}

	stale := rels[len(rels)/2]
	concurrent := "---\npinned: 99.0\n---\nSomeone else wrote it.\n"
	_, err := SetFrontmatterAtomic(root, atomicPinWrites(t, rels, ranks), &config.FrontmatterConfig{},
		WithFrontmatterLockEscalationForTest(testLockEscalation),
		withFrontmatterBeforeAtomicCommit(func() error {
			return os.WriteFile(filepath.Join(root, stale), []byte(concurrent), 0o644)
		}),
	)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	for _, rel := range rels {
		want := before[rel]
		if rel == stale {
			want = concurrent
		}
		if got := readFrontmatterTestFile(t, root, rel); got != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(JournalRoot(root)); !os.IsNotExist(err) {
		t.Fatalf("journal root stat err = %v, want missing", err)
	}
}

// TestSetFrontmatterAtomicRefusesALargeSetWhenOneNoteGoesStale proves the
// all-or-nothing rule still holds past the legacy cap: one note replaced after
// planning has to leave every other note in the set untouched.
func TestSetFrontmatterAtomicRefusesALargeSetWhenOneNoteGoesStale(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rels, ranks := largeAtomicSet(t, root)
	before := make(map[string]string, len(rels))
	for _, rel := range rels {
		before[rel] = readFrontmatterTestFile(t, root, rel)
	}

	stale := rels[len(rels)/2]
	concurrent := "---\npinned: 99.0\n---\nSomeone else wrote it.\n"
	_, err := SetFrontmatterAtomic(root, atomicPinWrites(t, rels, ranks), &config.FrontmatterConfig{},
		withFrontmatterBeforeAtomicCommit(func() error {
			return os.WriteFile(filepath.Join(root, stale), []byte(concurrent), 0o644)
		}),
	)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeConflict, err)
	}
	for _, rel := range rels {
		want := before[rel]
		if rel == stale {
			want = concurrent
		}
		if got := readFrontmatterTestFile(t, root, rel); got != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(JournalRoot(root)); !os.IsNotExist(err) {
		t.Fatalf("journal root stat err = %v, want missing", err)
	}
}
