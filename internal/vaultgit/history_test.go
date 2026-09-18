package vaultgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// treeDigest fingerprints every entry under dir by path, mode, and content.
//
// It walks `.git` too, deliberately: the schedule this test is about lives under
// `.git/brainnotes-safety/`, and so does the recovery intent. Contents rather
// than timestamps, because a reader that only stat'ed a file is allowed to have
// stat'ed it.
func treeDigest(t *testing.T, dir string) map[string]string {
	t.Helper()
	digest := map[string]string{}
	err := filepath.WalkDir(dir, func(abs string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, abs)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			digest[rel] = "dir"
			return nil
		}
		if !info.Mode().IsRegular() {
			digest[rel] = fmt.Sprintf("irregular %v", info.Mode())
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		digest[rel] = fmt.Sprintf("%v %s", info.Mode().Perm(), hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", dir, err)
	}
	return digest
}

// requireSameTree fails naming every path that appeared, vanished, or changed.
func requireSameTree(t *testing.T, before, after map[string]string, what string) {
	t.Helper()
	paths := map[string]struct{}{}
	for p := range before {
		paths[p] = struct{}{}
	}
	for p := range after {
		paths[p] = struct{}{}
	}
	var sorted []string
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		was, had := before[p]
		is, has := after[p]
		switch {
		case had && !has:
			t.Errorf("%s removed %s", what, p)
		case !had && has:
			t.Errorf("%s created %s", what, p)
		case was != is:
			t.Errorf("%s changed %s: %s -> %s", what, p, was, is)
		}
	}
}

// historyFixture is a vault with two snapshot commits and a watch schedule
// already on disk, which is the state a reader has to be harmless against: an
// autosave floor and a debounce deadline that a *writer* opening this repository
// would legitimately advance.
func historyFixture(t *testing.T) (*testHarness, []string) {
	t.Helper()
	h := newHarness(t)

	h.record("bn new", []string{"Alpha.md"}, func() { h.write("Alpha.md", "alpha\n") })
	h.commitPending()
	h.advance(h.mgr.AutosavePolicy().Interval * 4)
	h.record("bn set", []string{"Alpha.md", "Notes/Gamma.md"}, func() {
		h.write("Alpha.md", "alpha edited\n")
		h.write("Notes/Gamma.md", "gamma\n")
	})
	h.commitPending()

	commits := strings.Fields(rawGit(t, h.root, "rev-list", "--reverse", "HEAD"))
	if len(commits) != 2 {
		t.Fatalf("fixture built %d commits, want 2", len(commits))
	}
	return h, commits
}

// readEverything calls every method a History has, so the no-write assertion is
// made about the whole surface rather than about whichever one a test happened
// to reach for. A method added without a line here is a method this test stops
// covering, and nothing here catches that for you.
func readEverything(t *testing.T, view *History, commits []string) {
	t.Helper()
	ctx := context.Background()

	_ = view.Root()
	_ = view.Branch()
	_ = view.Watch()
	if _, _, err := view.Blocked(); err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	tip, err := view.Tip(ctx)
	if err != nil {
		t.Fatalf("Tip: %v", err)
	}
	if _, err := view.Contains(ctx, commits[0], tip); err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if _, err := view.CommitsAfter(ctx, "", tip); err != nil {
		t.Fatalf("CommitsAfter from the root: %v", err)
	}
	if _, err := view.CommitsAfter(ctx, commits[0], tip); err != nil {
		t.Fatalf("CommitsAfter: %v", err)
	}
	for _, commit := range commits {
		readCommit(t, view, commit)
	}
}

// readCommit calls every method that takes a commit, including the object reads
// its changes lead to.
func readCommit(t *testing.T, view *History, commit string) {
	t.Helper()
	ctx := context.Background()

	if _, err := view.Inspect(ctx, commit); err != nil {
		t.Fatalf("Inspect %s: %v", commit, err)
	}
	changes, err := view.Changes(ctx, commit)
	if err != nil {
		t.Fatalf("Changes %s: %v", commit, err)
	}
	var oids []string
	for _, change := range changes {
		if change.NewOID != "" {
			oids = append(oids, change.NewOID)
		}
	}
	if _, err := view.Blobs(ctx, oids); err != nil {
		t.Fatalf("Blobs: %v", err)
	}
	for _, oid := range oids {
		if _, _, err := view.BlobDigest(ctx, oid); err != nil {
			t.Fatalf("BlobDigest %s: %v", oid, err)
		}
	}
	if _, err := view.Tree(ctx, commit); err != nil {
		t.Fatalf("Tree %s: %v", commit, err)
	}
	if _, err := view.EntryKinds(ctx, commit, []string{"Alpha.md"}); err != nil {
		t.Fatalf("EntryKinds %s: %v", commit, err)
	}
}

