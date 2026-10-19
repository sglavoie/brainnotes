package vaultgit

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestEntryKindsAsksAboutExactlyTheNamesItIsGiven is the whole point of the
// method: the caller reaching for it is asking what is under *that* name, because
// it is about to decide whether two names that a folding filesystem cannot tell
// apart are both live. An answer that folded too would be no answer at all.
//
// The kind rather than a yes or no is the other half. A name that was a file and
// is now a directory is occupied and holds no file, and a caller that could only
// learn "occupied" would refuse the one history where that is exactly what
// legitimately happened.
func TestEntryKindsAsksAboutExactlyTheNamesItIsGiven(t *testing.T) {
	t.Parallel()
	h, commits := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	ctx := context.Background()
	tip := commits[len(commits)-1]

	for _, tc := range []struct {
		commit, path string
		want         string
	}{
		{commit: tip, path: "Alpha.md", want: KindBlob},
		{commit: tip, path: "Notes/Gamma.md", want: KindBlob},
		// The first commit predates Notes/Gamma.md, so the same question has a
		// different answer there — which is what makes it worth asking per commit.
		{commit: commits[0], path: "Notes/Gamma.md", want: ""},
		{commit: tip, path: "alpha.md", want: ""},
		{commit: tip, path: "ALPHA.MD", want: ""},
		{commit: tip, path: "Missing.md", want: ""},
		// The empty path is not the root tree here, whatever `<commit>:` means to
		// git rev-parse.
		{commit: tip, path: "", want: ""},
		// A directory is an entry and reports as one, which is the distinction the
		// kind exists to carry.
		{commit: tip, path: "Notes", want: KindTree},
		// A leading dash is data: the names go on stdin, never in an argv.
		{commit: tip, path: "--cached", want: ""},
		// A path that cannot be framed on that stdin takes the other road, and has
		// to come back with the same answer.
		{commit: tip, path: "Two\nLines.md", want: ""},
	} {
		kinds, err := view.EntryKinds(ctx, tc.commit, []string{tc.path})
		if err != nil {
			t.Fatalf("EntryKinds(%s, %q): %v", tc.commit[:7], tc.path, err)
		}
		if got := kinds[tc.path]; got != tc.want {
			t.Errorf("EntryKinds(%s, %q) = %q, want %q", tc.commit[:7], tc.path, got, tc.want)
		}
	}

	// A directory and something inside it, in one question. This is the case
	// `ls-tree` with both as operands answers wrongly: the deeper pathspec makes it
	// descend through `Notes` rather than report it, and the caller comparing a
	// name against the names underneath it gets silence for the entry it asked
	// about — which reads as "that name is free".
	kinds, err := view.EntryKinds(ctx, tip, []string{"Notes", "Notes/Gamma.md", "Alpha.md", "alpha.md"})
	if err != nil {
		t.Fatalf("EntryKinds with several paths: %v", err)
	}
	want := map[string]string{"Notes": KindTree, "Notes/Gamma.md": KindBlob, "Alpha.md": KindBlob}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("EntryKinds: got %v, want %v", kinds, want)
	}

	// And it reads committed objects only, like every other method here.
	h.write("Fresh.md", "never committed\n")
	kinds, err = view.EntryKinds(ctx, tip, []string{"Fresh.md"})
	if err != nil {
		t.Fatalf("EntryKinds after an uncommitted write: %v", err)
	}
	if len(kinds) != 0 {
		t.Fatalf("EntryKinds reported an uncommitted file: %v", kinds)
	}
}

// TestEntryKindsRefusesACommitItCannotResolve keeps a wrong cursor from reading
// as a clean one.
//
// `cat-file --batch-check` answers `missing` for every path of a commit that is
// not there and exits zero, so without a question about the commit's own tree an
// absent commit and a commit holding none of these names are the same answer. In
// the caller this exists for — a gate that refuses to publish a path a peer
// cannot hold — "holds nothing" is the answer that publishes.
func TestEntryKindsRefusesACommitItCannotResolve(t *testing.T) {
	t.Parallel()
	h, _ := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	// Well-formed, so checkObjectNames lets it through, and absent.
	const absent = "1111111111111111111111111111111111111111"
	kinds, err := view.EntryKinds(context.Background(), absent, []string{"Alpha.md"})
	if err == nil {
		t.Fatalf("EntryKinds on an absent commit reported %v, want an error", kinds)
	}
	// Pinned to the tree probe specifically: any error at all would also be
	// produced by git refusing the revision outright, and that is a guard this
	// test is not about.
	if !strings.Contains(err.Error(), "names no tree") {
		t.Errorf("EntryKinds: got %v, want the tree probe to refuse it", err)
	}
}