// TestHistoryReadsWithoutDisturbingTheVault is the requirement that export
// observation must not alter scheduled external-edit timing.
//
// The debounce deadline and the autosave floor live in `watch.json` under the
// state directory, and a Manager opened for writing legitimately rewrites them.
// A reader that did the same would move the next autosave every time somebody
// asked what there was to export — an observation with the power to disturb what
// it observes. The assertion is made over the whole vault, working tree and
// repository alike, so it also covers the index, the refs, and the recovery
// intent rather than only the one file the bug would most likely land in.
func TestHistoryReadsWithoutDisturbingTheVault(t *testing.T) {
	h, commits := historyFixture(t)

	stateDir := h.mgr.StateDir()
	before := treeDigest(t, h.root)
	beforeState := treeDigest(t, stateDir)

	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	// Opening alone must already be harmless: a reader that only created its
	// state directory on open would still have written to the vault.
	requireSameTree(t, beforeState, treeDigest(t, stateDir), "OpenHistory")

	readEverything(t, view, commits)

	requireSameTree(t, beforeState, treeDigest(t, stateDir), "reading the history")
	requireSameTree(t, before, treeDigest(t, h.root), "reading the history")
}

// TestHistoryTakesNoLock proves the reader is harmless in the other direction
// too: a mutation is still free to proceed while a History is open, so a
// long-running export cannot stall a person's save.
func TestHistoryTakesNoLock(t *testing.T) {
	h, commits := historyFixture(t)

	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	readEverything(t, view, commits)

	h.advance(h.mgr.AutosavePolicy().Interval * 4)
	h.record("bn set", []string{"Beta.md"}, func() { h.write("Beta.md", "beta\n") })
	h.commitPending()

	tip, err := view.Tip(context.Background())
	if err != nil {
		t.Fatalf("Tip after the write: %v", err)
	}
	if tip != h.head() {
		t.Fatalf("the view resolved %q, want the new tip %q", tip, h.head())
	}
	// The view was opened before that commit existed and reports it without being
	// reopened, because it holds no snapshot of anything.
	after, err := view.CommitsAfter(context.Background(), commits[len(commits)-1], tip)
	if err != nil {
		t.Fatalf("CommitsAfter: %v", err)
	}
	if len(after) != 1 || after[0] != tip {
		t.Fatalf("commits after the fixture: got %v, want [%s]", after, tip)
	}
}

// TestHistoryIgnoresUncommittedEdits is the property the exporter is built on:
// every method reads committed objects, so a half-typed note is invisible by
// construction rather than by a filter somebody has to remember to apply.
func TestHistoryIgnoresUncommittedEdits(t *testing.T) {
	h, commits := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	tip := commits[len(commits)-1]

	h.write("Alpha.md", "half a sentence typed into an open editor")
	h.write("Draft.md", "brand new and unsaved\n")

	got, err := view.Tip(context.Background())
	if err != nil {
		t.Fatalf("Tip: %v", err)
	}
	if got != tip {
		t.Fatalf("an uncommitted edit moved the tip to %q, want %q", got, tip)
	}
	files, err := view.Tree(context.Background(), tip)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	for _, file := range files {
		if file.Path == "Draft.md" {
			t.Fatal("an uncommitted file is in the committed tree")
		}
		if file.Path != "Alpha.md" {
			continue
		}
		blobs, err := view.Blobs(context.Background(), []string{file.OID})
		if err != nil {
			t.Fatalf("Blobs: %v", err)
		}
		if got := string(blobs[file.OID]); got != "alpha edited\n" {
			t.Fatalf("Alpha.md reads %q, want the committed bytes", got)
		}
	}
}

// TestOpenHistoryRefusesWhatOpenRefuses pins the two views to one set of
// structural refusals. They share openWorkingTree and resolveGitDir precisely so
// they cannot disagree about what a vault is; a reader that accepted a
// repository the writer rejects would report a history nothing else can commit
// to.
func TestOpenHistoryRefusesWhatOpenRefuses(t *testing.T) {
	requireGit(t)

	t.Run("no repository", func(t *testing.T) {
		isolateHome(t)
		_, err := OpenHistory(context.Background(), t.TempDir(), HistoryOptions{})
		if !IsUnmanaged(err) {
			t.Fatalf("OpenHistory on a non-Git vault = %v, want ErrUnmanaged", err)
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		isolateHome(t)
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
			t.Fatalf("write .git file: %v", err)
		}
		if _, err := OpenHistory(context.Background(), root, HistoryOptions{}); KindOf(err) != KindRepositoryState {
			t.Fatalf("OpenHistory with a .git file = %v, want a repository-state failure", err)
		}
	})

	t.Run("detached head", func(t *testing.T) {
		h, commits := historyFixture(t)
		rawGit(t, h.root, "checkout", "-q", "--detach", commits[0])
		_, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
		if KindOf(err) != KindRepositoryState {
			t.Fatalf("OpenHistory on a detached HEAD = %v, want a repository-state failure", err)
		}
		if !strings.Contains(err.Error(), "detached") {
			t.Fatalf("the refusal must say why: %v", err)
		}
	})
}

// TestTipReportsAnUnbornBranchAsItsOwnThing separates "this vault has no history
// yet" from "this repository is broken". The first is an ordinary state for a
// vault enrolled before its first commit and means there is nothing to export;
// the second must never be read that way.
func TestTipReportsAnUnbornBranchAsItsOwnThing(t *testing.T) {
	requireGit(t)
	isolateHome(t)
	root := t.TempDir()
	rawGit(t, root, "-c", "init.defaultBranch=main", "init", "-q")

	view, err := OpenHistory(context.Background(), root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	if _, err := view.Tip(context.Background()); !errors.Is(err, ErrUnbornBranch) {
		t.Fatalf("Tip on an unborn branch = %v, want ErrUnbornBranch", err)
	}
}

// TestCommitsAfterIsOldestFirst pins the replay order. `rev-list` is newest-first
// and `-n` selects *before* `--reverse` reverses, so a bounded query would return
// the newest few in oldest-first order — which reads as correct and would replay
// a vault's history backwards from the middle.
func TestCommitsAfterIsOldestFirst(t *testing.T) {
	h, commits := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	tip, err := view.Tip(context.Background())
	if err != nil {
		t.Fatalf("Tip: %v", err)
	}

	all, err := view.CommitsAfter(context.Background(), "", tip)
	if err != nil {
		t.Fatalf("CommitsAfter: %v", err)
	}
	if strings.Join(all, " ") != strings.Join(commits, " ") {
		t.Fatalf("commits: got %v, want %v", all, commits)
	}
	// And a base that is already the tip has nothing after it.
	rest, err := view.CommitsAfter(context.Background(), tip, tip)
	if err != nil {
		t.Fatalf("CommitsAfter from the tip: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("commits after the tip: %v", rest)
	}
}

// TestHistoryRefusesObjectNamesItDidNotProduce is checkObjectNames at the
// boundary. Every name these methods take came from git, so the only way one of
// these strings arrives is a corrupt cursor read back off a disk — and it must
// fail as that, rather than reaching rev-list as a range of its own devising.
func TestHistoryRefusesObjectNamesItDidNotProduce(t *testing.T) {
	h, commits := historyFixture(t)
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	tip := commits[len(commits)-1]

	for _, bad := range []string{"--all", "HEAD", "", commits[0][:7], strings.ToUpper(commits[0])} {
		requireRefusedObjectName(t, view, tip, bad)
	}
	// An empty base is the one exception, and it means "from the root commit".
	if _, err := view.CommitsAfter(context.Background(), "", tip); err != nil {
		t.Fatalf("CommitsAfter with no base: %v", err)
	}
}

// objectNameProbe is one method call made with a name checkObjectNames must
// refuse, paired with the name to report it by.
type objectNameProbe struct {
	method string
	call   func() error
}

// requireRefusedObjectName fails for every method that accepted name, so a
// method added without a refusal is a method this test reports.
func requireRefusedObjectName(t *testing.T, view *History, tip, name string) {
	t.Helper()
	ctx := context.Background()

	// A table rather than a run of ifs, so adding the next method's probe is one
	// line and the complexity ratchet stays out of the way of doing that.
	probes := []objectNameProbe{
		{"CommitsAfter tip", func() error { _, err := view.CommitsAfter(ctx, "", name); return err }},
		{"Inspect", func() error { _, err := view.Inspect(ctx, name); return err }},
		{"Changes", func() error { _, err := view.Changes(ctx, name); return err }},
		{"Tree", func() error { _, err := view.Tree(ctx, name); return err }},
		{"Blobs", func() error { _, err := view.Blobs(ctx, []string{name}); return err }},
		{"BlobDigest", func() error { _, _, err := view.BlobDigest(ctx, name); return err }},
		{"Contains", func() error { _, err := view.Contains(ctx, name, tip); return err }},
		{"EntryKinds", func() error { _, err := view.EntryKinds(ctx, name, []string{"Alpha.md"}); return err }},
	}
	// An empty base is CommitsAfter's one legitimate value — it means "from the
	// root commit" — and its caller asserts that separately.
	if name != "" {
		probes = append(probes, objectNameProbe{
			"CommitsAfter base",
			func() error { _, err := view.CommitsAfter(ctx, name, tip); return err },
		})
	}

	for _, probe := range probes {
		if err := probe.call(); err == nil {
			t.Errorf("%s accepted %q", probe.method, name)
		}
	}
}

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