// TestEntryKindsNamesAGitlinkOneWay is the agreement between the two roads.
//
// Git names a gitlink `commit` when the superproject happens to hold the
// pointed-at object and `submodule` when it does not, and ls-tree names it by
// mode. All three are the same entry, and a caller that saw two of those spellings
// would have to know which road its path took to read the answer.
func TestEntryKindsNamesAGitlinkOneWay(t *testing.T) {
	t.Parallel()
	h, commits := historyFixture(t)
	// The resolvable spelling points at a commit this repository has; the absent
	// one does not, which is what a real submodule looks like from outside.
	const absent = "2222222222222222222222222222222222222222"
	const framed, present, unframed = "Linked", "Present", "Two\nLinked"
	rawGit(t, h.root, "read-tree", "HEAD")
	rawGit(t, h.root, "update-index", "--add", "--cacheinfo", "160000,"+absent+","+framed)
	rawGit(t, h.root, "update-index", "--add", "--cacheinfo", "160000,"+commits[0]+","+present)
	rawGit(t, h.root, "update-index", "--add", "--cacheinfo", "160000,"+absent+","+unframed)
	tree := rawGit(t, h.root, "write-tree")
	commit := rawGit(t, h.root, "commit-tree", tree, "-p", "HEAD", "-m", "add gitlinks")
	rawGit(t, h.root, "update-ref", "HEAD", commit)

	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	// framed and present go over stdin — one answered `submodule` and the other
	// `commit`, because git can read one of the two objects — and unframed takes
	// the ls-tree road. All three are gitlinks and have to come back as one kind.
	kinds, err := view.EntryKinds(context.Background(), commit, []string{framed, present, unframed})
	if err != nil {
		t.Fatalf("EntryKinds: %v", err)
	}
	want := map[string]string{framed: KindSubmodule, present: KindSubmodule, unframed: KindSubmodule}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("EntryKinds: got %v, want %v", kinds, want)
	}
	// The agreement above is only worth something while git still spells the two
	// gitlinks differently. Asked raw, `framed` is the two-field `submodule` shape
	// and `present` the three-field `commit` one; if a git ever unified them this
	// probe is what says so, instead of the test quietly proving nothing.
	stdin := fmt.Sprintf("%s:%s\n%s:%s\n", commit, framed, commit, present)
	out, err := view.git.run(context.Background(), gitCall{
		args:  []string{"cat-file", "--batch-check"},
		stdin: []byte(stdin),
	})
	if err != nil {
		t.Fatalf("cat-file --batch-check: %v", err)
	}
	records := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(records) != 2 {
		t.Fatalf("cat-file --batch-check: got %q, want one record per gitlink", out)
	}
	if got := strings.Fields(records[0]); len(got) != 2 || got[1] != "submodule" {
		t.Errorf("unresolvable gitlink: got %q, want `<oid> submodule`", records[0])
	}
	if got := strings.Fields(records[1]); len(got) != 3 || got[1] != "commit" {
		t.Errorf("resolvable gitlink: got %q, want `<oid> commit <size>`", records[1])
	}
}

// TestEntryKindsRefusesANameANULWouldTruncate is the one framing failure that is
// worse than an error: git reads the record as a C string, so `Alpha.md\0zzz`
// resolves as `Alpha.md` and comes back as a blob — a kind for a name the tree
// does not hold, which in the collision gate is a rival that is not there.
func TestEntryKindsRefusesANameANULWouldTruncate(t *testing.T) {
	t.Parallel()
	h, commits := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	kinds, err := view.EntryKinds(context.Background(), commits[len(commits)-1], []string{"Alpha.md\x00zzz"})
	if err == nil {
		t.Fatalf("EntryKinds reported %v for a NUL-bearing name, want an error", kinds)
	}
	// The refusal has to be about the NUL. A name that merely failed to resolve
	// would come back as no entry rather than an error, so an unpinned assertion
	// here would keep passing if the byte stopped being the reason.
	if !strings.Contains(err.Error(), "NUL") {
		t.Errorf("EntryKinds: got %v, want the NUL named as the reason", err)
	}
}

// TestEntryKindsReadsANameItCannotFrameOnStdin covers the fallback with a name
// that is actually in the tree, so "the newline road works" is not proved only by
// a path that was missing either way.
func TestEntryKindsReadsANameItCannotFrameOnStdin(t *testing.T) {
	t.Parallel()
	h, _ := historyFixture(t)
	const awkward = "Two\nLines.md"
	h.record("bn new", []string{awkward, "Plain.md"}, func() {
		h.write(awkward, "a note whose name holds a newline\n")
		h.write("Plain.md", "an ordinary note\n")
	})
	h.commitPending()

	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	// Both roads in one call: the awkward name goes one at a time and the ordinary
	// ones share a batch, and the caller sees one map either way.
	kinds, err := view.EntryKinds(context.Background(), h.head(), []string{awkward, "Plain.md", "Notes"})
	if err != nil {
		t.Fatalf("EntryKinds: %v", err)
	}
	want := map[string]string{awkward: KindBlob, "Plain.md": KindBlob, "Notes": KindTree}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("EntryKinds: got %v, want %v", kinds, want)
	}
}
